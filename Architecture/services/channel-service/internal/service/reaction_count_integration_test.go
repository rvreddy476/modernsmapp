// DB-backed tests pinning what channel_updates.reaction_count means.
//
// It means ONE thing: how many emoji reactions an update has, counted from
// update_reactions. It used to have two writers that disagreed —
// syncReactionCount recomputed it from update_reactions, while
// SparkUpdate/UnsparkUpdate added and subtracted a spark weight (1, or 5
// for a supernova) — so sparking inflated the number and the next emoji
// reaction silently clobbered the inflation away. Sparks are not a channel
// feature (an update has an emoji reaction and a share), so they no longer
// touch the column at all.
//
// Requires TEST_PG_DSN on a "_test" database (channel_it_test); skipped
// when unset. Every test seeds its own channel, so the suite never depends
// on — or disturbs — rows another suite left behind.
//
// Run the DB-backed suites one package at a time: `go test -p 1 ./...`.
// internal/http's admin rig resets itself with `TRUNCATE broadcast_channels
// CASCADE`, which deletes these seeds out from under a concurrent package
// (the symptom is a channel_updates foreign-key violation, not a real
// failure of the code under test).
package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/channel-service/database"
	"github.com/atpost/channel-service/internal/store"
	pgstore "github.com/atpost/channel-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupReactionCountIT(t *testing.T) (*Service, *store.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping channel-service reaction count integration tests")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	st := store.New(pool)
	return New(st, nil), st, pool
}

// seedReactionChannel makes an active public channel so an anonymous viewer
// passes authorizeViewer.
func seedReactionChannel(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	handle := "rc" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO broadcast_channels (owner_id, handle, name, channel_type, status)
		VALUES ($1, $2, 'reaction count channel', 'public', 'active') RETURNING id`,
		uuid.New(), handle).Scan(&id); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return id
}

func seedReactionUpdate(t *testing.T, pool *pgxpool.Pool, channelID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO channel_updates (channel_id, author_id, body, status, published_at)
		VALUES ($1, $2, 'update', 'published', $3) RETURNING id`,
		channelID, uuid.New(), time.Now().Add(-time.Minute)).Scan(&id); err != nil {
		t.Fatalf("seed update: %v", err)
	}
	return id
}

