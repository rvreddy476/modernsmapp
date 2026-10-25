package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CI integration run 40209314997 (user-service): concurrent media edits of
// one row on a 4-connection pool deadlocked. Each holder lock pinned a
// connection (holding, or waiting in Postgres for, the advisory lock) and the
// holder's own next query found none free. These tests drive the real lock
// function against a model of the pool and of Postgres's advisory lock.

// fakePool is a pool of len(conns) connections. Begin takes one for the
// transaction; acquire takes one for a holder's ordinary query. Its advisory
// locks wait as pg_advisory_xact_lock does, pinning the transaction's
// connection meanwhile, and free on rollback.
type fakePool struct {
	conns    chan struct{}
	mu       sync.Mutex
	locks    map[int64]chan struct{}
	beginErr error
	execErr  error
}

func newFakePool(n int) *fakePool {
	return &fakePool{conns: make(chan struct{}, n), locks: map[int64]chan struct{}{}}
}

func (p *fakePool) acquire(ctx context.Context) error {
	select {
	case p.conns <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *fakePool) release() { <-p.conns }

func (p *fakePool) lockFor(key int64) chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	l := p.locks[key]
	if l == nil {
		l = make(chan struct{}, 1)
		p.locks[key] = l
	}
	return l
}

func (p *fakePool) Begin(ctx context.Context) (pgx.Tx, error) {
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}
	return &fakeTx{p: p}, nil
}

type fakeTx struct {
	pgx.Tx // unimplemented methods panic: the lock uses only these two
	p      *fakePool
	held   chan struct{}
	done   bool
}

func (t *fakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if sql != lockMediaHolderSQL || len(args) != 1 {
		return pgconn.CommandTag{}, fmt.Errorf("unexpected statement %q %v", sql, args)
	}
	if t.p.execErr != nil {
		return pgconn.CommandTag{}, t.p.execErr
	}
	l := t.p.lockFor(args[0].(int64))
	select {
	case l <- struct{}{}:
		t.held = l
		return pgconn.CommandTag{}, nil
	case <-ctx.Done():
		return pgconn.CommandTag{}, ctx.Err()
	}
}

func (t *fakeTx) Rollback(context.Context) error {
	if t.done {
		return pgx.ErrTxClosed
	}
	t.done = true
	if t.held != nil {
		<-t.held
	}
	t.p.release()
	return nil
}

// runHolders starts callers concurrent holders on a pool of conns, each
// locking keyOf(i) and then running one query of its own on the pool, as
// every media holder does. It fails the test if any holder cannot get that
// connection: without the gate the pool deadlocks.
func runHolders(t *testing.T, conns, callers int, keyOf func(int) int64) {
	t.Helper()
	pool := newFakePool(conns)
	gate := newHolderGate(holderSlotsFor(int32(conns)))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var stuck atomic.Int32
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			unlock, err := lockMediaHolder(ctx, gate, pool, keyOf(i))
			if err != nil {
				stuck.Add(1)
				return
			}
			defer unlock()
			// Give every other caller time to take its connection first.
			time.Sleep(20 * time.Millisecond)
			qctx, qcancel := context.WithTimeout(ctx, 2*time.Second)
			defer qcancel()
			if err := pool.acquire(qctx); err != nil {
				stuck.Add(1)
				cancel() // the pool is deadlocked: free everyone so the test ends
				return
			}
			pool.release()
		}(i)
	}
	wg.Wait()
	if n := stuck.Load(); n > 0 {
		t.Fatalf("%d of %d holders on a %d-connection pool never got a connection for their own query: the holder lock deadlocks the pool", n, callers, conns)
	}
	if len(pool.conns) != 0 || len(gate.slots) != 0 || len(gate.keys) != 0 {
		t.Fatalf("left behind: conns %d slots %d keys %d", len(pool.conns), len(gate.slots), len(gate.keys))
	}
}

func TestHolderLockCannotDeadlockThePool(t *testing.T) {
	const conns = 4
	t.Run("one row, N+1 edits", func(t *testing.T) {
		runHolders(t, conns, conns+1, func(int) int64 { return 42 })
	})
	t.Run("N+1 rows", func(t *testing.T) {
		runHolders(t, conns, conns+1, func(i int) int64 { return int64(i) })
	})
	t.Run("small pool", func(t *testing.T) {
		runHolders(t, 2, 3, func(i int) int64 { return int64(i % 2) })
	})
}

