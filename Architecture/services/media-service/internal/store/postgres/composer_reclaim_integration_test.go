//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Reclamation policy against the real schema — Slice C, C-CLB-1.
//
// # WHAT CHANGED, AND WHY IT IS A RETREAT
//
// This file previously asserted the opposite of what it asserts now: that a
// CONFIRMED, composer-leased, unattached asset was reclaimable. The lease
// scoping behind that was sound as far as it went — it stopped the sweep being
// a global collector for every old ready asset — but it answered the wrong
// question. Scoping decides WHICH confirmed assets are eligible; the unsafe
// part was reclaiming a confirmed asset at all.
//
// The independent review proved it live: a reclaim transaction locked a ready,
// leased, unreferenced media row and saw no reference; a concurrent writer set
// users.avatar_media_id to that asset and committed in 304 ms, entirely
// unblocked, because a plain UUID column takes no lock on the media row; the
// reclaim then deleted and committed. Media row gone, avatar reference still
// pointing at it, no error raised anywhere.
//
// So confirmed reclamation is off. pending_upload reclamation — the original
// audit-H9 case, where the bytes never arrived and nothing can be referencing
// the row — keeps running.
func TestConfirmedAssetsAreNeverReclaimable(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := New(pool)

	uploader := uuid.New()
	old := time.Now().Add(-48 * time.Hour)

	// Abandoned in the composer: confirmed, unattached, leased. Under the old
	// policy this was the headline candidate. It must now be retained.
	abandoned := insertAsset(t, ctx, pool, uploader, "ready", "passed", "composer", old)
	// The same shape from any other surface. Never was a candidate.
	leaseless := insertAsset(t, ctx, pool, uploader, "ready", "passed", "", old)
	// Confirmed but still processing. Also finished uploading, also retained.
	processing := insertAsset(t, ctx, pool, uploader, "processing", "pending", "composer", old)
	// Never uploaded at all. Reclaimable regardless of purpose.
	stalled := insertAsset(t, ctx, pool, uploader, "pending_upload", "pending", "", old)
	// Never uploaded, composer-leased. Also reclaimable: the lease is
	// irrelevant to this class, and it is the commonest abandonment of all —
	// the picker opened, the upload started, the app closed.
	stalledLeased := insertAsset(t, ctx, pool, uploader, "pending_upload", "pending", "composer", old)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM media_assets WHERE id = ANY($1)`,
			[]uuid.UUID{abandoned, leaseless, processing, stalled, stalledLeased})
	})

	ids, err := store.ListReclaimableMedia(ctx, time.Now().Add(-1*time.Hour), 500)
	if err != nil {
		t.Fatalf("list reclaimable: %v", err)
	}
	got := asSet(ids)

	if got[abandoned] {
		t.Error("a CONFIRMED composer asset was offered for reclamation. " +
			"Confirmed deletion is disabled (C-CLB-1): a finished asset can be " +
			"claimed by writers that take no lock on the media row, so no check " +
			"inside the reclaim transaction can see the claim coming")
	}
	if got[leaseless] {
		t.Error("a confirmed leaseless asset was offered for reclamation")
	}
	if got[processing] {
		t.Error("an asset still processing was offered for reclamation; " +
			"the bytes arrived, so it is claimable and must be retained")
	}
	if !got[stalled] {
		t.Error("a pending_upload asset must stay reclaimable whatever its " +
			"purpose. The bytes never arrived, nothing can reference it, and " +
			"retaining it forever is the unbounded growth audit H9 was about")
	}
	if !got[stalledLeased] {
		t.Error("a pending_upload composer asset must be reclaimable; " +
			"the lease does not make an un-uploaded row precious")
	}
}

// The exact race the review ran, replayed against the corrected policy — the
// named proof for C-CLB-1.
//
// A non-FK scalar writer (users.avatar_media_id, updated by user-service with a
// plain UPDATE that never touches media_assets) claims the asset while the
// reclaim transaction is running. Under the old policy this ended with the
// media row deleted and the avatar reference dangling.
//
// The assertion is the review's own closure wording: no committed live
// reference may point at a deleted media row.
func TestConfirmedAssetSurvivesTheNonFKAvatarRace(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := New(pool)

	uploader := uuid.New()
	asset := insertAsset(t, ctx, pool, uploader, "ready", "passed", "composer",
		time.Now().Add(-48*time.Hour))
	owner := insertUser(t, ctx, pool)

	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `UPDATE users SET avatar_media_id = NULL WHERE id = $1`, owner)
		_, _ = pool.Exec(bg, `DELETE FROM users WHERE id = $1`, owner)
		_, _ = pool.Exec(bg, `DELETE FROM media_assets WHERE id = $1`, asset)
	})

	// The writer runs concurrently with the reclaim, exactly as in the review's
	// reproduction. It is deliberately NOT synchronised to lose: the point is
	// that it never had to wait, and correctness no longer depends on whether
	// it does.
	var wg sync.WaitGroup
	wg.Add(1)
	started := time.Now()
	var writeErr error
	var writeTook time.Duration
	go func() {
		defer wg.Done()
		_, writeErr = pool.Exec(context.Background(),
			`UPDATE users SET avatar_media_id = $1 WHERE id = $2`, asset, owner)
		writeTook = time.Since(started)
	}()

	_, reclaimErr := store.DeleteOrphanMediaAtomic(ctx, asset, time.Hour)
	wg.Wait()

	if writeErr != nil {
		t.Fatalf("avatar write failed: %v", writeErr)
	}
	if !errors.Is(reclaimErr, ErrMediaConfirmed) {
		t.Fatalf("reclaim of a CONFIRMED asset must be refused with "+
			"ErrMediaConfirmed, got %v. That refusal is what removes the race "+
			"entirely rather than trying to win it", reclaimErr)
	}

	// The asset survived.
	var assets int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM media_assets WHERE id = $1`, asset).Scan(&assets); err != nil {
		t.Fatalf("count assets: %v", err)
	}
	// The reference committed and points at a row that still exists.
	var dangling int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM users u
		WHERE u.id = $1
		  AND u.avatar_media_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM media_assets m WHERE m.id = u.avatar_media_id)`,
		owner).Scan(&dangling); err != nil {
		t.Fatalf("count dangling: %v", err)
	}

	if assets != 1 {
		t.Errorf("the media row was deleted while a live reference was being "+
			"written (assets=%d). This is the data-loss defect C-CLB-1 closes", assets)
	}
	if dangling != 0 {
		t.Errorf("a committed live reference points at a deleted media row "+
			"(dangling=%d)", dangling)
	}
	t.Logf("non-FK avatar writer committed in %s, unblocked; reclaim refused with %v; "+
		"assets=%d dangling=%d", writeTook, reclaimErr, assets, dangling)
}

// A pending_upload asset is still genuinely deletable end to end.
//
// The retreat above must not have quietly disabled the whole sweeper. If this
// fails, storage grows without bound and nothing else in this file would notice.
func TestPendingUploadAssetIsStillReclaimable(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := New(pool)

	asset := insertAsset(t, ctx, pool, uuid.New(), "pending_upload", "pending", "composer",
		time.Now().Add(-48*time.Hour))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_assets WHERE id = $1`, asset)
	})

	keys, err := store.DeleteOrphanMediaAtomic(ctx, asset, time.Hour)
	if err != nil {
		t.Fatalf("a pending_upload asset must still be reclaimable: %v", err)
	}
	if len(keys) == 0 {
		t.Error("expected the storage key to be returned for blob reclamation")
	}

	var remaining int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM media_assets WHERE id = $1`, asset).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Error("the asset was not deleted; audit-H9 reclamation is broken")
	}
}

// The C-CLB-1 race proof, extended to the array writer: channel-service
// writing channel_updates.media_ids (UUID[]).
//
// This used to be TestNoLiveReferenceIsAnArrayColumn, which asserted that no
// array reference existed because the proof above covered a scalar writer
// only. channel_updates.media_ids was classified on 2026-09-27 (the boot
// sweep refused on it), so that assertion went red. Every array reference
// now has to be registered in arrayReferenceRaceProofs with a proof of this
// shape; TestNoArrayReferenceWithoutARaceProof enforces that without a
// database.
//
// The array writer is worse placed than the avatar writer, not better: an
// array cannot carry a foreign key at all, and channel-service's
// CreateUpdate/UpdateUpdate write whatever ids they are given in one plain
// INSERT/UPDATE that never touches media_assets. So the writer here is held
// open, UNCOMMITTED, for the whole reclaim — the worst interleaving, made
// deterministic instead of left to scheduling. Both channel-service shapes
// run: a new update INSERTed with the id inside a multi-element array, and an
// existing update whose whole array is replaced (UpdateUpdate's `media_ids =
// $5`).
//
// The reclaim must refuse on the asset's own status, without waiting on the
// writer (it holds no lock the writer needs) and without seeing it (it is
// uncommitted). Then the writers commit, and no committed array element may
// name a deleted media row.
func TestConfirmedAssetSurvivesTheChannelUpdatesArrayRace(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := New(pool)
	ensureChannelUpdatesFixture(t, ctx, pool)

	asset := insertAsset(t, ctx, pool, uuid.New(), "ready", "passed", "composer",
		time.Now().Add(-48*time.Hour))
	sibling := uuid.New() // another element, so ANY() has to look past index 1
	channel, author := uuid.New(), uuid.New()
	inserted, edited := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO channel_updates (id, channel_id, author_id, media_ids, status)
		VALUES ($1, $2, $3, '{}', 'published')`, edited, channel, author); err != nil {
		t.Fatalf("seed the update the editor will rewrite: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM channel_updates WHERE id = ANY($1)`, []uuid.UUID{inserted, edited})
		_, _ = pool.Exec(bg, `DELETE FROM media_assets WHERE id = $1`, asset)
	})

	// Both writers open and write, and stay uncommitted.
	creator, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin creator: %v", err)
	}
	defer creator.Rollback(ctx)
	if _, err := creator.Exec(ctx, `
		INSERT INTO channel_updates (id, channel_id, author_id, media_ids, status)
		VALUES ($1, $2, $3, $4, 'published')`,
		inserted, channel, author, []uuid.UUID{sibling, asset}); err != nil {
		t.Fatalf("CreateUpdate-shaped write: %v", err)
	}
	editor, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin editor: %v", err)
	}
	defer editor.Rollback(ctx)
	if _, err := editor.Exec(ctx, `
		UPDATE channel_updates SET media_ids = $2, updated_at = NOW() WHERE id = $1`,
		edited, []uuid.UUID{asset, sibling}); err != nil {
		t.Fatalf("UpdateUpdate-shaped write: %v", err)
	}

	// The reclaim runs in the middle. A bounded context turns "the reclaim
	// waited on the writer" into a failure instead of a hung suite.
	reclaimCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, reclaimErr := store.DeleteOrphanMediaAtomic(reclaimCtx, asset, time.Hour)

	if err := creator.Commit(ctx); err != nil {
		t.Fatalf("creator commit: %v", err)
	}
	if err := editor.Commit(ctx); err != nil {
		t.Fatalf("editor commit: %v", err)
	}

	if !errors.Is(reclaimErr, ErrMediaConfirmed) {
		t.Fatalf("reclaim of a CONFIRMED asset under an in-flight channel_updates.media_ids "+
			"write must be refused with ErrMediaConfirmed, got %v. The writer is uncommitted "+
			"and takes no lock on the media row; the status refusal is the only thing "+
			"that can protect it", reclaimErr)
	}

	var assets int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM media_assets WHERE id = $1`, asset).Scan(&assets); err != nil {
		t.Fatalf("count assets: %v", err)
	}
	if assets != 1 {
		t.Errorf("the media row was deleted while channel_updates.media_ids was being "+
			"written (assets=%d). This is the data-loss defect C-CLB-1 closes", assets)
	}

	// Every committed array element, in both rows, points at a row that exists.
	var holders, dangling int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE m.id = $2),
		       count(*) FILTER (WHERE m.id IS NULL AND e.id = $2)
		FROM channel_updates cu
		CROSS JOIN LATERAL unnest(cu.media_ids) AS e(id)
		LEFT JOIN media_assets m ON m.id = e.id
		WHERE cu.id = ANY($1)`,
		[]uuid.UUID{inserted, edited}, asset).Scan(&holders, &dangling); err != nil {
		t.Fatalf("count array references: %v", err)
	}
	if holders != 2 {
		t.Errorf("both channel updates must have committed a live reference to the asset, got %d", holders)
	}
	if dangling != 0 {
		t.Errorf("a committed channel_updates.media_ids element points at a deleted media "+
			"row (dangling=%d)", dangling)
	}
	t.Logf("two uncommitted channel_updates.media_ids writers in flight; reclaim refused with %v; "+
		"assets=%d holders=%d dangling=%d", reclaimErr, assets, holders, dangling)
}

