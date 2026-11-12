package svc

import (
	"testing"
	"time"

	"app/internal/core/claude"
)

func TestLimitReset(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	if _, ok := limitReset([]claude.Limit{{Window: "five_hour", Status: "allowed"}}, now); ok {
		t.Fatal("allowed window is not a limit hit")
	}
	at, ok := limitReset([]claude.Limit{
		{Window: "five_hour", Status: "rejected", ResetsAt: now.Add(time.Hour)},
		{Window: "seven_day", Status: "rejected", ResetsAt: now.Add(3 * time.Hour)},
	}, now)
	if !ok || !at.Equal(now.Add(3*time.Hour)) {
		t.Fatalf("got %v %v", at, ok)
	}
	at, ok = limitReset([]claude.Limit{{Status: "rejected"}}, now)
	if !ok || !at.Equal(now.Add(limitUnknown-limitGrace)) {
		t.Fatalf("unknown reset: got %v %v", at, ok)
	}
}
