package presence

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestStore(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewRedisStore(rdb, Options{
		ConversationActiveWindow: 2 * time.Second,
		ConversationKeyTTL:       4 * time.Second,
		TypingActiveWindow:       1 * time.Second,
		TypingKeyTTL:             2 * time.Second,
	}), mr
}

func TestEnterAndCount(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	if err := store.EnterConversation(ctx, "c1", "u1"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnterConversation(ctx, "c1", "u2"); err != nil {
		t.Fatal(err)
	}
	count, err := store.ActiveCount(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("active count = %d, want 2", count)
	}
}

func TestLeave(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	_ = store.EnterConversation(ctx, "c1", "u1")
	_ = store.EnterConversation(ctx, "c1", "u2")
	if err := store.LeaveConversation(ctx, "c1", "u1"); err != nil {
		t.Fatal(err)
	}
	users, err := store.ActiveUsers(ctx, "c1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0] != "u2" {
		t.Fatalf("active users = %v, want [u2]", users)
	}
}

func TestHeartbeatEvictsStale(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	_ = store.EnterConversation(ctx, "c1", "u1")
	// u1 went idle. Sleep past ConversationActiveWindow (2s in test cfg).
	time.Sleep(2100 * time.Millisecond)
	// u2 sends a heartbeat — that should evict u1 even though u1
	// didn't call leave.
	if err := store.HeartbeatConversation(ctx, "c1", "u2"); err != nil {
		t.Fatal(err)
	}
	users, err := store.ActiveUsers(ctx, "c1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0] != "u2" {
		t.Fatalf("active users after eviction = %v, want [u2]", users)
	}
}

func TestActiveUsersEvictsOnRead(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	_ = store.EnterConversation(ctx, "c1", "u1")
	time.Sleep(2100 * time.Millisecond)
	users, err := store.ActiveUsers(ctx, "c1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 0 {
		t.Fatalf("expected empty after stale, got %v", users)
	}
}

func TestTyping(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	if err := store.SetTyping(ctx, "c1", "u1"); err != nil {
		t.Fatal(err)
	}
	users, _ := store.TypingUsers(ctx, "c1", 10)
	if len(users) != 1 || users[0] != "u1" {
		t.Fatalf("typing = %v, want [u1]", users)
	}
	// Sleep past typing window (1s in test cfg) — should evict.
	time.Sleep(1100 * time.Millisecond)
	users, _ = store.TypingUsers(ctx, "c1", 10)
	if len(users) != 0 {
		t.Fatalf("typing after evict = %v, want []", users)
	}
}

func TestGlobalOnline(t *testing.T) {
	store, mr := newTestStore(t)
	ctx := context.Background()
	if err := store.SetUserOnline(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if !mr.Exists("presence:u1") {
		t.Fatalf("expected key to exist after SetUserOnline")
	}
	if err := store.ClearUserOnline(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if mr.Exists("presence:u1") {
		t.Fatalf("expected key to be cleared")
	}
}
