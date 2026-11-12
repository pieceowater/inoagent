package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"app/internal/core/claude"
	"app/internal/pkg/agent/ent"
)

// AgentRepository is defined as an interface (rather than the struct below
// being used directly) so the svc layer can be unit-tested against a fake
// without touching the disk.
type AgentRepository interface {
	Session(key ent.TopicKey) (ent.Session, bool)
	SetSession(key ent.TopicKey, id, contextHash string) error
	TopicName(key ent.TopicKey) string
	SetTopicName(key ent.TopicKey, name string) error
	ThreadByName(chat int64, name string) int
	DropSession(key ent.TopicKey) error
	// NoteSession records the size of the topic's conversation and, if it has
	// none yet, its title.
	NoteSession(key ent.TopicKey, tokens int, title string) error
	// History is the topic's earlier sessions, newest first; Restore makes
	// one of them the current session again.
	History(key ent.TopicKey) []ent.Session
	Restore(key ent.TopicKey, i int) (ent.Session, error)
	Model() string
	SetModel(model string) error
	UsageToday() int
	CountRun() error
	Offset() int64
	SetOffset(offset int64) error
	Limits() []claude.Limit
	SetLimits(ls []claude.Limit) error
	// MarkWarned records a limit warning; false if it was already given.
	MarkWarned(key string) bool
	// UseAccount forgets the stored limits and warnings when the Claude
	// account (a fingerprint of its token) is not the one they belong to.
	UseAccount(fingerprint string) error
}

// Repo keeps the bridge's own small memory in one JSON file on the state
// volume: which Claude session belongs to which topic, runs per day, and the
// Telegram update offset. The agent's real memory is its workspace, not this.
type Repo struct {
	mu   sync.Mutex
	path string
	d    data
}

type data struct {
	Sessions map[string]ent.Session `json:"sessions"`
	Usage    map[string]int         `json:"usage"`  // "2026-10-31" → runs
	Offset   int64                  `json:"offset"` // next Telegram update id
	// The subscription windows as last reported by a run, for when the
	// usage endpoint can't be reached.
	Limits map[string]claude.Limit `json:"limits"`
	// Limit warnings already posted, so each is said once per window.
	Warned map[string]time.Time `json:"warned"`
	// Forum topic names as last seen; Telegram doesn't send them with
	// every message.
	Topics map[string]string `json:"topics"`
	// Sessions a topic has left behind (reset, archived), newest first.
	History map[string][]ent.Session `json:"history,omitempty"`
	// Model chosen with /model; empty is the configured default.
	Model string `json:"model,omitempty"`
	// Which Claude account Limits and Warned are about.
	Account string `json:"account,omitempty"`
}

func New(dir string) (*Repo, error) {
	r := &Repo{path: filepath.Join(dir, "state.json")}
	b, err := os.ReadFile(r.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &r.d); err != nil {
			return nil, err
		}
	}
	if r.d.Sessions == nil {
		r.d.Sessions = map[string]ent.Session{}
	}
	if r.d.Usage == nil {
		r.d.Usage = map[string]int{}
	}
	if r.d.Limits == nil {
		r.d.Limits = map[string]claude.Limit{}
	}
	if r.d.Warned == nil {
		r.d.Warned = map[string]time.Time{}
	}
	if r.d.Topics == nil {
		r.d.Topics = map[string]string{}
	}
	return r, nil
}

