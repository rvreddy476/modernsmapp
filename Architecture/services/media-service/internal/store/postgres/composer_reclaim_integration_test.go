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

// No live reference is currently an array column.
//
// liveReferenceSQL supports = ANY(...) and TestArrayReferencesUseAnyMatching
// covers the generated SQL, but the review asked for a live array-writer race
// proof and there is no array reference to race. Asserting the absence is the
// honest form of that answer: if someone adds one, this fails, and the race
// proof above must be extended to that column's writer before it ships.
func TestNoLiveReferenceIsAnArrayColumn(t *testing.T) {
	for _, ref := range LiveMediaReferences {
		if ref.Array {
			t.Fatalf("%s.%s is an array reference. The C-CLB-1 race proof covers a "+
				"scalar non-FK writer only — extend it to this column's writer "+
				"before relying on it", ref.Table, ref.Column)
		}
	}
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