func TestHolderLockLeavesTheGateOnFailure(t *testing.T) {
	boom := errors.New("boom")
	for name, pool := range map[string]*fakePool{
		"begin": {conns: make(chan struct{}, 2), locks: map[int64]chan struct{}{}, beginErr: boom},
		"lock":  {conns: make(chan struct{}, 2), locks: map[int64]chan struct{}{}, execErr: boom},
	} {
		t.Run(name, func(t *testing.T) {
			gate := newHolderGate(1)
			for i := 0; i < 2; i++ { // the second would wait forever on a kept slot
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_, err := lockMediaHolder(ctx, gate, pool, 7)
				cancel()
				if !errors.Is(err, boom) {
					t.Fatalf("attempt %d: err %v, want the %s failure", i+1, err, name)
				}
			}
			if len(pool.conns) != 0 || len(gate.slots) != 0 || len(gate.keys) != 0 {
				t.Fatalf("left behind: conns %d slots %d keys %d", len(pool.conns), len(gate.slots), len(gate.keys))
			}
		})
	}
}

func TestHolderUnlockIsIdempotent(t *testing.T) {
	pool := newFakePool(2)
	gate := newHolderGate(1)
	unlock, err := lockMediaHolder(context.Background(), gate, pool, 1)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	second := make(chan struct{})
	go func() { unlock(); close(second) }()
	select {
	case <-second:
	case <-time.After(2 * time.Second):
		t.Fatal("a second unlock blocked: it left the gate twice")
	}
	if len(pool.conns) != 0 || len(gate.slots) != 0 || len(gate.keys) != 0 {
		t.Fatalf("left behind: conns %d slots %d keys %d", len(pool.conns), len(gate.slots), len(gate.keys))
	}
}

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

func TestHolderGateSerialisesOneKeyInProcess(t *testing.T) {
	g := newHolderGate(8)
	var inside, maxInside int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			leave, err := g.enter(context.Background(), 9)
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
		t.Fatalf("%d callers for one key were inside at once; same-key callers must queue in process", maxInside)
	}
	if len(g.keys) != 0 {
		t.Fatalf("the gate kept %d idle keys", len(g.keys))
	}
}

func TestHolderGateBoundsHeldLocks(t *testing.T) {
	g := newHolderGate(2)
	a, err := g.enter(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.enter(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := g.enter(ctx, 3); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a third holder entered a 2-slot gate (err %v)", err)
	}
	a()
	c, err := g.enter(context.Background(), 4)
	if err != nil {
		t.Fatalf("a freed slot was not reusable: %v", err)
	}
	b()
	c()
	if len(g.slots) != 0 || len(g.keys) != 0 {
		t.Fatalf("slots %d keys %d left after every leave", len(g.slots), len(g.keys))
	}
}

// A caller that gives up while queued behind the same key takes nothing with
// it and leaves nothing behind.
func TestHolderGateCancelledWaitLeavesNothing(t *testing.T) {
	g := newHolderGate(4)
	leave, err := g.enter(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := g.enter(ctx, 5); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second caller for a held key was let in (err %v)", err)
	}
	leave()
	again, err := g.enter(context.Background(), 5)
	if err != nil {
		t.Fatalf("the key stayed blocked after a cancelled waiter: %v", err)
	}
	again()
	if len(g.slots) != 0 || len(g.keys) != 0 {
		t.Fatalf("slots %d keys %d left", len(g.slots), len(g.keys))
	}
}

// New sizes the gate from the pool it is given. The pool is never dialled.
func TestNewSizesTheHolderGateFromThePool(t *testing.T) {
	if s := New(nil); s.holders == nil || cap(s.holders.slots) != 1 {
		t.Fatal("New must always install the holder gate")
	}
	cfg, err := pgxpool.ParseConfig("postgres://nobody@127.0.0.1:1/never_test?pool_max_conns=6")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if s := New(pool); s.holders == nil || cap(s.holders.slots) != 3 {
		t.Fatalf("a 6-connection pool got %v holder slots, want 3", s.holders)
	}
}