// save writes atomically, so a crash mid-write never leaves a broken file.
// Callers hold r.mu.
func (r *Repo) save() error {
	b, err := json.MarshalIndent(r.d, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

func (r *Repo) Session(key ent.TopicKey) (ent.Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.d.Sessions[key.String()]
	return s, ok
}

func (r *Repo) SetSession(key ent.TopicKey, id, contextHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := ent.Session{ID: id, Updated: time.Now(), Context: contextHash}
	if old, ok := r.d.Sessions[key.String()]; ok && old.ID == id {
		next.Tokens, next.Title = old.Tokens, old.Title
	}
	r.d.Sessions[key.String()] = next
	return r.save()
}

func (r *Repo) DropSession(key ent.TopicKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key.String()
	if s, ok := r.d.Sessions[k]; ok && s.ID != "" {
		if r.d.History == nil {
			r.d.History = map[string][]ent.Session{}
		}
		h := append([]ent.Session{s}, r.d.History[k]...)
		r.d.History[k] = h[:min(len(h), historyKeep)]
	}
	delete(r.d.Sessions, k)
	return r.save()
}

// historyKeep is how many earlier sessions a topic remembers.
const historyKeep = 6

func (r *Repo) NoteSession(key ent.TopicKey, tokens int, title string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.d.Sessions[key.String()]
	if !ok {
		return nil
	}
	s.Tokens = tokens
	if s.Title == "" {
		s.Title = title
	}
	r.d.Sessions[key.String()] = s
	return r.save()
}

func (r *Repo) History(key ent.TopicKey) []ent.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ent.Session(nil), r.d.History[key.String()]...)
}

func (r *Repo) Restore(key ent.TopicKey, i int) (ent.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key.String()
	h := r.d.History[k]
	if i < 0 || i >= len(h) {
		return ent.Session{}, errors.New("нет такой сессии")
	}
	pick := h[i]
	rest := append(append([]ent.Session(nil), h[:i]...), h[i+1:]...)
	if cur, ok := r.d.Sessions[k]; ok && cur.ID != "" {
		rest = append([]ent.Session{cur}, rest...)
	}
	r.d.History[k] = rest[:min(len(rest), historyKeep)]
	pick.Updated = time.Now()
	r.d.Sessions[k] = pick
	return pick, r.save()
}

func (r *Repo) Model() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.d.Model
}

func (r *Repo) SetModel(model string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.d.Model = model
	return r.save()
}

func today() string { return time.Now().Format("2006-01-02") }

func (r *Repo) UsageToday() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.d.Usage[today()]
}

func (r *Repo) CountRun() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.d.Usage[today()]++
	// Keep a month of history, not forever.
	cutoff := time.Now().AddDate(0, 0, -31).Format("2006-01-02")
	for day := range r.d.Usage {
		if day < cutoff {
			delete(r.d.Usage, day)
		}
	}
	return r.save()
}

func (r *Repo) Offset() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.d.Offset
}

func (r *Repo) SetOffset(offset int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if offset == r.d.Offset {
		return nil
	}
	r.d.Offset = offset
	return r.save()
}

func (r *Repo) Limits() []claude.Limit {
	r.mu.Lock()
	defer r.mu.Unlock()
	ls := make([]claude.Limit, 0, len(r.d.Limits))
	for _, l := range r.d.Limits {
		ls = append(ls, l)
	}
	claude.SortLimits(ls)
	return ls
}

func (r *Repo) SetLimits(ls []claude.Limit) error {
	if len(ls) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range ls {
		r.d.Limits[l.Window] = l
	}
	return r.save()
}

func (r *Repo) MarkWarned(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, done := r.d.Warned[key]; done {
		return false
	}
	r.d.Warned[key] = time.Now()
	for k, at := range r.d.Warned {
		if time.Since(at) > 14*24*time.Hour {
			delete(r.d.Warned, k)
		}
	}
	_ = r.save()
	return true
}

func (r *Repo) TopicName(key ent.TopicKey) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.d.Topics[key.String()]
}

// ThreadByName is the thread of the chat's topic with that name (0 if none is known).
func (r *Repo) ThreadByName(chat int64, name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range r.d.Topics {
		var c int64
		var t int
		if v == name {
			if _, err := fmt.Sscanf(k, "%d:%d", &c, &t); err == nil && c == chat {
				return t
			}
		}
	}
	return 0
}

func (r *Repo) SetTopicName(key ent.TopicKey, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.d.Topics[key.String()] == name {
		return nil
	}
	r.d.Topics[key.String()] = name
	return r.save()
}

func (r *Repo) UseAccount(fingerprint string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.d.Account == fingerprint {
		return nil
	}
	r.d.Account = fingerprint
	r.d.Limits = map[string]claude.Limit{}
	r.d.Warned = map[string]time.Time{}
	return r.save()
}
