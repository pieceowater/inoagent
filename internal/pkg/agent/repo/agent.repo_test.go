package repo

import (
	"testing"

	"app/internal/pkg/agent/ent"
)

func TestHistoryAndRestore(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := ent.TopicKey{Chat: 1, Thread: 2}
	_ = r.SetSession(key, "a", "")
	_ = r.NoteSession(key, 5000, "первая задача")
	_ = r.DropSession(key)
	_ = r.SetSession(key, "b", "")
	_ = r.DropSession(key)

	h := r.History(key)
	if len(h) != 2 || h[0].ID != "b" || h[1].ID != "a" || h[1].Title != "первая задача" {
		t.Fatalf("history: %+v", h)
	}
	s, err := r.Restore(key, 1)
	if err != nil || s.ID != "a" {
		t.Fatalf("restore: %+v %v", s, err)
	}
	if cur, ok := r.Session(key); !ok || cur.ID != "a" {
		t.Fatalf("current: %+v", cur)
	}
	if _, err := r.Restore(key, 9); err == nil {
		t.Fatal("restore out of range must fail")
	}
}