// The live-reference half of the protocol for the array column.
//
// The status refusal covers confirmed assets. A pending_upload asset is the
// one state the reclaim may delete, so for it the reference check is what
// protects a committed claim — and for an array that check is `$1 =
// ANY(channel_updates.media_ids)`, which TestBootRefusedColumnsAreClassified-
// AsLiveClaims only proves as a string. This runs it against a real UUID[]
// column with the id at a non-first position, through both the candidate scan
// and the atomic delete, and then shows the refusal is the reference's doing:
// with the element removed the same asset is reclaimed.
func TestPendingUploadHeldByChannelUpdateArrayIsNotReclaimed(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := New(pool)
	ensureChannelUpdatesFixture(t, ctx, pool)

	asset := insertAsset(t, ctx, pool, uuid.New(), "pending_upload", "pending", "composer",
		time.Now().Add(-48*time.Hour))
	update := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO channel_updates (id, channel_id, author_id, media_ids, status)
		VALUES ($1, $2, $3, $4, 'published')`,
		update, uuid.New(), uuid.New(), []uuid.UUID{uuid.New(), uuid.New(), asset}); err != nil {
		t.Fatalf("seed channel update: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM channel_updates WHERE id = $1`, update)
		_, _ = pool.Exec(bg, `DELETE FROM media_assets WHERE id = $1`, asset)
	})

	ids, err := store.ListReclaimableMedia(ctx, time.Now().Add(-1*time.Hour), 500)
	if err != nil {
		t.Fatalf("list reclaimable: %v", err)
	}
	if asSet(ids)[asset] {
		t.Error("the candidate scan offered an asset that channel_updates.media_ids holds")
	}
	if _, err := store.DeleteOrphanMediaAtomic(ctx, asset, time.Hour); !errors.Is(err, ErrMediaStillReferenced) {
		t.Fatalf("an asset held by channel_updates.media_ids must be refused with "+
			"ErrMediaStillReferenced, got %v", err)
	}

	// Drop the element: the same asset is now reclaimable, so the refusal
	// above came from the array match and not from something blanket.
	if _, err := pool.Exec(ctx, `
		UPDATE channel_updates SET media_ids = array_remove(media_ids, $2) WHERE id = $1`,
		update, asset); err != nil {
		t.Fatalf("remove the array element: %v", err)
	}
	if _, err := store.DeleteOrphanMediaAtomic(ctx, asset, time.Hour); err != nil {
		t.Fatalf("with no array element left the pending_upload asset must be reclaimed: %v", err)
	}
}

