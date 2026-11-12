package svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"app/internal/core/claude"
	"app/internal/core/telegram"
)

// Limits returns the subscription windows: live, from a tiny probe request,
// or — if that fails — what the last run reported. stale tells
// which one it is; err is the live lookup's failure, if any.
func (s *AgentService) Limits(ctx context.Context) (limits []claude.Limit, stale bool, err error) {
	limits, err = s.runner.Probe(ctx)
	if err == nil {
		if serr := s.repo.SetLimits(limits); serr != nil {
			s.log.Error("save limits", "err", serr)
		}
		return limits, false, nil
	}
	s.log.Warn("limits probe failed", "err", err)
	return s.repo.Limits(), true, err
}

// Thresholds at which the bot warns that a subscription window is running
// out, once per window and level.
var warnLevels = []float64{80, 95}

// warnLimits posts into the topic when a run pushed a window past a
// threshold, or the limit is already hit.
func (s *AgentService) warnLimits(ctx context.Context, m *telegram.Message, limits []claude.Limit) {
	for _, text := range limitWarnings(limits, s.repo.MarkWarned) {
		s.msg.Reply(ctx, m, text)
	}
}

// limitWarnings is what to say about these windows; markWarned records a
// warning and reports whether it is new, so each is said once per window.
func limitWarnings(limits []claude.Limit, markWarned func(key string) bool) []string {
	var out []string
	for _, l := range limits {
		window := "«" + claude.WindowName(l.Window) + "»"
		reset := ""
		if !l.ResetsAt.IsZero() {
			reset = ", сброс " + claude.ResetIn(l.ResetsAt)
		}
		key := fmt.Sprintf("%s|%d|", l.Window, l.ResetsAt.Unix())
		if l.Status == "rejected" {
			if markWarned(key + "rejected") {
				out = append(out, fmt.Sprintf("🛑 Всё, лимит %s кончился%s. До сброса я овощ.", window, reset))
			}
			continue
		}
		level := 0.0
		for _, lv := range warnLevels {
			if l.Utilization >= lv {
				level = lv
			}
		}
		if level == 0 || !markWarned(fmt.Sprintf("%s%.0f", key, level)) {
			continue
		}
		text := fmt.Sprintf("⚠️ Лимит %s уже %.0f%%%s. Скоро мне пизда — экономьте запросы.", window, l.Utilization, reset)
		if level >= 95 {
			text = fmt.Sprintf("🚨 Лимит %s %.0f%%%s. Ещё пара запросов — и я отключусь до сброса.", window, l.Utilization, reset)
		}
		out = append(out, text)
	}
	return out
}

// ForgetOtherAccounts drops limits and warnings remembered for a different
// Claude account: after a new token they'd describe someone else's quota.
func (s *AgentService) ForgetOtherAccounts() {
	token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
	if token == "" {
		token = os.Getenv("ANTHROPIC_API_KEY")
	}
	sum := sha256.Sum256([]byte(token))
	if err := s.repo.UseAccount(hex.EncodeToString(sum[:8])); err != nil {
		s.log.Error("reset limits for a new account", "err", err)
	}
}
