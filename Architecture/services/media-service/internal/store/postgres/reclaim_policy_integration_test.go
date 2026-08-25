//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Slice C / C-P0-4 — "a new reference authority cannot appear silently".
//
// The previous version of this guard walked FOREIGN KEYS into `media_assets`.
// That was the wrong question: this is one shared `app` database and most
// services reference media by a plain UUID column with no constraint at all, so
// the walk could not see `users.avatar_media_id`, `channels.banner_media_id`,
// `posts.cover_media_id` or any UUID array. The guard passed while the sweep
// was blind to the references that mattered most.
//
// ResolveLiveReferences now scans by COLUMN NAME as well, and refuses when it
// finds one that reclaim_policy.go does not classify. These tests prove that
// refusal is real against a live catalog.
//
// Run with:
//
//	POSTGRES_DSN=postgres://... go test -tags integration ./internal/store/postgres/ -run Reclaim -v

func TestReclaimResolutionAcceptsTheRealSchema(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	refs, err := ResolveLiveReferences(ctx, pool)
	if err != nil {
		t.Fatalf("the live schema holds media references this policy does not "+
			"classify, so no sweep may run: %v", err)
	}
	if len(refs) == 0 {
		t.Fatal("no live reference tables resolved — the schema is not loaded, " +
			"so this guard proved nothing")
	}
	for _, r := range refs {
		t.Logf("protected: %s.%s (uuid=%v array=%v)",
			r.ref.Table, r.ref.Column, r.isUUID, r.ref.Array)
	}
}

// An unclassified media-referencing column must STOP reclamation.
//
// Simulated by creating a table with a media-shaped column that the policy does
// not know about, exactly as a future migration would.
func TestUnclassifiedReferenceRefusesReclamation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS slice_c_guard_probe (id uuid PRIMARY KEY, media_id uuid)`,
	); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS slice_c_guard_probe`)
	})

	_, err := ResolveLiveReferences(ctx, pool)
	var unclassified ErrUnclassifiedMediaReference
	if !errors.As(err, &unclassified) {
		t.Fatalf("an unclassified media_id column must refuse resolution, got %v", err)
	}

	// And the sweep's candidate scan must inherit that refusal rather than
	// falling back to a partial predicate.
	if _, err := New(pool).ListReclaimableMedia(ctx, timeZero(), 10); err == nil {
		t.Fatal("the candidate scan must refuse while an unclassified reference exists")
	}
}

// The composed predicate must actually run against the real schema.
//
// A malformed SQL string would otherwise only surface when a sweep executed in
// production, and the sweep swallows list errors as a warning.
func TestReclaimPredicateExecutes(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	refs, err := ResolveLiveReferences(ctx, pool)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var referenced bool
	err = pool.QueryRow(ctx,
		`SELECT `+liveReferenceSQL(refs, "$1"),
		"00000000-0000-0000-0000-000000000000").Scan(&referenced)
	if err != nil {
		t.Fatalf("the live-reference predicate is not valid SQL against the real schema: %v", err)
	}
	if referenced {
		t.Error("the all-zero media id should not be referenced by anything")
	}
}

func timeZero() time.Time { return time.Now().Add(-100 * 365 * 24 * time.Hour) }
