package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A fake CLI: answers once its input has been quiet, with everything it read.
func TestFeedJoinsRun(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake")
	script := `#!/bin/sh
all=""
while IFS= read -r line; do
  all="$all$line;"
  if echo "$line" | grep -q second; then
    printf '{"type":"result","result":"got %s","num_turns":1}\n' "$(echo "$all" | grep -o 'first\|second' | tr '\n' ' ')"
  fi
done
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Bin: bin, Workdir: dir}
	feed := &Feed{}
	go func() {
		for i := 0; i < 50; i++ {
			if feed.Send("second") {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := r.Run(ctx, "first", "", nil, nil, feed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "first") || !strings.Contains(res.Text, "second") {
		t.Fatalf("text %q", res.Text)
	}
	if feed.Send("late") {
		t.Fatal("send after the run must fail")
	}
}
