// Package loader batches lookups made while one GraphQL request resolves, so a list of
// parents costs one query per child field instead of one per parent.
package loader

import (
	"context"
	"sync"
	"time"
)

// Wait is how long a batch collects keys before it is fetched.
const Wait = time.Millisecond

type batch[K comparable, V any] struct {
	keys []K
	done chan struct{}
	data map[K]V
	err  error
}

// Loader collects keys for a short moment and fetches them together. Results are kept for the
// life of the Loader, so create one per request.
type Loader[K comparable, V any] struct {
	fetch func(ctx context.Context, keys []K) (map[K]V, error)

	mu      sync.Mutex
	pending *batch[K, V]
	seen    map[K]*batch[K, V]
}

func New[K comparable, V any](fetch func(ctx context.Context, keys []K) (map[K]V, error)) *Loader[K, V] {
	return &Loader[K, V]{fetch: fetch, seen: map[K]*batch[K, V]{}}
}

// Load returns the value for key, or the zero value when the fetch has none for it.
func (l *Loader[K, V]) Load(ctx context.Context, key K) (V, error) {
	l.mu.Lock()
	b, ok := l.seen[key]
	if !ok {
		if l.pending == nil {
			l.pending = &batch[K, V]{done: make(chan struct{})}
			go l.run(ctx, l.pending)
		}
		b = l.pending
		b.keys = append(b.keys, key)
		l.seen[key] = b
	}
	l.mu.Unlock()

	<-b.done
	return b.data[key], b.err
}

func (l *Loader[K, V]) run(ctx context.Context, b *batch[K, V]) {
	time.Sleep(Wait)
	l.mu.Lock()
	l.pending = nil
	l.mu.Unlock()
	b.data, b.err = l.fetch(ctx, b.keys)
	close(b.done)
}
