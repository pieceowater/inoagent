package ctrl

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"app/internal/pkg/agent/ent"
)

// "че как?" while the agent is busy: answered right away from what the run
// has done so far, without interrupting it or waiting for the queue.
var statusRe = regexp.MustCompile(`(?i)(^|[^\p{L}])(че|чё|что)\s+(как|там|по|с)|как\s+(дела|там|оно|успехи|продвиж\p{L}*|прогресс|идёт|идет|поживаешь)|ну\s+(что|как)|(^|[^\p{L}-])(статус|прогресс|скоро|готово)([^\p{L}-]|$)|долго\s+ещ[её]|ещ[её]\s+долго|how('s|\s+is)?\s+it\s+going|status|progress|^\s*(ну|и|эй|алло|\?+)\s*\?*\s*$`)

const maxStatusWords = 7

func isStatusQuestion(text string) bool {
	n := len(strings.Fields(text))
	return n > 0 && n <= maxStatusWords && statusRe.MatchString(text)
}

func progressText(p ent.Progress) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Работаю над «%s» уже %s", truncateRunes(oneLine(p.Task), 80), p.Elapsed.Round(time.Second))
	if p.Steps > 0 {
		fmt.Fprintf(&b, ", шагов: %d", p.Steps)
	}
	b.WriteString(".")
	if p.Tool != "" {
		last := p.Tool
		if p.Summary != "" {
			last += ": " + p.Summary
		}
		fmt.Fprintf(&b, "\nСейчас: `%s`", strings.ReplaceAll(truncateRunes(last, 120), "`", "'"))
	}
	if p.Said != "" {
		fmt.Fprintf(&b, "\nПоследнее, что отметил: %s", truncateRunes(oneLine(p.Said), 300))
	}
	if p.Queued > 0 {
		fmt.Fprintf(&b, "\nВ очереди ещё: %d.", p.Queued)
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
