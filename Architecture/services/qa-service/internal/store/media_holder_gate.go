package store

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// The in-process half of LockMediaHolder, the same gate as user-service's
// (Architecture/services/user-service/internal/store/media_claims.go).
//
// The lock's transaction pins one pool connection for as long as it is held
// or waited for, and the holder then needs a SECOND connection for its own
// reads and writes. Unbounded, that deadlocks the pool: with N connections, N
// callers each holding (or waiting in Postgres for) a lock leave none for the
// holder's next query, and nobody ever unlocks (CI integration run
// 40209314997: four concurrent edits of one update on a 4-connection pool
// hung until the 10-minute test timeout). So before a connection is taken the
// caller passes the holderGate: same-ref callers queue in process (only one of
// them waits in Postgres; the advisory lock still serialises across
// replicas), and at most holderSlotsFor(MaxConns) locks are held at once, so a
// holder always finds a free connection.

// holderSlotsFor is how many holder locks may be held at once on a pool of
// maxConns connections: half, so lock holders can never take the last
// connection (each needs one more than it pins) and ordinary traffic keeps
// the rest. At least one.
func holderSlotsFor(maxConns int32) int {
	if n := int(maxConns / 2); n > 1 {
		return n
	}
	return 1
}

type holderGate struct {
	slots chan struct{} // one token per held lock
	mu    sync.Mutex
	refs  map[uuid.UUID]*holderRef
}

type holderRef struct {
	turn  chan struct{} // capacity 1: the token is this ref's turn
	users int           // callers holding or waiting for the turn
}

func newHolderGate(slots int) *holderGate {
	if slots < 1 {
		slots = 1
	}
	return &holderGate{slots: make(chan struct{}, slots), refs: map[uuid.UUID]*holderRef{}}
}

// enter waits for ref's turn and then for a slot, in that order (so callers
// queued behind the same ref hold no slot). leave gives both back; it must be
// called exactly once. A cancelled ctx stops the wait with ctx.Err().
func (g *holderGate) enter(ctx context.Context, ref uuid.UUID) (leave func(), err error) {
	g.mu.Lock()
	r := g.refs[ref]
	if r == nil {
		r = &holderRef{turn: make(chan struct{}, 1)}
		g.refs[ref] = r
	}
	r.users++
	g.mu.Unlock()

	done := func() {
		g.mu.Lock()
		r.users--
		if r.users == 0 {
			delete(g.refs, ref)
		}
		g.mu.Unlock()
	}
	select {
	case r.turn <- struct{}{}:
	case <-ctx.Done():
		done()
		return nil, ctx.Err()
	}
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		<-r.turn
		done()
		return nil, ctx.Err()
	}
	return func() {
		<-g.slots
		<-r.turn
		done()
	}, nil
}
