// DB-backed tests for broadcast_channels.subscriber_count (2026-11-07).
//
// The count is the roster: non-banned channel_members rows. It used to be a
// Redis shard sum written over the column, never seeded from Postgres, so a
// channel's first join dropped the owner from the count. These run the real
// statements. Requires TEST_PG_DSN on a "_test" database; skipped when unset.
package service

import (
	"context"
	"testing"

	"github.com/atpost/channel-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func storedSubscriberCount(t *testing.T, pool *pgxpool.Pool, channelID uuid.UUID) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(),
		`SELECT subscriber_count FROM broadcast_channels WHERE id = $1`, channelID).Scan(&n); err != nil {
		t.Fatalf("read subscriber_count: %v", err)
	}
	return n
}

func setStoredSubscriberCount(t *testing.T, pool *pgxpool.Pool, channelID uuid.UUID, n int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE broadcast_channels SET subscriber_count = $2 WHERE id = $1`, channelID, n); err != nil {
		t.Fatalf("set subscriber_count: %v", err)
	}
}

// The reported defect: the owner counts, and a join must not replace that 1
// with a 1. Every later change lands on the roster, whatever the column said.
func TestSubscriberCountIT_IsTheRosterAfterEveryChange(t *testing.T) {
	svc, _, pool := setupReactionCountIT(t)
	svc.policy.PilotMode = false
	ctx := context.Background()
	owner, a, b := uuid.New(), uuid.New(), uuid.New()
	ch := seedGateChannel(t, pool, "public", owner)
	setStoredSubscriberCount(t, pool, ch, 1) // as CreateChannel writes it

	for _, u := range []uuid.UUID{a, b} {
		if err := svc.Subscribe(ctx, ch, u); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	}
	if got := storedSubscriberCount(t, pool, ch); got != 3 {
		t.Fatalf("owner + 2 joins: subscriber_count = %d, want 3", got)
	}

	// A wrong stored value (drift from the old counter) heals on the next change.
	setStoredSubscriberCount(t, pool, ch, 40)
	if err := svc.Unsubscribe(ctx, ch, a); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if got := storedSubscriberCount(t, pool, ch); got != 2 {
		t.Fatalf("after a leave from a drifted 40: subscriber_count = %d, want 2", got)
	}

	// A banned row is not a subscriber.
	if err := svc.BanMember(ctx, ch, owner, b); err != nil {
		t.Fatalf("ban: %v", err)
	}
	if got := storedSubscriberCount(t, pool, ch); got != 1 {
		t.Fatalf("after banning the last subscriber: subscriber_count = %d, want 1", got)
	}
	if err := svc.UnbanMember(ctx, ch, owner, b); err != nil {
		t.Fatalf("unban: %v", err)
	}
	if got := storedSubscriberCount(t, pool, ch); got != 2 {
		t.Fatalf("after unban: subscriber_count = %d, want 2", got)
	}
}

// Account deletion removes the person from every channel; each of those
// channels must stop counting them.
func TestSubscriberCountIT_AccountDeletionRecountsEveryChannelLeft(t *testing.T) {
	_, st, pool := setupReactionCountIT(t)
	ctx := context.Background()
	leaver := uuid.New()
	var chs []uuid.UUID
	for i := 0; i < 2; i++ {
		ch := seedGateChannel(t, pool, "public", uuid.New())
		seedGateMember(t, pool, ch, leaver, "subscriber")
		setStoredSubscriberCount(t, pool, ch, 2)
		chs = append(chs, ch)
	}
	if err := st.RemoveUserFromAllChannels(ctx, leaver); err != nil {
		t.Fatalf("RemoveUserFromAllChannels: %v", err)
	}
	for _, ch := range chs {
		if got := storedSubscriberCount(t, pool, ch); got != 1 {
			t.Fatalf("channel %s after the member was erased: subscriber_count = %d, want 1 (the owner)", ch, got)
		}
	}
}

// The hand-run repair (migrations/002) fixes wrong rows and is a no-op on a
// second run.
func TestSubscriberCountIT_RepairScriptRecountsAndIsIdempotent(t *testing.T) {
	_, _, pool := setupReactionCountIT(t)
	ctx := context.Background()
	ch := seedGateChannel(t, pool, "public", uuid.New())
	seedGateMember(t, pool, ch, uuid.New(), "subscriber")
	seedGateMember(t, pool, ch, uuid.New(), "banned")
	setStoredSubscriberCount(t, pool, ch, 0)

	if _, err := pool.Exec(ctx, database.RecountSubscriberCountSQL); err != nil {
		t.Fatalf("run 002: %v", err)
	}
	if got := storedSubscriberCount(t, pool, ch); got != 2 {
		t.Fatalf("after 002: subscriber_count = %d, want 2 (owner + subscriber, not the banned row)", got)
	}
	if _, err := pool.Exec(ctx, database.RecountSubscriberCountSQL); err != nil {
		t.Fatalf("re-run 002: %v", err)
	}
	if got := storedSubscriberCount(t, pool, ch); got != 2 {
		t.Fatalf("after a second run of 002: subscriber_count = %d, want 2", got)
	}
}

// A suspension, and lifting it, are announced with the new status: search
// drops a suspended channel only when told.
func TestChannelStateIT_SuspendAndUnsuspendAreAnnounced(t *testing.T) {
	svc, _, pool := setupReactionCountIT(t)
	rec := &recordedState{}
	svc.stateSeam = rec
	ctx := context.Background()
	ch := seedGateChannel(t, pool, "public", uuid.New())

	if err := svc.SuspendChannel(ctx, ch, "test"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if err := svc.UnsuspendChannel(ctx, ch); err != nil {
		t.Fatalf("unsuspend: %v", err)
	}
	if len(rec.snaps) != 2 || rec.snaps[0].Status != "suspended" || rec.snaps[1].Status != "active" {
		t.Fatalf("announced %+v, want suspended then active", rec.snaps)
	}
	if rec.snaps[0].ChannelID != ch.String() || rec.snaps[0].Name == "" || rec.snaps[0].OwnerID == "" {
		t.Fatalf("the snapshot must be the whole row: %+v", rec.snaps[0])
	}
}
