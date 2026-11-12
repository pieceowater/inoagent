package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app/internal/core/cfg"
	"app/internal/core/telegram"
	"app/internal/pkg/agent/ent"
)

func TestTopicName(t *testing.T) {
	created := &telegram.Message{ForumTopicCreated: &telegram.ForumTopic{Name: "lota"}}
	cases := []struct {
		m    *telegram.Message
		want string
	}{
		{created, "lota"},
		{&telegram.Message{ReplyToMessage: created}, "lota"},
		{&telegram.Message{ForumTopicEdited: &telegram.ForumTopic{Name: "LOTA tools"}}, "LOTA tools"},
		{&telegram.Message{ForumTopicEdited: &telegram.ForumTopic{}}, ""}, // icon change only
		{&telegram.Message{ReplyToMessage: &telegram.Message{Text: "hi"}}, ""},
	}
	for _, c := range cases {
		if got := c.m.TopicName(); got != c.want {
			t.Errorf("TopicName() = %q, want %q", got, c.want)
		}
	}
}

func TestTopicFile(t *testing.T) {
	for name, want := range map[string]string{
		"lota":          "topics/lota.md",
		"Сервак итд":    "topics/сервак-итд.md",
		"  LOTA / web ": "topics/lota-web.md",
		"!!!":           "",
	} {
		if got := TopicFile(name); got != want {
			t.Errorf("TopicFile(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestTopicContext(t *testing.T) {
	ws := t.TempDir()
	s := &AgentService{cfg: &cfg.Config{Workspace: ws}}
	if p, h := s.topicContext("lota", ent.Session{}, false); p != "" || h != "" {
		t.Fatal("no file, no context")
	}
	_ = os.MkdirAll(filepath.Join(ws, "topics"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, "topics", "lota.md"), []byte("# LOTOF handoff"), 0o644)

	// A new conversation gets the file.
	p, h := s.topicContext("lota", ent.Session{}, false)
	if !strings.Contains(p, "# LOTOF handoff") || h == "" {
		t.Fatalf("new session: %q %q", p, h)
	}
	// A resumed one that already has this version gets nothing again…
	if p2, h2 := s.topicContext("lota", ent.Session{Context: h}, true); p2 != "" || h2 != h {
		t.Fatalf("resumed, same file: %q", p2)
	}
	// …until the file changes.
	_ = os.WriteFile(filepath.Join(ws, "topics", "lota.md"), []byte("# LOTOF handoff v2"), 0o644)
	p3, h3 := s.topicContext("lota", ent.Session{Context: h}, true)
	if !strings.Contains(p3, "обновился") || !strings.Contains(p3, "v2") || h3 == h {
		t.Fatalf("resumed, edited file: %q", p3)
	}
}
