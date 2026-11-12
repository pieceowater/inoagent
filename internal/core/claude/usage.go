package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Limit is one of the subscription's rolling windows: the 5-hour session, the
// week, the week for a specific model.
type Limit struct {
	Window      string    // "five_hour", "seven_day", "seven_day_opus", …
	Utilization float64   // percent, 0–100
	ResetsAt    time.Time // zero if unknown
	Status      string    // from run events: "allowed", "allowed_warning", "rejected"
}

// UsageURL is what Claude Code's own /usage screen reads for subscription accounts.
const UsageURL = "https://api.anthropic.com/api/oauth/usage"

// FetchUsage asks Anthropic how much of each subscription window is used.
// Only works with a subscription OAuth token, not an API key.
func FetchUsage(ctx context.Context, oauthToken string) ([]Limit, error) {
	if oauthToken == "" {
		return nil, fmt.Errorf("нет CLAUDE_CODE_OAUTH_TOKEN")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, UsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+oauthToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("запрос лимитов: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("запрос лимитов: HTTP %d", resp.StatusCode)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("запрос лимитов: %w", err)
	}
	var limits []Limit
	for window, raw := range body {
		var w struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    string   `json:"resets_at"`
		}
		if json.Unmarshal(raw, &w) != nil || w.Utilization == nil {
			continue // null windows (not on this plan) and non-window fields
		}
		l := Limit{Window: window, Utilization: *w.Utilization}
		l.ResetsAt, _ = time.Parse(time.RFC3339, w.ResetsAt)
		limits = append(limits, l)
	}
	SortLimits(limits)
	return limits, nil
}

// limitsFromEvent reads a stream-json "rate_limit_event", which the CLI
// emits during runs on a subscription. Its rate_limit_info describes the
// window that is closest to its limit and, under unifiedWindows, every
// window; field names have moved between CLI versions, so it takes whatever
// it recognises.
func limitsFromEvent(raw json.RawMessage) []Limit {
	var info map[string]any
	if json.Unmarshal(raw, &info) != nil {
		return nil
	}
	if windows, ok := info["unifiedWindows"].(map[string]any); ok && len(windows) > 0 {
		var ls []Limit
		for name, w := range windows {
			if wm, ok := w.(map[string]any); ok {
				ls = append(ls, limitFromMap(name, wm))
			}
		}
		SortLimits(ls)
		return ls
	}
	name, _ := info["rateLimitType"].(string)
	if name == "" {
		name, _ = info["rate_limit_type"].(string)
	}
	if name == "" {
		return nil
	}
	return []Limit{limitFromMap(name, info)}
}

func limitFromMap(window string, m map[string]any) Limit {
	l := Limit{Window: window}
	l.Status, _ = m["status"].(string)
	for _, k := range []string{"utilization", "usedPercentage"} {
		if f, ok := m[k].(float64); ok {
			if f <= 1 {
				f *= 100 // a fraction, not a percent
			}
			l.Utilization = f
		}
	}
	switch v := m["resetsAt"].(type) {
	case float64:
		l.ResetsAt = time.Unix(int64(v), 0)
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			l.ResetsAt = t
		} else if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			l.ResetsAt = time.Unix(n, 0)
		}
	}
	return l
}

var windowOrder = map[string]int{"five_hour": 0, "seven_day": 1, "seven_day_sonnet": 2, "seven_day_opus": 3}

// SortLimits puts the 5-hour window first, then the week, then the rest.
func SortLimits(ls []Limit) {
	sort.Slice(ls, func(i, j int) bool {
		oi, iok := windowOrder[ls[i].Window]
		oj, jok := windowOrder[ls[j].Window]
		switch {
		case iok && jok:
			return oi < oj
		case iok != jok:
			return iok
		}
		return ls[i].Window < ls[j].Window
	})
}

// WindowName is the window as people say it.
func WindowName(w string) string {
	switch w {
	case "five_hour":
		return "5 часов"
	case "seven_day":
		return "неделя"
	case "seven_day_opus":
		return "неделя, Opus"
	case "seven_day_sonnet":
		return "неделя, Sonnet"
	}
	return strings.ReplaceAll(w, "_", " ")
}

// ResetIn says when a window resets, as people say it: "через 2ч 10м",
// or a weekday and time when it's more than a day away.
func ResetIn(t time.Time) string {
	d := time.Until(t)
	switch {
	case d <= 0:
		return "уже был"
	case d < time.Hour:
		return fmt.Sprintf("через %d мин", int(d.Minutes())+1)
	case d < 24*time.Hour:
		return fmt.Sprintf("через %dч %dм", int(d.Hours()), int(d.Minutes())%60)
	}
	days := [...]string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}
	lt := t.Local()
	return fmt.Sprintf("%s %s", days[lt.Weekday()], lt.Format("02.01 15:04"))
}
