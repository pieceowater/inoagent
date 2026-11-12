package ctrl

import (
	"sync"
	"time"

	"app/internal/pkg/agent/ent"
)

// batchKey: one person's messages in one topic.
type batchKey struct {
	topic ent.TopicKey
	user  int64
}

// batcher glues a person's consecutive messages (and the photos of an album)
// into one request: it waits until they pause for `wait`, but no longer than
// `limit` from the first one.
type batcher struct {
	mu    sync.Mutex
	wait  time.Duration
	limit time.Duration
	open  map[batchKey]*pending
	flush func([]ent.BatchItem)
}

type pending struct {
	items []ent.BatchItem
	first time.Time
	timer *time.Timer
}

func newBatcher(wait time.Duration, flush func([]ent.BatchItem)) *batcher {
	return &batcher{wait: wait, limit: 5 * wait, open: map[batchKey]*pending{}, flush: flush}
}

func (b *batcher) has(k batchKey) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.open[k] != nil
}

func (b *batcher) add(k batchKey, it ent.BatchItem) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.open[k]
	if p == nil {
		p = &pending{first: time.Now()}
		b.open[k] = p
	}
	p.items = append(p.items, it)
	if p.timer != nil {
		p.timer.Stop()
	}
	d := min(b.wait, max(b.limit-time.Since(p.first), 0))
	p.timer = time.AfterFunc(d, func() { b.fire(k, p) })
}

// drop forgets what is still gathering ("stop" takes it back too).
func (b *batcher) drop(k batchKey) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p := b.open[k]; p != nil {
		p.timer.Stop()
		delete(b.open, k)
	}
}

func (b *batcher) fire(k batchKey, p *pending) {
	b.mu.Lock()
	if b.open[k] != p {
		b.mu.Unlock()
		return
	}
	delete(b.open, k)
	b.mu.Unlock()
	b.flush(p.items)
}
