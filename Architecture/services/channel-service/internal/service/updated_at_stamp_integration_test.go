//go:build integration

// DB-backed proof that broadcast_channels.updated_at only ever moves forward
// (2026-11-08; store.BroadcastChannel). search-service orders channel
// snapshots by it and drops one that is not newer, so a write that commits
// last must carry the newest stamp. updated_at_stamp_test.go pins the
// statements. Requires TEST_PG_DSN on a "_test" database; skipped when unset.
package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func storedUpdatedAt(t *testing.T, pool *pgxpool.Pool, channelID uuid.UUID) time.Time {
	t.Helper()
	var at time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT updated_at FROM broadcast_channels WHERE id = $1`, channelID).Scan(&at); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	return at
}

func dropStampChannel(t *testing.T, pool *pgxpool.Pool, channelID uuid.UUID) {
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM broadcast_channels WHERE id = $1`, channelID)
	})
}

// The clock cannot pass a stamp in the year 2999, so this is a clock that
// does not advance (or runs behind the row): every write must still land
// exactly one microsecond after the version it replaces.
func TestUpdatedAtStampIT_SequentialWritesIncreaseWhenTheClockDoesNot(t *testing.T) {
	_, st, pool := setupReactionCountIT(t)
	ctx := context.Background()
	id := seedGateChannel(t, pool, "public", uuid.New())
	dropStampChannel(t, pool, id)
	ahead := time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `UPDATE broadcast_channels SET updated_at = $2 WHERE id = $1`, id, ahead); err != nil {
		t.Fatalf("set updated_at ahead of the clock: %v", err)
	}

	edit := func() error {
		ch, err := st.GetChannelByID(ctx, id)
		if err != nil {
			return err
		}
		ch.Name += "!"
		if err := st.UpdateChannel(ctx, ch); err != nil {
			return err
		}
		// The row as returned by the write is the row as stored.
		if got := storedUpdatedAt(t, pool, id); !ch.UpdatedAt.Equal(got) {
			t.Errorf("UpdateChannel returned updated_at %v, row holds %v", ch.UpdatedAt, got)
		}
		return nil
	}
	writes := []struct {
		name string
		run  func() error
	}{
		{"edit", edit},
		{"edit again", edit},
		{"update count", func() error { return st.IncrementUpdateCount(ctx, id, 1) }},
		{"suspend", func() error { return st.SetChannelStatus(ctx, id, "suspended") }},
		{"unsuspend", func() error { return st.SetChannelStatus(ctx, id, "active") }},
		{"archive", func() error { return st.ArchiveChannel(ctx, id) }},
		{"delete", func() error { return st.DeleteChannel(ctx, id) }},
	}
	prev := storedUpdatedAt(t, pool, id)
	for i, w := range writes {
		if err := w.run(); err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		got := storedUpdatedAt(t, pool, id)
		want := ahead.Add(time.Duration(i+1) * time.Microsecond)
		if !got.After(prev) || !got.Equal(want) {
			t.Fatalf("%s: updated_at = %v after %v, want %v (strictly newer than the version replaced)",
				w.name, got, prev, want)
		}
		prev = got
	}
}

// The race itself. Another writer holds the row; a suspension starts (its
// NOW() is fixed there) and waits on the lock; the holder then stamps the
// row with the clock and commits; the suspension commits last. With
// updated_at = NOW() the suspension, the newer state, would carry the older
// stamp and search would drop it as stale.
func TestUpdatedAtStampIT_ALockWaiterCommittingLastIsNewest(t *testing.T) {
	_, st, pool := setupReactionCountIT(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id := seedGateChannel(t, pool, "public", uuid.New())
	dropStampChannel(t, pool, id)

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(ctx, `SELECT 1 FROM broadcast_channels WHERE id = $1 FOR UPDATE`, id); err != nil {
		t.Fatalf("lock the row: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- st.SetChannelStatus(ctx, id, "suspended") }()

	// Wait until the suspension is blocked on the row lock, so its
	// transaction (and NOW()) started before the holder's stamp below.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock'
			   AND query ILIKE 'UPDATE broadcast_channels SET status%'`).Scan(&waiting); err != nil {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the suspension never waited on the row lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	var held time.Time
	if err := holder.QueryRow(ctx,
		`UPDATE broadcast_channels SET updated_at = clock_timestamp() WHERE id = $1 RETURNING updated_at`,
		id).Scan(&held); err != nil {
		t.Fatalf("holder stamp: %v", err)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("holder commit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("suspend: %v", err)
	}

	var status string
	var got time.Time
	if err := pool.QueryRow(ctx, `SELECT status, updated_at FROM broadcast_channels WHERE id = $1`, id).Scan(&status, &got); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != "suspended" || !got.After(held) {
		t.Fatalf("suspension committed last with updated_at %v, not after the holder's %v (status %s): search would drop it as stale",
			got, held, status)
	}
}
