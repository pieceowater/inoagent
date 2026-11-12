package ctrl

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"app/internal/core/telegram"
)

// In a group people talk to each other. A message that calls someone else —
// a reply to another person, an @tag, a name at the start — is not a request
// to the agent, unless it calls the agent too.

// othersFile lists extra names people go by in the chat (nicknames, how
// they are called aloud), one per line, so "Адель, глянь" is recognised even
// though the Telegram profile says something else.
const othersFile = "not-me-names.txt"

var mentionRe = regexp.MustCompile(`@([A-Za-z0-9_]{3,})`)

// greetings may precede the name: "эй, Адель".
var greetings = map[string]bool{
	"привет": true, "эй": true, "ей": true, "ну": true, "слышь": true,
	"хей": true, "йо": true, "ку": true, "так": true, "а": true, "и": true,
}

// roster remembers who has written in the chat, to recognise their names.
type roster struct {
	mu    sync.Mutex
	names map[int64][]string // user id → lowercase names
}

func (r *roster) see(u *telegram.User) {
	if u == nil {
		return
	}
	var names []string
	for _, n := range []string{u.FirstName, u.LastName, u.Username} {
		if n = strings.ToLower(strings.TrimSpace(n)); utf8.RuneCountInString(n) >= 3 {
			names = append(names, n)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names == nil {
		r.names = map[int64][]string{}
	}
	r.names[u.ID] = names
}

// namesExcept: every known name that is not the sender's own.
func (r *roster) namesExcept(id int64) map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]bool{}
	for uid, names := range r.names {
		if uid == id {
			continue
		}
		for _, n := range names {
			out[n] = true
		}
	}
	return out
}

func (c *AgentController) extraNames() map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(c.cfg.Workspace, othersFile))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		if n := strings.ToLower(strings.TrimSpace(line)); n != "" && !strings.HasPrefix(n, "#") {
			out[n] = true
		}
	}
	return out
}

// forSomeoneElse: the message is addressed to another person and not to us.
func (c *AgentController) forSomeoneElse(m *telegram.Message, text string) bool {
	if m.Chat.Type == "private" || m.From == nil {
		return false
	}
	if c.callsUs(m, text) {
		return false
	}
	// A plain message inside a topic "replies" to the topic's first message.
	if r := m.ReplyToMessage; r != nil && r.From != nil && r.From.ID != m.From.ID && r.MessageID != m.MessageThreadID {
		return true
	}
	for _, e := range m.Entities {
		if e.User != nil && e.User.ID != c.me.ID {
			return true
		}
	}
	for _, g := range mentionRe.FindAllStringSubmatch(text, -1) {
		if !strings.EqualFold(g[1], c.me.Username) {
			return true
		}
	}
	return c.startsWithName(m, text)
}

// callsUs: the agent is called explicitly, by reply, @tag or name.
func (c *AgentController) callsUs(m *telegram.Message, text string) bool {
	if r := m.ReplyToMessage; r != nil && r.From != nil && r.From.ID == c.me.ID {
		return true
	}
	low := strings.ToLower(text)
	if c.me.Username != "" && strings.Contains(low, "@"+strings.ToLower(c.me.Username)) {
		return true
	}
	for _, e := range m.Entities {
		if e.User != nil && e.User.ID == c.me.ID {
			return true
		}
	}
	return strings.Contains(low, "иноагент") || strings.Contains(low, "inoagent")
}

func (c *AgentController) startsWithName(m *telegram.Message, text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	if len(words) == 0 {
		return false
	}
	if greetings[words[0]] && len(words) > 1 {
		words = words[1:]
	}
	first := words[0]
	return c.people.namesExcept(m.From.ID)[first] || c.extraNames()[first]
}
