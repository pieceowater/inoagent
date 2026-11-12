package ctrl

import (
	"os"
	"path/filepath"
	"testing"

	"app/internal/core/cfg"
	"app/internal/core/telegram"
)

func TestForSomeoneElse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, othersFile), []byte("# names\nадель\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	me := telegram.User{ID: 1, IsBot: true, Username: "inoagent_bot", FirstName: "Иноагент"}
	yury := &telegram.User{ID: 10, FirstName: "yury", Username: "pieceowater"}
	lena := &telegram.User{ID: 11, FirstName: "Лена", Username: "lena_x"}
	c := &AgentController{cfg: &cfg.Config{Workspace: dir}, me: me}
	c.people.see(yury)
	c.people.see(lena)

	group := telegram.Chat{ID: -100, Type: "supergroup"}
	msg := func(from *telegram.User, text string) *telegram.Message {
		return &telegram.Message{MessageID: 50, MessageThreadID: 2, IsTopicMessage: true, From: from, Chat: group, Text: text}
	}
	replyTo := func(m *telegram.Message, to *telegram.Message) *telegram.Message { m.ReplyToMessage = to; return m }

	tests := []struct {
		name string
		m    *telegram.Message
		want bool
	}{
		{"plain request", msg(yury, "глянь диск"), false},
		{"tag of someone else", msg(yury, "@lena_x ты где"), true},
		{"tag of us", msg(yury, "@inoagent_bot глянь диск"), false},
		{"both tagged", msg(yury, "@lena_x смотри что @inoagent_bot говорит"), false},
		{"name first", msg(yury, "Лена, ты где"), true},
		{"greeting then name", msg(yury, "эй, лена"), true},
		{"alias from file", msg(yury, "Адель, иди сюда"), true},
		{"own name", msg(lena, "Лена пишет"), false},
		{"name inside sentence", msg(yury, "скажи Лене что ужин"), false},
		{"called by name", msg(yury, "Иноагент, лена где"), false},
		{"reply to a person", replyTo(msg(yury, "да"), &telegram.Message{MessageID: 40, From: lena}), true},
		{"reply to us", replyTo(msg(yury, "да"), &telegram.Message{MessageID: 40, From: &me}), false},
		{"reply to own message", replyTo(msg(yury, "да"), &telegram.Message{MessageID: 40, From: yury}), false},
		{"topic root is not a reply", replyTo(msg(yury, "глянь диск"), &telegram.Message{MessageID: 2, From: lena}), false},
		{"text_mention of someone else", func() *telegram.Message {
			m := msg(yury, "эй ты")
			m.Entities = []telegram.MessageEntity{{Type: "text_mention", User: lena}}
			return m
		}(), true},
		{"private chat", &telegram.Message{From: yury, Chat: telegram.Chat{Type: "private"}, Text: "Лена"}, false},
	}
	for _, tt := range tests {
		if got := c.forSomeoneElse(tt.m, tt.m.Text); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
