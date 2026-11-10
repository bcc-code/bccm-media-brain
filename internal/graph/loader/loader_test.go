package loader

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBatchesConcurrentLoads(t *testing.T) {
	var calls atomic.Int32
	l := New(func(_ context.Context, keys []int) (map[int]string, error) {
		calls.Add(1)
		out := map[int]string{}
		for _, k := range keys {
			if k != 99 {
				out[k] = string(rune('a' + k))
			}
		}
		return out, nil
	})

	var wg sync.WaitGroup
	for _, k := range []int{0, 1, 2, 1, 99} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := l.Load(context.Background(), k)
			want := string(rune('a' + k))
			if k == 99 {
				want = "" // missing keys give the zero value
			}
			if err != nil || got != want {
				t.Errorf("Load(%d) = %q, %v", k, got, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("fetch ran %d times, want 1", calls.Load())
	}

	// Already loaded keys are served from the cache; new keys start a new batch.
	if got, _ := l.Load(context.Background(), 2); got != "c" || calls.Load() != 1 {
		t.Fatalf("cached load = %q after %d calls", got, calls.Load())
	}
	if got, _ := l.Load(context.Background(), 3); got != "d" || calls.Load() != 2 {
		t.Fatalf("new key = %q after %d calls", got, calls.Load())
	}
}

func TestFetchError(t *testing.T) {
	boom := errors.New("boom")
	l := New(func(context.Context, []string) (map[string]int, error) { return nil, boom })
	if _, err := l.Load(context.Background(), "x"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}
