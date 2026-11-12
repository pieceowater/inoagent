package ent

import (
	"fmt"
	"strings"
	"time"

	"app/internal/core/telegram"
)

// TopicKey names one conversation: a forum topic of a chat (thread 0 is the
// General topic, or a chat without topics).
type TopicKey struct {
	Chat   int64
	Thread int
}

func (k TopicKey) String() string { return fmt.Sprintf("%d:%d", k.Chat, k.Thread) }

func KeyOf(m *telegram.Message) TopicKey { return TopicKey{Chat: m.Chat.ID, Thread: m.Thread()} }

// Session is the Claude Code session a topic continues with --resume.
type Session struct {
	ID      string    `json:"id"`
	Updated time.Time `json:"updated"`
	// Hash of the topic's context file the session was last given, so an
	// edited file is handed over again (see svc/topic.svc.go).
	Context string `json:"context,omitempty"`
	// Tokens is the size of the conversation after its last request.
	Tokens int `json:"tokens,omitempty"`
	// Title is the first request, to tell sessions apart in /resume.
	Title string `json:"title,omitempty"`
}

// Job is one message waiting for the agent in its topic's queue.
type Job struct {
	Msg  *telegram.Message
	Text string // what the agent is asked, with the bot's own @mention removed
	// Kind is empty for a request; JobCompact and JobArchive are upkeep of the
	// topic's conversation.
	Kind string
	// Batch is the messages the request was made of, in order: a person's
	// consecutive messages (and photos of one album) count as one request.
	// Text is then their texts joined. The agent checks before starting that
	// they are still there, so deleting a message in time takes it back.
	Batch []BatchItem
	// Extra are the other messages of a settled batch: only their attachments
	// matter, the reply goes to Msg.
	Extra []*telegram.Message
	// Done, if set, gets the final answer text (buffered, one value) when the
	// job ends, however it ends.
	Done chan<- string
}

// BatchItem is one message of a batch with the text it contributes.
type BatchItem struct {
	Msg  *telegram.Message
	Text string
}

const (
	JobCompact = "compact"
	JobArchive = "archive"
)

// QuickCommand is a shortcut people asked the agent to make: /command (or its
// button in /menu) sends Prompt as if someone had typed it.
type QuickCommand struct {
	Command     string `json:"command"`
	Icon        string `json:"icon,omitempty"` // emoji on the button, ⚡ if empty
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
}

// TopicStatus is what /status reports about a topic.
type TopicStatus struct {
	Busy   bool
	Queued int
}

// Progress is what the running request has done so far, for "how is it going".
type Progress struct {
	Busy    bool
	Task    string
	Elapsed time.Duration
	Steps   int
	Tool    string // last tool call, e.g. "Bash"
	Summary string // ...and its one-line summary
	Said    string // last thing the agent wrote on the way
	Queued  int
}

// What a scheduled item does when it is due.
const (
	ModeRemind = "remind" // "⏰ @who text"
	ModeSay    = "say"    // the text, as is: a delayed message
	ModeRun    = "run"    // the text goes to the agent as a request
)

// Reminder is one scheduled item: a file in <state>/reminders.
type Reminder struct {
	ID       string    `json:"id"`
	Chat     int64     `json:"chat"`
	Thread   int       `json:"thread"`
	UserID   int64     `json:"user_id"`
	Username string    `json:"username"`
	Name     string    `json:"name"`
	At       time.Time `json:"at"`
	Text     string    `json:"text"`
	Mode     string    `json:"mode"`
	Every    string    `json:"every,omitempty"` // "1d", "7d", "2h"; empty = once
}

// Next is the time of the following run for a repeating item, the first one
// after now (a bot that was off for a week does not catch up on seven runs).
func (r Reminder) Next(now time.Time) (time.Time, bool) {
	step, ok := ParseEvery(r.Every)
	if !ok {
		return time.Time{}, false
	}
	next := r.At
	for !next.After(now) {
		next = next.Add(step)
	}
	return next, true
}

// ParseEvery turns "30m", "2h", "1d", "1w" into a duration.
func ParseEvery(s string) (time.Duration, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if len(s) < 2 {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(s[:len(s)-1], "%d", &n); err != nil || n <= 0 {
		return 0, false
	}
	unit := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[s[len(s)-1]]
	if unit == 0 {
		return 0, false
	}
	return time.Duration(n) * unit, true
}
