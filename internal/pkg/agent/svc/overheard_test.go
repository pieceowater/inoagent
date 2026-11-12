package svc

import (
	"strings"
	"testing"
	"time"
)

func TestFormatOverheard(t *testing.T) {
	now := time.Now()
	if got := formatOverheard(nil, now); got != "" {
		t.Fatalf("empty list should give nothing, got %q", got)
	}
	got := formatOverheard([]overheard{
		{at: now.Add(-48 * time.Hour), who: "Старый", text: "давно"},
		{at: now.Add(-time.Minute), who: "Юра", text: "Адель, ты где"},
	}, now)
	if strings.Contains(got, "давно") || !strings.Contains(got, "- Юра: Адель, ты где") {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestReactionEmoji(t *testing.T) {
	for in, want := range map[string]string{"🔥": "🔥", " 👍\n": "👍", "❤️": "❤", "🥀": "", "text": ""} {
		if got := reactionEmoji(in); got != want {
			t.Errorf("reactionEmoji(%q) = %q, want %q", in, got, want)
		}
	}
	if !isSilent(" NO_REPLY\n") || isSilent("NO_REPLY, но нет") {
		t.Error("isSilent")
	}
}
