package ctrl

import (
	"testing"
	"time"

	"app/internal/pkg/agent/ent"
)

func TestBatcherGluesAndDrops(t *testing.T) {
	got := make(chan []ent.BatchItem, 4)
	b := newBatcher(40*time.Millisecond, func(it []ent.BatchItem) { got <- it })
	k := batchKey{user: 1}

	b.add(k, ent.BatchItem{Text: "a"})
	time.Sleep(20 * time.Millisecond)
	b.add(k, ent.BatchItem{Text: "b"})
	if !b.has(k) {
		t.Fatal("batch should be open")
	}
	select {
	case it := <-got:
		if len(it) != 2 || it[0].Text != "a" || it[1].Text != "b" {
			t.Fatalf("got %+v", it)
		}
	case <-time.After(time.Second):
		t.Fatal("batch never flushed")
	}

	b.add(k, ent.BatchItem{Text: "c"})
	b.drop(k)
	select {
	case it := <-got:
		t.Fatalf("dropped batch flushed: %+v", it)
	case <-time.After(120 * time.Millisecond):
	}
}
