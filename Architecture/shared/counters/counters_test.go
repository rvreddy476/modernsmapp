package counters

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newCounter(t *testing.T) (*Counter, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return New(rdb, Config{EntityKind: "test_count", Shards: 8}), mr
}

func TestIncAndRead(t *testing.T) {
	c, _ := newCounter(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := c.Inc(ctx, "e1", 1); err != nil {
			t.Fatal(err)
		}
	}
	total, err := c.Read(ctx, "e1")
	if err != nil {
		t.Fatal(err)
	}
	if total != 10 {
		t.Fatalf("total = %d, want 10", total)
	}
}

func TestIncDistributesAcrossShards(t *testing.T) {
	c, mr := newCounter(t)
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		_ = c.Inc(ctx, "e1", 1)
	}
	// 200 increments across 8 shards should hit more than one — if all
	// 200 landed on a single shard the random shard selection is broken.
	hit := 0
	for s := 0; s < 8; s++ {
		if mr.Exists(c.shardKey("e1", s)) {
			hit++
		}
	}
	if hit < 2 {
		t.Fatalf("expected increments to spread across shards, hit=%d", hit)
	}
}

func TestNegativeDelta(t *testing.T) {
	c, _ := newCounter(t)
	ctx := context.Background()
	_ = c.Inc(ctx, "e1", 5)
	_ = c.Inc(ctx, "e1", -2)
	total, _ := c.Read(ctx, "e1")
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
}

func TestSeedClearsOldShards(t *testing.T) {
	c, _ := newCounter(t)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		_ = c.Inc(ctx, "e1", 1)
	}
	// Now seed to a fixed value — should clobber all shards.
	if err := c.Seed(ctx, "e1", 99); err != nil {
		t.Fatal(err)
	}
	total, _ := c.Read(ctx, "e1")
	if total != 99 {
		t.Fatalf("total after seed = %d, want 99", total)
	}
}

func TestPopDirty(t *testing.T) {
	c, _ := newCounter(t)
	ctx := context.Background()
	_ = c.Inc(ctx, "e1", 1)
	_ = c.Inc(ctx, "e2", 1)
	_ = c.Inc(ctx, "e3", 1)
	ids, err := c.PopDirty(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Fatalf("popped = %d, want 3", len(ids))
	}
	// Second pop should be empty.
	ids, _ = c.PopDirty(ctx, 100)
	if len(ids) != 0 {
		t.Fatalf("second pop = %d, want 0", len(ids))
	}
}

func TestWorkerFlushSuccess(t *testing.T) {
	c, _ := newCounter(t)
	ctx := context.Background()
	_ = c.Inc(ctx, "e1", 7)
	_ = c.Inc(ctx, "e2", 3)

	var mu sync.Mutex
	got := map[string]int64{}
	w := NewWorker(c, func(_ context.Context, id string, total int64) error {
		mu.Lock()
		defer mu.Unlock()
		got[id] = total
		return nil
	}, WorkerOptions{})
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got["e1"] != 7 || got["e2"] != 3 {
		t.Fatalf("flushed values = %v", got)
	}
}

func TestWorkerFlushRetriesOnFailure(t *testing.T) {
	c, _ := newCounter(t)
	ctx := context.Background()
	_ = c.Inc(ctx, "e1", 1)
	w := NewWorker(c, func(_ context.Context, _ string, _ int64) error {
		return errors.New("db down")
	}, WorkerOptions{})
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	// Entity should be re-marked dirty.
	ids, _ := c.PopDirty(ctx, 100)
	if len(ids) != 1 || ids[0] != "e1" {
		t.Fatalf("re-queued = %v, want [e1]", ids)
	}
}
