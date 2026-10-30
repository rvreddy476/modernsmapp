package callauth

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

// Audit M6: a call that outlives DefaultTTL kept working only until the key
// expired. Refresh re-arms the TTL of the active tuple for the same call.
func TestRefreshExtendsActivePairPastDefaultTTL(t *testing.T) {
	mr, rdb := newRedis(t)
	ctx := context.Background()
	a, b, call := uuid.New(), uuid.New(), uuid.New()
	if err := Set(ctx, rdb, a, b, call, StateActive); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(50 * time.Minute)

	ok, err := Refresh(ctx, rdb, b, a, call, 0) // either argument order
	if err != nil || !ok {
		t.Fatalf("refresh of the active pair: ok=%v err=%v", ok, err)
	}
	if ttl := mr.TTL(PairKey(a, b)); ttl != DefaultTTL {
		t.Fatalf("TTL after refresh = %v, want %v", ttl, DefaultTTL)
	}
	// 50 + 50 minutes is past the original hour; the pair still authorizes.
	mr.FastForward(50 * time.Minute)
	st, err := Get(ctx, rdb, a, b)
	if err != nil || st == nil || st.State != StateActive {
		t.Fatalf("refreshed pair expired: state=%+v err=%v", st, err)
	}
}

// Refresh must never revive authorization the end of the call cleared.
func TestRefreshNeverRecreatesAClearedPair(t *testing.T) {
	mr, rdb := newRedis(t)
	ctx := context.Background()
	a, b, call := uuid.New(), uuid.New(), uuid.New()
	if err := Set(ctx, rdb, a, b, call, StateActive); err != nil {
		t.Fatal(err)
	}
	if err := Clear(ctx, rdb, a, b); err != nil {
		t.Fatal(err)
	}
	ok, err := Refresh(ctx, rdb, a, b, call, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok || mr.Exists(PairKey(a, b)) {
		t.Fatal("refresh re-created the authorization of an ended call")
	}
}

func TestRefreshIgnoresAnotherCallsTuple(t *testing.T) {
	mr, rdb := newRedis(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	oldCall, newCall := uuid.New(), uuid.New()
	if err := Set(ctx, rdb, a, b, newCall, StateActive); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(30 * time.Minute)
	ok, err := Refresh(ctx, rdb, a, b, oldCall, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok || mr.TTL(PairKey(a, b)) != 30*time.Minute {
		t.Fatalf("refresh for a stale call extended another call's tuple (ok=%v ttl=%v)", ok, mr.TTL(PairKey(a, b)))
	}
}

// A ringing tuple is not extended: ringing is bounded by the ring timeout,
// and only an accepted call earns a longer life.
func TestRefreshIgnoresRingingTuple(t *testing.T) {
	mr, rdb := newRedis(t)
	ctx := context.Background()
	a, b, call := uuid.New(), uuid.New(), uuid.New()
	if err := Set(ctx, rdb, a, b, call, StateRinging); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(10 * time.Minute)
	ok, err := Refresh(ctx, rdb, a, b, call, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok || mr.TTL(PairKey(a, b)) != DefaultTTL-10*time.Minute {
		t.Fatalf("ringing tuple was extended (ok=%v ttl=%v)", ok, mr.TTL(PairKey(a, b)))
	}
}

func TestRefreshNilClient(t *testing.T) {
	if _, err := Refresh(context.Background(), nil, uuid.New(), uuid.New(), uuid.New(), 0); err == nil {
		t.Fatal("nil client must error")
	}
}
