//go:build integration

// DB-backed proof that communities.updated_at only ever moves forward
// (2026-11-08; store.Community). search-service orders community snapshots
// by it and drops one that is not newer, so a write that commits last must
// carry the newest stamp. updated_at_stamp_test.go pins the statements.
// TEST_PG_DSN on a "_test" database, CI only.
package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedStampCommunity(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO communities (owner_id, handle, name) VALUES ($1, $2, 'stamp community') RETURNING id`,
		uuid.New(), "st"+strings.ReplaceAll(uuid.NewString(), "-", "")[:16]).Scan(&id); err != nil {
		t.Fatalf("seed community: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM communities WHERE id = $1`, id)
	})
	return id
}

func communityUpdatedAt(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) time.Time {
	t.Helper()
	var at time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT updated_at FROM communities WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	return at
}

// The clock cannot pass a stamp in the year 2999, so this is a clock that
// does not advance (or runs behind the row): every write must still land
// exactly one microsecond after the version it replaces.
func TestUpdatedAtStampIT_SequentialWritesIncreaseWhenTheClockDoesNot(t *testing.T) {
	st, pool := setupEngagementIT(t)
	ctx := context.Background()
	id := seedStampCommunity(t, pool)
	ahead := time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `UPDATE communities SET updated_at = $2 WHERE id = $1`, id, ahead); err != nil {
		t.Fatalf("set updated_at ahead of the clock: %v", err)
	}

	edit := func() error {
		c, err := st.GetCommunityByID(ctx, id)
		if err != nil {
			return err
		}
		c.Name += "!"
		if err := st.UpdateCommunity(ctx, c); err != nil {
			return err
		}
		if got := communityUpdatedAt(t, pool, id); !c.UpdatedAt.Equal(got) {
			t.Errorf("UpdateCommunity returned updated_at %v, row holds %v", c.UpdatedAt, got)
		}
		return nil
	}
	writes := []struct {
		name string
		run  func() error
	}{
		{"edit", edit},
		{"edit again", edit},
		{"member count +1", func() error { return st.IncrementMemberCount(ctx, id, 1) }},
		{"member count set", func() error { return st.SetMemberCount(ctx, id, 5) }},
		{"space count +1", func() error { return st.IncrementSpaceCount(ctx, id, 1) }},
		{"archive", func() error { return st.ArchiveCommunity(ctx, id) }},
		{"delete", func() error { return st.DeleteCommunity(ctx, id) }},
	}
	prev := communityUpdatedAt(t, pool, id)
	for i, w := range writes {
		if err := w.run(); err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		got := communityUpdatedAt(t, pool, id)
		want := ahead.Add(time.Duration(i+1) * time.Microsecond)
		if !got.After(prev) || !got.Equal(want) {
			t.Fatalf("%s: updated_at = %v after %v, want %v (strictly newer than the version replaced)",
				w.name, got, prev, want)
		}
		prev = got
	}
}

// The race itself. Another writer holds the row; the archive starts (its
// NOW() is fixed there) and waits on the lock; the holder then stamps the row
// with the clock and commits; the archive commits last. With
// updated_at = NOW() the archive, the newer state, would carry the older
// stamp and search would drop it as stale.
func TestUpdatedAtStampIT_ALockWaiterCommittingLastIsNewest(t *testing.T) {
	st, pool := setupEngagementIT(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id := seedStampCommunity(t, pool)

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(ctx, `SELECT 1 FROM communities WHERE id = $1 FOR UPDATE`, id); err != nil {
		t.Fatalf("lock the row: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- st.ArchiveCommunity(ctx, id) }()

	// Wait until the archive is blocked on the row lock, so its transaction
	// (and NOW()) started before the holder's stamp below.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock'
			   AND query ILIKE 'UPDATE communities SET status = ''archived''%'`).Scan(&waiting); err != nil {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the archive never waited on the row lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	var held time.Time
	if err := holder.QueryRow(ctx,
		`UPDATE communities SET updated_at = clock_timestamp() WHERE id = $1 RETURNING updated_at`,
		id).Scan(&held); err != nil {
		t.Fatalf("holder stamp: %v", err)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("holder commit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("archive: %v", err)
	}

	var status string
	var got time.Time
	if err := pool.QueryRow(ctx, `SELECT status, updated_at FROM communities WHERE id = $1`, id).Scan(&status, &got); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != "archived" || !got.After(held) {
		t.Fatalf("archive committed last with updated_at %v, not after the holder's %v (status %s): search would drop it as stale",
			got, held, status)
	}
}
