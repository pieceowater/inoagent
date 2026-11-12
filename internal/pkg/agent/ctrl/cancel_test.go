package ctrl

import (
	"strings"
	"testing"

	"app/internal/pkg/agent/ent"
)

func TestIsCancel(t *testing.T) {
	for text, want := range map[string]bool{
		"нет, стой, не делай": true,
		"Стоп":                true,
		"отмени":              true,
		"не надо удалять":     true,
		"подожди секунду":     true,
		"stop":                true,
		"Нос, не надо":        true,
		"носки постирай":      false,
		"глянь диск":          false,
		"остановись и объясни как работает cron, когда он срабатывает и где лог": false,
		"остановил сервис nginx?": false,
		"нет такого файла что-то": true, // accepted: the agent sees it is not a cancel and goes on
		"подожди": true,
	} {
		if got := isCancel(text); got != want {
			t.Errorf("isCancel(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestCancelNote(t *testing.T) {
	if cancelNote(nil) != "" {
		t.Error("nothing to take back should give no note")
	}
	if n := cancelNote([]string{"удали логи"}); !strings.Contains(n, "«удали логи»") {
		t.Errorf("note: %q", n)
	}
}

func TestKeyboardHasCancelNotStatus(t *testing.T) {
	c := &AgentController{}
	kb := c.keyboard(nil)
	var texts []string
	for _, row := range kb.Keyboard {
		for _, b := range row {
			texts = append(texts, b.Text)
		}
	}
	got := strings.Join(texts, "|")
	if !strings.Contains(got, btnCancel) || strings.Contains(got, btnStatus) {
		t.Errorf("keyboard = %s", got)
	}
	if cmd, ok := c.buttonCommand(btnCancel); !ok || cmd != "/cancel" {
		t.Errorf("cancel button maps to %q, %v", cmd, ok)
	}
	if cmd, ok := c.buttonCommand(btnStatus); !ok || cmd != "/status" {
		t.Error("old status button must still work")
	}
}

func TestQuickButtonLabels(t *testing.T) {
	q := ent.QuickCommand{Command: "todo", Icon: "📝", Description: "Дела"}
	if got := quickLabel(q); got != "📝 Дела" {
		t.Errorf("label = %q", got)
	}
	if got := quickLabel(ent.QuickCommand{Description: "Диск"}); got != "⚡ Диск" {
		t.Errorf("default icon: %q", got)
	}
}
