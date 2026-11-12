// Package claude runs one Claude Code turn: `claude -p` with the prompt on
// stdin, --output-format stream-json on stdout, and --resume to continue a
// topic's conversation. Permission decisions don't happen here — the
// PreToolUse hook in managed-settings.json sends every tool call to the bridge.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// stallTimeout is how long a run may stay completely silent before it counts
// as hung. A tool or an API call that never returns would otherwise burn the
// whole run timeout. Longer than an approval wait, which is silent too.
const stallTimeout = 15 * time.Minute

type Runner struct {
	Bin     string // path to the claude CLI
	Workdir string // cwd of the run: the agent's workspace, where CLAUDE.md lives
	Model   string // empty: the CLI's own default; SetModel overrides it
	modelMu sync.RWMutex
	model   string
	Env     []string // full environment of the child process
	// Built fresh for every run and appended to Claude Code's system prompt
	// (the agent's rules and manner of speaking); nil for none.
	SystemPrompt func() string
}

// Event is something worth showing in the "working…" message while a run is
// in flight: a tool being used.
// Event is one step of a run: a tool call (Tool, Summary) or something the
// model said on the way (Text).
// SetModel switches the model for the following runs; empty goes back to Model.
func (r *Runner) SetModel(m string) {
	r.modelMu.Lock()
	r.model = m
	r.modelMu.Unlock()
}

// CurrentModel is the model runs use now; empty means the CLI's default.
func (r *Runner) CurrentModel() string {
	r.modelMu.RLock()
	defer r.modelMu.RUnlock()
	if r.model != "" {
		return r.model
	}
	return r.Model
}

type Event struct {
	Tool    string
	Summary string
	Text    string
	// Delta is a fragment of the answer as the model writes it; NewTurn marks
	// the start of a new assistant message (after tool calls).
	Delta   string
	NewTurn bool
}

type Result struct {
	SessionID string
	Text      string
	IsError   bool
	CostUSD   float64
	Turns     int
	Duration  time.Duration
	// Subscription windows the CLI reported during the run, if any.
	Limits []Limit
	// ContextTokens is how much the conversation holds after the run: the
	// prompt size of its last model call (0 if unknown).
	ContextTokens int
}

// ErrNoResult means the CLI exited without its final "result" line — a crash,
// a bad --resume id, a missing login. Stderr's tail is attached.
var ErrNoResult = errors.New("claude: no result")

// ErrStalled means the CLI printed nothing for stallTimeout and was killed.
var ErrStalled = errors.New("claude: stalled")

