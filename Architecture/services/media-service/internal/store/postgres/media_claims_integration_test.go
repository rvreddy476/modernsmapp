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

// Cross-service media claims (migration 031) against real PostgreSQL. CI only:
// POSTGRES_DSN on a database whose name ends in _test.

func claimsPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := subtitleEventsPool(t) // bootstraps setup.sql + migrations
	requireScratchDatabase(t, pool)
	return pool
}

func countClaims(t *testing.T, pool *pgxpool.Pool, mediaID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM media_claims WHERE media_id = $1`, mediaID).Scan(&n); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	return n
}

func assetExists(t *testing.T, pool *pgxpool.Pool, mediaID uuid.UUID) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM media_assets WHERE id = $1`, mediaID).Scan(&n); err != nil {
		t.Fatalf("count assets: %v", err)
	}
	return n == 1
}

func cleanupClaimed(t *testing.T, pool *pgxpool.Pool, ids ...uuid.UUID) {
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM media_claims WHERE media_id = ANY($1)`, ids)
		_, _ = pool.Exec(bg, `DELETE FROM media_blob_reclaim WHERE media_id = ANY($1)`, ids)
		_, _ = pool.Exec(bg, `DELETE FROM media_assets WHERE id = ANY($1)`, ids)
	})
}

func TestMediaClaimsForeignKeyIsRestrictive(t *testing.T) {
	pool := claimsPool(t)
	var action string
	if err := pool.QueryRow(context.Background(), `
		SELECT confdeltype::text FROM pg_constraint
		WHERE conrelid = 'media_claims'::regclass
		  AND confrelid = 'media_assets'::regclass AND contype = 'f'`).Scan(&action); err != nil {
		t.Fatalf("load media_claims FK: %v", err)
	}
	if action != "r" && action != "a" {
		t.Fatalf("media_claims FK must be RESTRICT/NO ACTION (the claim/delete serialisation rests on it), got %q", action)
	}
}

// Claim → owner delete refused with the holders → release → delete succeeds.
// Claiming is idempotent and all-or-nothing.
func TestOwnerDeleteRefusedWhileClaimedThenAllowedAfterRelease(t *testing.T) {
	pool := claimsPool(t)
	ctx := context.Background()
	store := New(pool)
	owner := uuid.New()
	asset := insertAsset(t, ctx, pool, owner, "ready", "passed", "", time.Now())
	cleanupClaimed(t, pool, asset)

	update := MediaClaim{Claimant: "channel-service", Kind: "channel_update", Ref: uuid.NewString()}
	avatar := MediaClaim{Claimant: "channel-service", Kind: "channel_avatar", Ref: uuid.NewString()}
	for i := 0; i < 2; i++ {
		if err := store.ClaimMedia(ctx, update, []uuid.UUID{asset, asset}); err != nil {
			t.Fatalf("claim #%d: %v", i, err)
		}
	}
	if err := store.ClaimMedia(ctx, avatar, []uuid.UUID{asset}); err != nil {
		t.Fatalf("avatar claim: %v", err)
	}
	if n := countClaims(t, pool, asset); n != 2 {
		t.Fatalf("idempotent claims: want 2 rows (one per holder), got %d", n)
	}

	_, err := store.DeleteMedia(ctx, asset)
	var inUse *MediaInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("owner delete of a claimed asset must be refused with MediaInUseError, got %v", err)
	}
	if len(inUse.Holders) != 2 || inUse.Holders[0].Kind != "channel_avatar" || inUse.Holders[1].Kind != "channel_update" {
		t.Fatalf("holders %+v", inUse.Holders)
	}
	if !assetExists(t, pool, asset) {
		t.Fatal("a refused delete removed the asset")
	}

	if n, err := store.ReleaseMediaClaims(ctx, update, nil); err != nil || n != 1 {
		t.Fatalf("release update: n=%d err=%v", n, err)
	}
	if n, err := store.ReleaseMediaClaims(ctx, update, nil); err != nil || n != 0 {
		t.Fatalf("a repeated release must succeed with nothing to do: n=%d err=%v", n, err)
	}
	if _, err := store.DeleteMedia(ctx, asset); !errors.Is(err, ErrMediaInUse) {
		t.Fatalf("the avatar claim still holds it, got %v", err)
	}
	if n, err := store.ReleaseMediaClaims(ctx, avatar, []uuid.UUID{asset}); err != nil || n != 1 {
		t.Fatalf("release avatar: n=%d err=%v", n, err)
	}
	if _, err := store.DeleteMedia(ctx, asset); err != nil {
		t.Fatalf("with no claim left the owner delete must succeed: %v", err)
	}
	if assetExists(t, pool, asset) {
		t.Fatal("the asset survived its delete")
	}
}

func TestClaimIsAllOrNothingAndRefusesMissingMedia(t *testing.T) {
	pool := claimsPool(t)
	ctx := context.Background()
	store := New(pool)
	live := insertAsset(t, ctx, pool, uuid.New(), "ready", "passed", "", time.Now())
	cleanupClaimed(t, pool, live)
	c := MediaClaim{Claimant: "channel-service", Kind: "channel_update", Ref: uuid.NewString()}

	// The missing id sorts either side of the live one across runs; both
	// orders must refuse before anything is written.
	for i := 0; i < 4; i++ {
		if err := store.ClaimMedia(ctx, c, []uuid.UUID{live, uuid.New()}); !errors.Is(err, ErrMediaNotFound) {
			t.Fatalf("a missing id must refuse the claim, got %v", err)
		}
	}
	if n := countClaims(t, pool, live); n != 0 {
		t.Fatalf("a refused claim recorded %d row(s) for its valid sibling", n)
	}
}

// The race the protocol exists for, run many times: an attach (claim) and the
// owner's delete of the same asset start together. Exactly one may win, and
// no committed claim may name a deleted row.
func TestOwnerDeleteAndClaimRaceNeverDangle(t *testing.T) {
	pool := claimsPool(t)
	ctx := context.Background()
	store := New(pool)

	var claimWins, deleteWins int
	for i := 0; i < 40; i++ {
		asset := insertAsset(t, ctx, pool, uuid.New(), "ready", "passed", "", time.Now())
		cleanupClaimed(t, pool, asset)
		c := MediaClaim{Claimant: "channel-service", Kind: "channel_update", Ref: uuid.NewString()}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var claimErr, deleteErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			claimErr = store.ClaimMedia(ctx, c, []uuid.UUID{asset})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, deleteErr = store.DeleteMedia(ctx, asset)
		}()
		close(start)
		wg.Wait()

		exists, claims := assetExists(t, pool, asset), countClaims(t, pool, asset)
		switch {
		case claimErr == nil && deleteErr == nil:
			t.Fatalf("iteration %d: the claim and the delete both succeeded", i)
		case claims > 0 && !exists:
			t.Fatalf("iteration %d: a committed claim names a deleted asset (dangling)", i)
		case claimErr == nil:
			if !exists || claims != 1 || !errors.Is(deleteErr, ErrMediaInUse) {
				t.Fatalf("iteration %d: claim won but exists=%v claims=%d deleteErr=%v", i, exists, claims, deleteErr)
			}
			claimWins++
		case deleteErr == nil:
			if exists || claims != 0 || !errors.Is(claimErr, ErrMediaNotFound) {
				t.Fatalf("iteration %d: delete won but exists=%v claims=%d claimErr=%v", i, exists, claims, claimErr)
			}
			deleteWins++
		default:
			t.Fatalf("iteration %d: both refused (claim=%v delete=%v)", i, claimErr, deleteErr)
		}
	}
	t.Logf("40 races: claim won %d, delete won %d, dangling 0", claimWins, deleteWins)
}

// A claim also holds off the sweeper and the post purge.
func TestClaimHoldsOffReclaimAndReferrerPurge(t *testing.T) {
	pool := claimsPool(t)
	ctx := context.Background()
	store := New(pool)
	stalled := insertAsset(t, ctx, pool, uuid.New(), "pending_upload", "pending", "composer",
		time.Now().Add(-48*time.Hour))
	ready := insertAsset(t, ctx, pool, uuid.New(), "ready", "passed", "", time.Now())
	cleanupClaimed(t, pool, stalled, ready)
	c := MediaClaim{Claimant: "channel-service", Kind: "channel_update", Ref: uuid.NewString()}
	if err := store.ClaimMedia(ctx, c, []uuid.UUID{stalled, ready}); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if _, err := store.DeleteOrphanMediaAtomic(ctx, stalled, time.Hour); !errors.Is(err, ErrMediaStillReferenced) {
		t.Fatalf("a claimed pending_upload asset must not be reclaimed, got %v", err)
	}
	if _, err := store.DeleteAssetForReferrer(ctx, ready, "post", uuid.New()); !errors.Is(err, ErrMediaStillReferenced) {
		t.Fatalf("the post purge must refuse a claimed asset, got %v", err)
	}

	if _, err := store.ReleaseMediaClaims(ctx, c, nil); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := store.DeleteOrphanMediaAtomic(ctx, stalled, time.Hour); err != nil {
		t.Fatalf("released, the pending_upload asset must be reclaimable again: %v", err)
	}
}

// An account erasure is never blocked by another service's claim.
func TestPurgeUserRemovesClaimsOnTheUsersMedia(t *testing.T) {
	pool := claimsPool(t)
	ctx := context.Background()
	store := New(pool)
	user := uuid.New()
	asset := insertAsset(t, ctx, pool, user, "ready", "passed", "", time.Now())
	cleanupClaimed(t, pool, asset)
	if err := store.ClaimMedia(ctx, MediaClaim{Claimant: "channel-service", Kind: "channel_banner", Ref: uuid.NewString()},
		[]uuid.UUID{asset}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.PurgeUser(ctx, user); err != nil {
		t.Fatalf("purge of a user with claimed media: %v", err)
	}
	if assetExists(t, pool, asset) || countClaims(t, pool, asset) != 0 {
		t.Fatal("purge left the asset or its claims")
	}
}

func TestReleaseExceptKeepsExactlyTheSet(t *testing.T) {
	pool := claimsPool(t)
	ctx := context.Background()
	store := New(pool)
	keep := insertAsset(t, ctx, pool, uuid.New(), "ready", "passed", "", time.Now())
	stale := insertAsset(t, ctx, pool, uuid.New(), "ready", "passed", "", time.Now())
	cleanupClaimed(t, pool, keep, stale)
	c := MediaClaim{Claimant: "channel-service", Kind: "channel_update", Ref: uuid.NewString()}
	other := MediaClaim{Claimant: "channel-service", Kind: "channel_update", Ref: uuid.NewString()}
	if err := store.ClaimMedia(ctx, c, []uuid.UUID{keep, stale}); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimMedia(ctx, other, []uuid.UUID{stale}); err != nil {
		t.Fatal(err)
	}
	if n, err := store.ReleaseMediaClaimsExcept(ctx, c, []uuid.UUID{keep}); err != nil || n != 1 {
		t.Fatalf("release except: n=%d err=%v", n, err)
	}
	if countClaims(t, pool, keep) != 1 || countClaims(t, pool, stale) != 1 {
		t.Fatal("release-except must drop only this holder's other claims")
	}
}
