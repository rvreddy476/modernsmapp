package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// CI integration run 40209314997: five concurrent edits of one channel on a
// 4-connection pool deadlocked. Each LockMediaHolder pinned a connection
// (holding, or waiting in Postgres for, the advisory lock) and the holder's
// own next query found none free. The gate keeps lock holders below the pool.

func TestHolderSlotsLeaveConnectionsForTheHolders(t *testing.T) {
	for _, tc := range []struct {
		maxConns int32
		want     int
	}{{0, 1}, {1, 1}, {2, 1}, {3, 1}, {4, 2}, {5, 2}, {25, 12}} {
		got := holderSlotsFor(tc.maxConns)
		if got != tc.want {
			t.Errorf("holderSlotsFor(%d) = %d, want %d", tc.maxConns, got, tc.want)
		}
		if tc.maxConns > 1 && int32(got) >= tc.maxConns {
			t.Errorf("holderSlotsFor(%d) = %d leaves no connection for a holder's query", tc.maxConns, got)
		}
	}
}

func TestHolderGateSerialisesOneRefInProcess(t *testing.T) {
	g := newHolderGate(8)
	ref := uuid.New()
	var inside, maxInside int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			leave, err := g.enter(context.Background(), ref)
			if err != nil {
				t.Error(err)
				return
			}
			n := atomic.AddInt32(&inside, 1)
			for {
				m := atomic.LoadInt32(&maxInside)
				if n <= m || atomic.CompareAndSwapInt32(&maxInside, m, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&inside, -1)
			leave()
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("%d callers for one ref were inside at once; same-ref callers must queue in process", maxInside)
	}
	if len(g.refs) != 0 {
		t.Fatalf("the gate kept %d idle refs", len(g.refs))
	}
}

func TestHolderGateBoundsHeldLocks(t *testing.T) {
	g := newHolderGate(2)
	a, err := g.enter(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.enter(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := g.enter(ctx, uuid.New()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a third holder entered a 2-slot gate (err %v)", err)
	}
	a()
	c, err := g.enter(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("a freed slot was not reusable: %v", err)
	}
	b()
	c()
	if len(g.slots) != 0 || len(g.refs) != 0 {
		t.Fatalf("slots %d refs %d left after every leave", len(g.slots), len(g.refs))
	}
}

// A caller that gives up while queued behind the same ref takes nothing with
// it and leaves nothing behind.
func TestHolderGateCancelledWaitLeavesNothing(t *testing.T) {
	g := newHolderGate(4)
	ref := uuid.New()
	leave, err := g.enter(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := g.enter(ctx, ref); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second caller for a held ref was let in (err %v)", err)
	}
	leave()
	again, err := g.enter(context.Background(), ref)
	if err != nil {
		t.Fatalf("the ref stayed blocked after a cancelled waiter: %v", err)
	}
	again()
	if len(g.slots) != 0 || len(g.refs) != 0 {
		t.Fatalf("slots %d refs %d left", len(g.slots), len(g.refs))
	}
}

func TestStoreWithoutPoolStillGates(t *testing.T) {
	s := New(nil)
	if s.holders == nil || cap(s.holders.slots) != 1 {
		t.Fatal("New must always install the holder gate")
	}
}