// ensureChannelUpdatesFixture creates channel_updates for the duration of a
// test.
//
// The media-service CI database does not load channel-service's schema (see
// scripts/ci/integration-services.yaml: adding a service's schema can trip
// the reclaim guard). The fixture has the real column the reclaim reads —
// media_ids UUID[] NOT NULL — plus the NOT NULL columns channel-service's
// store writes beside it, minus the broadcast_channels foreign key that would
// drag that service's schema in. If the real table is ever loaded here, this
// fails loudly rather than racing two shapes of the same table.
func ensureChannelUpdatesFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT to_regclass('public.channel_updates') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("look up channel_updates: %v", err)
	}
	if exists {
		t.Fatal("channel_updates already exists in this database; this fixture creates " +
			"its own shape — insert a broadcast_channels parent and use the real table instead")
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE channel_updates (
			id         UUID PRIMARY KEY,
			channel_id UUID NOT NULL,
			author_id  UUID NOT NULL,
			media_ids  UUID[] NOT NULL DEFAULT '{}',
			status     TEXT NOT NULL DEFAULT 'draft',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		t.Fatalf("create channel_updates fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS channel_updates`)
	})
}

func insertUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, display_name, created_at, updated_at)
		VALUES ($1, $2, 'reclaim race fixture', NOW(), NOW())`,
		id, "reclaim_race_"+id.String()[:8]); err != nil {
		t.Fatalf("insert user fixture: %v", err)
	}
	return id
}

func insertAsset(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	uploader uuid.UUID,
	processing, moderation, purpose string,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var leased any
	if purpose != "" {
		leased = purpose
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_assets
			(id, uploader_id, file_type, media_subtype, mime_type, file_size_bytes,
			 storage_bucket, storage_key, processing_status, moderation_status,
			 upload_purpose, created_at, updated_at)
		VALUES ($1::uuid, $2, 'image', 'general', 'image/jpeg', 1,
			 'media', $7, $3, $4, $5, $6, $6)`,
		id, uploader, processing, moderation, leased, createdAt, "fixture/"+id.String(),
	); err != nil {
		t.Fatalf("insert media fixture: %v", err)
	}
	return id
}

func asSet(ids []uuid.UUID) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}
