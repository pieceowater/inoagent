package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"app/internal/core/telegram"
)

// Every run gets an outbox: a temporary folder (INOAGENT_OUTBOX) where the
// agent puts what it made for people — a presentation, a document, a chart,
// an archive — directly or with the `send-file` helper. After the answer the
// bot sends everything in it to the same topic and deletes the folder, so
// results end up in the chat, not on the server.

func (s *AgentService) newOutbox(runID string) (string, error) {
	dir := filepath.Join(s.cfg.StateDir, "outbox", runID)
	return dir, os.MkdirAll(dir, 0o755)
}

var photoExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}

// deliverOutbox carries out what the run left in the outbox, as replies to m:
// the reaction, pin, polls, new topics, then the files; and removes the
// folder. answer is the last message the bot sent for this run, if any.
func (s *AgentService) deliverOutbox(ctx context.Context, m *telegram.Message, dir string, answer telegram.Message) {
	defer os.RemoveAll(dir)
	s.outboxActions(ctx, m, dir, answer)
	if data, err := os.ReadFile(filepath.Join(dir, reactFile)); err == nil {
		if e := reactionEmoji(string(data)); e == "" {
			s.log.Warn("unsupported reaction", "text", strings.TrimSpace(string(data)))
		} else if err := s.msg.Client().SetReaction(ctx, m.Chat.ID, m.MessageID, e); err != nil {
			s.log.Warn("set reaction failed", "emoji", e, "err", err)
		}
	}
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && !strings.HasPrefix(d.Name(), ".") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	for _, path := range files {
		name := filepath.Base(path)
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		if st.Size() > telegram.MaxUpload {
			s.msg.Reply(ctx, m, fmt.Sprintf("Файл `%s` больше 50 МБ — Telegram такой от бота не примет.", name))
			continue
		}
		// Opus in OGG (what voice-say writes) goes as a voice message.
		if strings.EqualFold(filepath.Ext(name), ".ogg") {
			if err := s.msg.Client().SendVoice(ctx, m.Chat.ID, m.Thread(), m.MessageID, path); err != nil {
				s.log.Warn("send voice failed", "name", name, "err", err)
				s.msg.Reply(ctx, m, fmt.Sprintf("Не смог отправить голосовое: %v", err))
			}
			continue
		}
		// Small images go as photos, to be seen in the chat; anything else —
		// and big images, which a photo would recompress — as documents.
		photo := photoExt[strings.ToLower(filepath.Ext(name))] && st.Size() < 5<<20
		if err := s.msg.Client().SendFile(ctx, m.Chat.ID, m.Thread(), m.MessageID, path, "", photo); err != nil {
			s.log.Warn("send file failed", "name", name, "err", err)
			s.msg.Reply(ctx, m, fmt.Sprintf("Не смог отправить `%s`: %v", name, err))
		}
	}
}

// reactFile is where the `react` helper leaves the emoji for the message the
// run is answering.
const reactFile = ".react"

// Telegram lets bots react only with these.
var allowedReactions = map[string]bool{}

func init() {
	for _, e := range strings.Fields("👍 👎 ❤ 🔥 🥰 👏 😁 🤔 🤯 😱 🤬 😢 🎉 🤩 🤮 💩 🙏 👌 🕊 🤡 🥱 🥴 😍 🐳 ❤‍🔥 🌚 🌭 💯 🤣 ⚡ 🍌 🏆 💔 🤨 😐 🍓 🍾 💋 🖕 😈 😴 😭 🤓 👻 👨‍💻 👀 🎃 🙈 😇 😨 🤝 ✍ 🤗 🫡 🎅 🎄 ☃ 💅 🤪 🗿 🆒 💘 🙉 🦄 😘 💊 🙊 😎 👾 🤷‍♂ 🤷 🤷‍♀ 😡") {
		allowedReactions[e] = true
	}
}

// reactionEmoji is the emoji to react with, or "" if Telegram would refuse it.
func reactionEmoji(s string) string {
	s = strings.TrimSpace(s)
	if allowedReactions[s] {
		return s
	}
	// Variation selectors often trail an emoji ("❤️"); Telegram's list has none.
	if t := strings.ReplaceAll(s, "\uFE0F", ""); allowedReactions[t] {
		return t
	}
	return ""
}

// Files starting with a dot in the outbox are instructions, not files to send:
//
//	.react        an emoji for the message being answered
//	.pin          "answer" (pin the bot's reply) or "target" (pin the message
//	              the request replies to, or the request itself)
//	.poll-*.json  a poll: {"question", "options", "anonymous", "multiple"}
//	.topic-*.json a new forum topic: {"name"}
func (s *AgentService) outboxActions(ctx context.Context, m *telegram.Message, dir string, answer telegram.Message) {
	if data, err := os.ReadFile(filepath.Join(dir, ".pin")); err == nil {
		id := answer.MessageID
		if strings.TrimSpace(string(data)) == "target" {
			id = m.MessageID
			if r := m.ReplyToMessage; r != nil && r.MessageID != m.MessageThreadID {
				id = r.MessageID
			}
		}
		if id == 0 {
			s.log.Warn("pin: nothing to pin")
		} else if err := s.msg.Client().PinMessage(ctx, m.Chat.ID, id); err != nil {
			s.log.Warn("pin failed", "err", err)
			s.msg.Reply(ctx, m, fmt.Sprintf("Не смог закрепить: %v. Проверь, что у бота есть право закреплять сообщения.", err))
		}
	}
	polls, _ := filepath.Glob(filepath.Join(dir, ".poll-*.json"))
	for _, path := range polls {
		var p telegram.Poll
		if data, err := os.ReadFile(path); err != nil || json.Unmarshal(data, &p) != nil || p.Question == "" || len(p.Options) < 2 {
			s.log.Warn("bad poll file", "path", filepath.Base(path))
			continue
		}
		if err := s.msg.Client().SendPoll(ctx, m.Chat.ID, m.Thread(), m.MessageID, p); err != nil {
			s.msg.Reply(ctx, m, fmt.Sprintf("Не смог создать опрос: %v", err))
		}
	}
	topics, _ := filepath.Glob(filepath.Join(dir, ".topic-*.json"))
	for _, path := range topics {
		var t struct {
			Name string `json:"name"`
		}
		if data, err := os.ReadFile(path); err != nil || json.Unmarshal(data, &t) != nil || strings.TrimSpace(t.Name) == "" {
			continue
		}
		thread, err := s.msg.Client().CreateTopic(ctx, m.Chat.ID, strings.TrimSpace(t.Name))
		if err != nil {
			s.msg.Reply(ctx, m, fmt.Sprintf("Не смог создать тему: %v. Нужно право управлять темами.", err))
			continue
		}
		s.msg.Reply(ctx, &telegram.Message{Chat: m.Chat, MessageThreadID: thread, IsTopicMessage: true},
			"Тема создана. Пиши сюда.")
	}
}
