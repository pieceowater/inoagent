package ctrl

import (
	"strings"
	"testing"
	"time"

	"app/internal/pkg/agent/ent"
)

func TestIsStatusQuestion(t *testing.T) {
	for text, want := range map[string]bool{
		"че как":     true,
		"Чё там?":    true,
		"как успехи": true,
		"ну что":     true,
		"долго еще?": true,
		"статус":     true,
		"ну?":        true,
		"глянь диск": false,
		"что там в логах nginx за вчера и позавчера": false,
		"как дела у реелхаба на сервере и вообще":    false,
		"сделай статус-страницу для сервера":         false,
	} {
		if got := isStatusQuestion(text); got != want {
			t.Errorf("isStatusQuestion(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestProgressText(t *testing.T) {
	got := progressText(ent.Progress{Busy: true, Task: "почисти\nдиск", Elapsed: 75 * time.Second, Steps: 4,
		Tool: "Bash", Summary: "docker system df", Said: "Смотрю, что занимает место", Queued: 1})
	for _, want := range []string{"«почисти диск»", "1m15s", "шагов: 4", "`Bash: docker system df`", "Смотрю, что занимает место", "В очереди ещё: 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