func reactionCount(t *testing.T, pool *pgxpool.Pool, updateID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT reaction_count FROM channel_updates WHERE id = $1`, updateID).Scan(&n); err != nil {
		t.Fatalf("read reaction_count: %v", err)
	}
	return n
}

// The exact defect, start to finish: sparks must never move the column, and
// an emoji reaction landing afterwards must not find an inflated number to
// clobber. Before the fix the count read 3 after the sparks and then
// collapsed to 1 on the reaction.
func TestReactionCountIT_SparksNeverMoveItAndReactionsAreNotClobbered(t *testing.T) {
	svc, st, pool := setupReactionCountIT(t)
	ctx := context.Background()
	channelID := seedReactionChannel(t, pool)
	updateID := seedReactionUpdate(t, pool, channelID)

	// Three people spark. The column must not budge.
	for i := 0; i < 3; i++ {
		if err := st.SparkUpdate(ctx, updateID, uuid.New(), false); err != nil {
			t.Fatalf("spark %d: %v", i, err)
		}
	}
	if got := reactionCount(t, pool, updateID); got != 0 {
		t.Fatalf("after 3 sparks reaction_count = %d, want 0: sparks must not touch the column", got)
	}

	// A supernova spark weighed 5 under the old code — the worst case.
	if err := st.SparkUpdate(ctx, updateID, uuid.New(), true); err != nil {
		t.Fatalf("supernova spark: %v", err)
	}
	if got := reactionCount(t, pool, updateID); got != 0 {
		t.Fatalf("after a supernova spark reaction_count = %d, want 0", got)
	}

	// Now one emoji reaction. It is the only thing the column counts.
	reactor := uuid.New()
	if _, err := svc.ReactToUpdate(ctx, channelID, updateID, reactor, "👍"); err != nil {
		t.Fatalf("react: %v", err)
	}
	if got := reactionCount(t, pool, updateID); got != 1 {
		t.Fatalf("after 1 reaction reaction_count = %d, want 1", got)
	}

	// A second reactor, and a third who replaces their own emoji rather
	// than adding a second — one reaction per viewer.
	second := uuid.New()
	if _, err := svc.ReactToUpdate(ctx, channelID, updateID, second, "🔥"); err != nil {
		t.Fatalf("second react: %v", err)
	}
	if _, err := svc.ReactToUpdate(ctx, channelID, updateID, second, "❤️"); err != nil {
		t.Fatalf("replace react: %v", err)
	}
	if got := reactionCount(t, pool, updateID); got != 2 {
		t.Fatalf("after 2 reactors (one having changed emoji) reaction_count = %d, want 2", got)
	}

	// Unsparking must not drag the column down either.
	if got := reactionCount(t, pool, updateID); got != 2 {
		t.Fatalf("precondition: reaction_count = %d, want 2", got)
	}

	// Removing a reaction is the only thing that decrements it.
	if _, err := svc.UnreactToUpdate(ctx, channelID, updateID, reactor); err != nil {
		t.Fatalf("unreact: %v", err)
	}
	if got := reactionCount(t, pool, updateID); got != 1 {
		t.Fatalf("after removing one reaction reaction_count = %d, want 1", got)
	}
}

// Unsparking used to subtract a weight from the column; it must now leave
// it exactly where the reactions put it.
func TestReactionCountIT_UnsparkLeavesReactionsAlone(t *testing.T) {
	svc, st, pool := setupReactionCountIT(t)
	ctx := context.Background()
	channelID := seedReactionChannel(t, pool)
	updateID := seedReactionUpdate(t, pool, channelID)

	if _, err := svc.ReactToUpdate(ctx, channelID, updateID, uuid.New(), "👍"); err != nil {
		t.Fatalf("react: %v", err)
	}
	sparker := uuid.New()
	if err := st.SparkUpdate(ctx, updateID, sparker, true); err != nil {
		t.Fatalf("spark: %v", err)
	}
	if err := st.UnsparkUpdate(ctx, updateID, sparker); err != nil {
		t.Fatalf("unspark: %v", err)
	}

	if got := reactionCount(t, pool, updateID); got != 1 {
		t.Fatalf("after spark+unspark around one reaction, reaction_count = %d, want 1", got)
	}
}

// The repair script must fix a row corrupted the way the old code corrupted
// them, be safe to run twice, and touch nothing else. It is run here as the
// exact text an operator runs, from the embedded file.
func TestReactionCountIT_BackfillRepairsAndIsIdempotent(t *testing.T) {
	svc, _, pool := setupReactionCountIT(t)
	ctx := context.Background()
	channelID := seedReactionChannel(t, pool)
	damaged := seedReactionUpdate(t, pool, channelID)
	untouched := seedReactionUpdate(t, pool, channelID)

	if _, err := svc.ReactToUpdate(ctx, channelID, damaged, uuid.New(), "👍"); err != nil {
		t.Fatalf("react: %v", err)
	}
	if _, err := svc.ReactToUpdate(ctx, channelID, untouched, uuid.New(), "🔥"); err != nil {
		t.Fatalf("react on control: %v", err)
	}

	// Reproduce the corruption the old SparkUpdate caused: a supernova
	// inflating the column by 5 over the true reaction count.
	if _, err := pool.Exec(ctx,
		`UPDATE channel_updates SET reaction_count = reaction_count + 5 WHERE id = $1`, damaged); err != nil {
		t.Fatalf("simulate corruption: %v", err)
	}
	if got := reactionCount(t, pool, damaged); got != 6 {
		t.Fatalf("precondition: corrupted count = %d, want 6", got)
	}

	if _, err := pool.Exec(ctx, database.BackfillReactionCountSQL); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := reactionCount(t, pool, damaged); got != 1 {
		t.Fatalf("after backfill reaction_count = %d, want 1", got)
	}
	if got := reactionCount(t, pool, untouched); got != 1 {
		t.Fatalf("backfill disturbed an uncorrupted row: %d, want 1", got)
	}

	// Re-running must land on the same value, not drift.
	if _, err := pool.Exec(ctx, database.BackfillReactionCountSQL); err != nil {
		t.Fatalf("backfill rerun: %v", err)
	}
	if got := reactionCount(t, pool, damaged); got != 1 {
		t.Fatalf("after a second backfill reaction_count = %d, want 1", got)
	}
}
