package postgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

// LockMediaHolder's in-process admission (holderGate). Without it, edits of
// one row fill the pool with lock transactions and the holder never gets the
// connection its write needs (CI run 40209314997 hung 30 minutes on
// TestMediaClaimsIT_ConcurrentCoverEditsNeverLeaveTheCoverUnclaimed).

const gateWait = 50 * time.Millisecond

func acquireSoon(t *testing.T, g *holderGate, key int64) (func(), bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), gateWait)
	defer cancel()
	release, err := g.acquire(ctx, key)
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("acquire: %v", err)
		}
		return nil, false
	}
	return release, true
}

// A second writer of the same row waits for the first, and while it waits it
// holds no slot: another row still gets in.
func TestHolderGateSerialisesOneKeyWithoutTakingASlot(t *testing.T) {
	g := newHolderGate(2)
	r1, ok := acquireSoon(t, g, 1)
	if !ok {
		t.Fatal("the first writer of a row must get in")
	}

	waiting := make(chan func(), 1)
	go func() {
		r, err := g.acquire(context.Background(), 1)
		if err != nil {
			t.Errorf("queued writer: %v", err)
		}
		waiting <- r
	}()
	select {
	case <-waiting:
		t.Fatal("two writers of one row held the gate at once")
	case <-time.After(gateWait):
	}

	// Two slots, one held by key 1: key 2 gets in even though key 1 has a
	// queued writer, because the queued writer holds no slot.
	r2, ok := acquireSoon(t, g, 2)
	if !ok {
		t.Fatal("a writer queued on one row took a slot from another row")
	}
	r2()

	r1()
	select {
	case r := <-waiting:
		r()
	case <-time.After(time.Second):
		t.Fatal("the queued writer was not let in when the first released")
	}
	if n := len(g.keys); n != 0 {
		t.Fatalf("%d key entries left after every writer released", n)
	}
}

// Distinct rows are bounded by the slots: the pool keeps connections for the
// holders' work.
func TestHolderGateBoundsConcurrentHolders(t *testing.T) {
	g := newHolderGate(2)
	r1, ok1 := acquireSoon(t, g, 1)
	r2, ok2 := acquireSoon(t, g, 2)
	if !ok1 || !ok2 {
		t.Fatal("two slots must admit two rows")
	}
	if r, ok := acquireSoon(t, g, 3); ok {
		r()
		t.Fatal("a third row got in past two slots")
	}
	r1()
	r3, ok := acquireSoon(t, g, 3)
	if !ok {
		t.Fatal("a freed slot was not reused")
	}
	r2()
	r3()
	if n := len(g.slots); n != 0 {
		t.Fatalf("%d slots still held", n)
	}
}

// A cancelled wait gives back what it held, so the row is not wedged.
func TestHolderGateCancelledWaitLeavesNothingHeld(t *testing.T) {
	g := newHolderGate(1)
	r1, _ := acquireSoon(t, g, 1)
	if _, ok := acquireSoon(t, g, 1); ok { // waits on the key, then times out
		t.Fatal("same row admitted twice")
	}
	if _, ok := acquireSoon(t, g, 2); ok { // waits on the slot, then times out
		t.Fatal("a second row got in past one slot")
	}
	r1()
	for _, key := range []int64{1, 2} {
		r, ok := acquireSoon(t, g, key)
		if !ok {
			t.Fatalf("row %d wedged after a cancelled wait", key)
		}
		r()
	}
	if len(g.keys) != 0 || len(g.slots) != 0 {
		t.Fatalf("left behind: keys=%d slots=%d", len(g.keys), len(g.slots))
	}
}

// Half the pool, never zero.
func TestHolderLockSlots(t *testing.T) {
	for maxConns, want := range map[int32]int{0: 1, 1: 1, 2: 1, 4: 2, 25: 12} {
		if got := holderLockSlots(maxConns); got != want {
			t.Errorf("holderLockSlots(%d) = %d, want %d", maxConns, got, want)
		}
	}
}
