package ctrl

import (
	"fmt"
	"regexp"
	"strings"
)

// Code words that mean "drop everything, this first".
var urgentRe = regexp.MustCompile(`(?i)(^|[^\p{L}])(срочн\p{L}*|срочняк\p{L}*|критичн\p{L}*|третичн\p{L}*|немедленн\p{L}*|незамедлительн\p{L}*|аврал|горит|прямо\s+сейчас|сию\s+минуту|asap|urgent\p{L}*|critical|emergency)([^\p{L}]|$)`)

func isUrgent(text string) bool { return urgentRe.MatchString(text) }

// urgentNote tells the agent that this message goes first and what it
// pushed aside. Empty asked: nothing was running, the note is just the flag.
func urgentNote(asked []string) string {
	var b strings.Builder
	b.WriteString("(СРОЧНО: это сообщение важнее всего остального, займись им первым и сразу.")
	if len(asked) > 0 {
		b.WriteString(" Ради него прервано:")
		for _, a := range asked {
			fmt.Fprintf(&b, " «%s»;", truncateRunes(a, 300))
		}
		b.WriteString(" Когда закончишь срочное, коротко скажи, что прерванное не доделано, и продолжи его, если оно ещё нужно.")
	}
	b.WriteString(")\n")
	return b.String()
}
