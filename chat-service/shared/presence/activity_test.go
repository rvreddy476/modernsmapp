package presence

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newActivityStore(t *testing.T) (*RedisStore, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewRedisStore(rdb, Options{GlobalUserTTL: 90 * time.Second}), rdb
}

// Closing one of two connections must NOT report the user offline — the
// legacy marker used to be deleted on every disconnect.
func TestSecondConnectionKeepsUserOnline(t *testing.T) {
	store, rdb := newActivityStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	first, err := store.ConnectionOpened(ctx, "u1", "tab", now)
	if err != nil || !first {
		t.Fatalf("first connection must be the online transition: %v %v", first, err)
	}
	second, err := store.ConnectionOpened(ctx, "u1", "phone", now)
	if err != nil || second {
		t.Fatalf("second connection is not a transition: %v %v", second, err)
	}

	offline, err := store.ConnectionClosed(ctx, "u1", "tab", now.Add(time.Second))
	if err != nil || offline {
		t.Fatalf("closing one of two connections reported offline: %v %v", offline, err)
	}
	if got, _ := Lookup(ctx, rdb, []string{"u1"}); !got["u1"].Online {
		t.Fatal("user must stay online while the phone is connected")
	}

	offline, err = store.ConnectionClosed(ctx, "u1", "phone", now.Add(2*time.Second))
	if err != nil || !offline {
		t.Fatalf("closing the last connection must be the offline transition: %v %v", offline, err)
	}
	got, _ := Lookup(ctx, rdb, []string{"u1"})
	if got["u1"].Online {
		t.Fatal("legacy marker must be gone after the last close")
	}
	if want := now.Add(2 * time.Second).UTC(); !got["u1"].LastActiveAt.Equal(want) {
		t.Fatalf("last active = %v, want %v", got["u1"].LastActiveAt, want)
	}
}

// A connection whose heartbeat stopped (gateway crash) ages out, so the next
// connect is an online transition again.
func TestStaleConnectionAgesOut(t *testing.T) {
	store, _ := newActivityStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	if _, err := store.ConnectionOpened(ctx, "u1", "crashed", now); err != nil {
		t.Fatal(err)
	}
	again, err := store.ConnectionOpened(ctx, "u1", "fresh", now.Add(91*time.Second))
	if err != nil || !again {
		t.Fatalf("an aged-out connection must not hold the user online: %v %v", again, err)
	}
}

func TestHeartbeatAndLastActiveRead(t *testing.T) {
	store, rdb := newActivityStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	if _, err := store.ConnectionOpened(ctx, "u1", "c", now); err != nil {
		t.Fatal(err)
	}
	if err := store.ConnectionHeartbeat(ctx, "u1", "c", now.Add(80*time.Second)); err != nil {
		t.Fatal(err)
	}
	// 80s heartbeat pushed expiry to 170s: a connect at 100s is not a transition.
	if again, _ := store.ConnectionOpened(ctx, "u1", "c2", now.Add(100*time.Second)); again {
		t.Fatal("heartbeat did not extend the connection")
	}
	got, err := LastActiveAt(ctx, rdb, []string{"u1", "never"})
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(100 * time.Second).UTC(); !got["u1"].Equal(want) {
		t.Fatalf("last active = %v, want %v", got["u1"], want)
	}
	if _, ok := got["never"]; ok {
		t.Fatal("a user with no activity must be absent")
	}
}
