package claude

import (
	"encoding/json"
	"testing"
)

func TestLimitsFromEvent(t *testing.T) {
	// The shape Claude Code 2.1.312 sends on a Pro subscription.
	ls := limitsFromEvent(json.RawMessage(`{"status":"allowed_warning","resetsAt":1793782800,"rateLimitType":"seven_day","utilization":0.85,
		"unifiedWindows":{"five_hour":{"utilization":0.4,"resetsAt":1793470200,"status":"allowed"},
		"seven_day":{"utilization":0.85,"resetsAt":1793782800,"status":"allowed_warning"}}}`))
	if len(ls) != 2 || ls[0].Window != "five_hour" || ls[1].Window != "seven_day" {
		t.Fatalf("got %+v", ls)
	}
	if ls[1].Utilization < 84.9 || ls[1].Utilization > 85.1 || ls[1].Status != "allowed_warning" || ls[1].ResetsAt.Unix() != 1793782800 {
		t.Fatalf("seven_day: %+v", ls[1])
	}

	// Older shape: one window, no unifiedWindows.
	ls = limitsFromEvent(json.RawMessage(`{"status":"allowed","resetsAt":1793470200,"rateLimitType":"five_hour","utilization":0.4}`))
	if len(ls) != 1 || ls[0].Window != "five_hour" || ls[0].Utilization < 39.9 {
		t.Fatalf("got %+v", ls)
	}
	if ls := limitsFromEvent(json.RawMessage(`{"status":"allowed"}`)); len(ls) != 0 {
		t.Fatal("event without a window must be skipped")
	}
}

func TestSortLimits(t *testing.T) {
	ls := []Limit{{Window: "zzz"}, {Window: "seven_day"}, {Window: "five_hour"}}
	SortLimits(ls)
	if ls[0].Window != "five_hour" || ls[1].Window != "seven_day" || ls[2].Window != "zzz" {
		t.Fatalf("got %v", ls)
	}
}
