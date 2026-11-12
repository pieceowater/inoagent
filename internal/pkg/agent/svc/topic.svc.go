package svc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"app/internal/core/telegram"
	"app/internal/pkg/agent/ent"
)

// Topics. Telegram tells the bot a topic's name only now and then (with the
// topic's creation, a rename, or a message that implicitly replies to the
// creation), so the name is remembered as seen. A topic can have its own
// context in the vault — topics/<name>.md: what the topic is about, how to
// work there. It is handed to Claude when a conversation in the topic starts
// and again whenever the file changes, not with every message.

// LearnTopic remembers the topic's name if this message carries it.
func (s *AgentService) LearnTopic(m *telegram.Message) {
	if name := strings.TrimSpace(m.TopicName()); name != "" && m.Thread() != 0 {
		if err := s.repo.SetTopicName(ent.KeyOf(m), name); err != nil {
			s.log.Error("save topic name", "err", err)
		}
	}
}

func (s *AgentService) TopicName(key ent.TopicKey) string { return s.repo.TopicName(key) }

var nonSlug = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// TopicFile is the vault-relative path of a topic's context file.
func TopicFile(name string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		return ""
	}
	return filepath.Join("topics", slug+".md")
}

// topicContext returns the text to put in front of the prompt (empty if the
// session already has the current version) and the file's hash to store.
func (s *AgentService) topicContext(name string, sess ent.Session, resumed bool) (prefix, hash string) {
	rel := TopicFile(name)
	if rel == "" {
		return "", ""
	}
	b, err := os.ReadFile(filepath.Join(s.cfg.Workspace, rel))
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return "", ""
	}
	sum := sha256.Sum256(b)
	hash = hex.EncodeToString(sum[:8])
	if resumed && sess.Context == hash {
		return "", hash
	}
	head := fmt.Sprintf("[Контекст темы «%s» — из vault `%s`. Это правила и знания для всей работы в этой теме: держи их в голове весь разговор.]", name, rel)
	if resumed && sess.Context != "" {
		head = fmt.Sprintf("[Контекст темы «%s» обновился (`%s`), новая версия целиком:]", name, rel)
	}
	return head + "\n" + string(b) + "\n[Конец контекста темы]\n\n", hash
}
