package postgres

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
)

// The holder lock and its in-process gate, the same gate as user-service's
// (Architecture/services/user-service/internal/store/media_claims.go).
//
// The lock's transaction pins one pool connection for as long as it is held
// or waited for, and the holder then needs a SECOND connection for its own
// reads and writes (they run on the pool, around media-service calls, so they
// cannot share the lock's transaction). Unbounded, that deadlocks the pool:
// with N connections, N callers each holding (or waiting in Postgres for) a
// lock leave none for the holder's next query, and nobody ever unlocks (CI
// integration run 40209314997 hung that way on a 4-connection pool). So
// before a connection is taken the caller passes the holderGate: same-key
// callers queue in process (only one of them waits in Postgres; the advisory
// lock still serialises across replicas), and at most holderSlotsFor(MaxConns)
// locks are held at once, so a holder always finds a free connection.
//
// A caller must never hold two holder locks at once: with one slot (a pool of
// fewer than four connections) the second would wait for itself.

// txBeginner is the one pool method the lock uses (*pgxpool.Pool).
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// lockMediaHolder passes the gate for key, then takes the advisory lock on a
// transaction of its own. unlock rolls the transaction back and leaves the
// gate; it is idempotent. Every failure leaves the gate too.
func lockMediaHolder(ctx context.Context, gate *holderGate, db txBeginner, key int64) (unlock func(), err error) {
	leave, err := gate.enter(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		leave() // no connection was taken
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	if _, err := tx.Exec(ctx, lockMediaHolderSQL, key); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		leave() // after the connection is back
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			leave()
		})
	}, nil
}

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

// holderGate is the in-process half of the holder lock. It is keyed by the
// advisory lock key, so two holders whose keys collide queue here exactly as
// they would in Postgres.
type holderGate struct {
	slots chan struct{} // one token per held lock
	mu    sync.Mutex
	keys  map[int64]*holderKey
}

type holderKey struct {
	turn  chan struct{} // capacity 1: the token is this key's turn
	users int           // callers holding or waiting for the turn
}

func newHolderGate(slots int) *holderGate {
	if slots < 1 {
		slots = 1
	}
	return &holderGate{slots: make(chan struct{}, slots), keys: map[int64]*holderKey{}}
}

// enter waits for key's turn and then for a slot, in that order (so callers
// queued behind the same key hold no slot). leave gives both back; it must be
// called exactly once. A cancelled ctx stops the wait with ctx.Err().
func (g *holderGate) enter(ctx context.Context, key int64) (leave func(), err error) {
	g.mu.Lock()
	k := g.keys[key]
	if k == nil {
		k = &holderKey{turn: make(chan struct{}, 1)}
		g.keys[key] = k
	}
	k.users++
	g.mu.Unlock()

	done := func() {
		g.mu.Lock()
		k.users--
		if k.users == 0 {
			delete(g.keys, key)
		}
		g.mu.Unlock()
	}
	select {
	case k.turn <- struct{}{}:
	case <-ctx.Done():
		done()
		return nil, ctx.Err()
	}
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		<-k.turn
		done()
		return nil, ctx.Err()
	}
	return func() {
		<-g.slots
		<-k.turn
		done()
	}, nil
}