type streamLine struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Result    string          `json:"result"`
	IsError   bool            `json:"is_error"`
	CostUSD   float64         `json:"total_cost_usd"`
	NumTurns  int             `json:"num_turns"`
	RateLimit json.RawMessage `json:"rate_limit_info"`
	Event     *struct {
		Type  string `json:"type"`
		Delta *struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	Message         *struct {
		Usage *struct {
			Input         int `json:"input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

// Feed lets the caller hand the running claude more messages: the CLI folds
// them into the turn in progress, between tool calls, instead of starting a
// new request. Once the run has its answer the feed is closed and Send says so.
type Feed struct {
	mu     sync.Mutex
	in     io.WriteCloser
	closed bool
}

// Send adds a message to the run; false means the run is over (or never took
// messages) and the text was not delivered.
func (f *Feed) Send(text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.in == nil {
		return false
	}
	if err := writeUser(f.in, text); err != nil {
		f.closed = true
		return false
	}
	return true
}

func (f *Feed) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed && f.in != nil {
		_ = f.in.Close()
	}
	f.closed = true
}

func writeUser(w io.Writer, text string) error {
	line, err := json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": text},
	})
	if err != nil {
		return err
	}
	_, err = w.Write(append(line, '\n'))
	return err
}

// Run starts claude with the prompt. With a feed, more messages can join the
// run until it answers; nil means the prompt is all there is.
func (r *Runner) Run(ctx context.Context, prompt, sessionID string, extraEnv []string, onEvent func(Event), feed *Feed) (Result, error) {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages"}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	if m := r.CurrentModel(); m != "" {
		args = append(args, "--model", m)
	}
	if r.SystemPrompt != nil {
		if p := r.SystemPrompt(); p != "" {
			args = append(args, "--append-system-prompt", p)
		}
	}
	parent := ctx
	ctx, cancelRun := context.WithCancelCause(parent)
	defer cancelRun(nil)
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Dir = r.Workdir
	cmd.Env = append(append([]string{}, r.Env...), extraEnv...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{}, err
	}
	// Own process group, so a /stop or a timeout also kills the shells and
	// commands Claude started, not just the CLI itself.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second

	stderr := &tailBuffer{max: 4096}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("claude: start: %w", err)
	}
	if err := writeUser(stdin, prompt); err != nil {
		cancelRun(err)
	}
	if feed == nil {
		_ = stdin.Close()
	} else {
		feed.mu.Lock()
		feed.in, feed.closed = stdin, false
		feed.mu.Unlock()
		defer feed.close()
	}

	var lastOutput atomic.Int64
	lastOutput.Store(time.Now().UnixNano())
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if time.Since(time.Unix(0, lastOutput.Load())) > stallTimeout {
					cancelRun(ErrStalled)
					return
				}
			}
		}
	}()

	var res Result
	var gotResult bool
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		lastOutput.Store(time.Now().UnixNano())
		var l streamLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if l.SessionID != "" {
			res.SessionID = l.SessionID
		}
		switch l.Type {
		case "stream_event":
			if l.Event == nil || onEvent == nil {
				continue
			}
			switch {
			case l.Event.Type == "message_start":
				onEvent(Event{NewTurn: true})
			case l.Event.Type == "content_block_delta" && l.Event.Delta != nil && l.Event.Delta.Type == "text_delta":
				onEvent(Event{Delta: l.Event.Delta.Text})
			}
		case "assistant":
			if l.Message != nil && l.Message.Usage != nil && l.ParentToolUseID == nil {
				u := l.Message.Usage
				res.ContextTokens = u.Input + u.CacheCreation + u.CacheRead
			}
			if l.Message == nil || onEvent == nil {
				continue
			}
			for _, c := range l.Message.Content {
				switch {
				case c.Type == "tool_use":
					onEvent(Event{Tool: c.Name, Summary: summarize(c.Name, c.Input)})
				case c.Type == "text" && strings.TrimSpace(c.Text) != "":
					onEvent(Event{Text: strings.TrimSpace(c.Text)})
				}
			}
		case "rate_limit_event":
			res.Limits = append(res.Limits, limitsFromEvent(l.RateLimit)...)
		case "result":
			gotResult = true
			if feed != nil {
				feed.close() // the answer is in; anything that came in time was folded into it
			}
			if l.Result != "" || res.Text == "" {
				res.Text = l.Result
			}
			res.IsError = l.IsError
			res.CostUSD = l.CostUSD
			res.Turns = l.NumTurns
		}
	}
	waitErr := cmd.Wait()
	res.Duration = time.Since(start)

	if parent.Err() != nil {
		return res, parent.Err()
	}
	if errors.Is(context.Cause(ctx), ErrStalled) {
		return res, ErrStalled
	}
	if !gotResult {
		msg := strings.TrimSpace(stderr.String())
		if waitErr != nil {
			msg = waitErr.Error() + ": " + msg
		}
		return res, fmt.Errorf("%w: %s", ErrNoResult, msg)
	}
	return res, nil
}

// summarize is the one line shown per tool call, e.g. "docker ps" for Bash.
func summarize(tool string, input json.RawMessage) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	pick := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := in[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	s := pick("command", "file_path", "notebook_path", "pattern", "url", "query", "description", "path")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120]) + "…"
	}
	return s
}

// tailBuffer keeps only the last max bytes written to it.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

// Probe makes the smallest possible request — one word to the cheapest
// model, no tools, no project context — just to read the subscription
// windows the CLI reports with it. The usage endpoint doesn't serve tokens
// made by `claude setup-token`, so this is how /limits gets live numbers.
func (r *Runner) Probe(ctx context.Context) ([]Limit, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.Bin, "-p", "--output-format", "stream-json", "--verbose", "--model", "haiku")
	cmd.Dir = os.TempDir() // no CLAUDE.md to load
	cmd.Env = r.Env
	cmd.Stdin = strings.NewReader("ok")
	out, err := cmd.Output()
	var limits []Limit
	for _, line := range strings.Split(string(out), "\n") {
		var l streamLine
		if json.Unmarshal([]byte(line), &l) == nil && l.Type == "rate_limit_event" {
			limits = append(limits, limitsFromEvent(l.RateLimit)...)
		}
	}
	if len(limits) == 0 {
		if err == nil {
			err = errors.New("claude не сообщил лимиты")
		}
		return nil, err
	}
	SortLimits(limits)
	return limits, nil
}
