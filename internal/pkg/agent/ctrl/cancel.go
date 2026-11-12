package ctrl

import (
	"fmt"
	"regexp"
	"strings"
)

// A change of mind sent right after a request: short, with a stop word.
var cancelRe = regexp.MustCompile(`(?i)(^|[^\p{L}])(стой|стоп|стопе|отмен\p{L}*|отбой|отставить|погоди|подожди|забей|не\s+(делай|надо|нужно|ставь|запускай|удаляй|трогай|гони)|нет|нос|stop|cancel|abort|wait|don'?t)([^\p{L}]|$)`)

const maxCancelWords = 10

func isCancel(text string) bool {
	return len(strings.Fields(text)) <= maxCancelWords && cancelRe.MatchString(text)
}

// cancelNote tells the agent what the message took back. Empty if nothing
// was running or waiting, then the message is just a message.
func cancelNote(asked []string) string {
	if len(asked) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("(следующее сообщение отменяет то, что ты не закончил. Прерванные запросы:")
	for _, a := range asked {
		fmt.Fprintf(&b, " «%s»;", truncateRunes(a, 300))
	}
	b.WriteString(" Остановись, коротко скажи, что успел сделать, и предложи откат, если что-то уже изменилось. Если на самом деле это не отмена, а уточнение — продолжи задачу с его учётом.)\n")
	return b.String()
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
