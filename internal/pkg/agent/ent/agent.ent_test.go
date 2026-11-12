package ent

import (
	"testing"
	"time"
)

func TestParseEvery(t *testing.T) {
	for in, want := range map[string]time.Duration{"30m": 30 * time.Minute, "2h": 2 * time.Hour, "1d": 24 * time.Hour, "1w": 7 * 24 * time.Hour} {
		if got, ok := ParseEvery(in); !ok || got != want {
			t.Errorf("ParseEvery(%q) = %v, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "d", "0d", "-1d", "5x", "abc"} {
		if _, ok := ParseEvery(in); ok {
			t.Errorf("ParseEvery(%q) should fail", in)
		}
	}
}

func TestReminderNext(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	once := Reminder{At: now.Add(-time.Minute)}
	if _, ok := once.Next(now); ok {
		t.Error("a one-off reminder has no next run")
	}
	daily := Reminder{At: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), Every: "1d"}
	next, ok := daily.Next(now)
	if !ok || !next.Equal(time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("next = %v, %v; want one run after now, not a catch-up", next, ok)
	}
}
