package svc

import (
	"strings"
	"testing"
	"time"

	"app/internal/core/claude"
)

func TestLimitWarnings(t *testing.T) {
	seen := map[string]bool{}
	mark := func(k string) bool {
		if seen[k] {
			return false
		}
		seen[k] = true
		return true
	}
	week := time.Now().Add(48 * time.Hour)
	five := time.Now().Add(2 * time.Hour)

	// 85% of the week (today's real number), 40% of the session: one warning.
	ls := []claude.Limit{{Window: "five_hour", Utilization: 40, ResetsAt: five}, {Window: "seven_day", Utilization: 85, ResetsAt: week}}
	got := limitWarnings(ls, mark)
	if len(got) != 1 || !strings.Contains(got[0], "«неделя» уже 85%") || !strings.Contains(got[0], "пизда") {
		t.Fatalf("first run: %q", got)
	}
	// The next run in the same window says nothing again…
	if got := limitWarnings(ls, mark); len(got) != 0 {
		t.Fatalf("repeated: %q", got)
	}
	// …until the next level.
	ls[1].Utilization = 96
	if got := limitWarnings(ls, mark); len(got) != 1 || !strings.HasPrefix(got[0], "🚨") {
		t.Fatalf("95%%: %q", got)
	}
	// Hitting the limit is said once.
	ls[0] = claude.Limit{Window: "five_hour", Utilization: 100, ResetsAt: five, Status: "rejected"}
	if got := limitWarnings(ls, mark); len(got) != 1 || !strings.Contains(got[0], "кончился") {
		t.Fatalf("rejected: %q", got)
	}
	// A new week (another reset time) warns afresh.
	ls = []claude.Limit{{Window: "seven_day", Utilization: 81, ResetsAt: week.Add(7 * 24 * time.Hour)}}
	if got := limitWarnings(ls, mark); len(got) != 1 {
		t.Fatalf("new window: %q", got)
	}
}
