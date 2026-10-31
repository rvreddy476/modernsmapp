// Package claimsync reconciles media-service's claims with the media ids
// post-service's non-FK columns actually hold (cmd/media-claims).
//
// # Why it exists
//
// The claim protocol (internal/service/media_claims.go) is live only for
// writes made after it shipped, and its releases are best effort:
//
//   - BACKFILL: every post cover, reel draft, event cover, composer draft
//     cover, video series cover and playlist cover written before its kind
//     was claimed names media with no claim, so its owner can still delete
//     it out from under the row;
//   - REPAIR: a release that failed (media-service down after the write)
//     leaves a stale claim, which refuses the owner a delete they are
//     entitled to — and blocks the post purge from deleting the asset;
//   - ORPHANS: a row that is gone (a purged post whose release was lost, an
//     erased account's drafts and events) cannot be found by paging
//     post-service's tables. The orphan pass reads media_claims itself
//     (posts and media share the app database) for the refs post-service
//     holds claims under, and releases those whose row is gone.
//
// # What one pass does, per holder row
//
//   - a LIVE row with media: claim its current set (idempotent), then release
//     anything else the row holds. Claim first: releasing first would leave a
//     moment where the row names an unclaimed asset. An id media-service no
//     longer has is reported as DANGLING (already broken before the pass; an
//     operator decides whether to clear it) and the row's other ids are still
//     claimed one by one.
//   - a DEAD row (deleted or published reel draft) or a gone one: release all.
//
// Applying, each row is locked (the same per-row lock the writers take) and
// RE-READ first: the page was read earlier, and acting on a stale set would
// release a claim an edit made since relies on.
//
// Dry run by default: nothing is sent, the report says what would be.
package claimsync

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/post-service/internal/mediaclaims"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Claims is the media-service claim API (mediaclaims.Client).
type Claims interface {
	Claim(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error
	Release(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error
	ReleaseExcept(ctx context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error
}

// Holders serialises with the writers and re-reads a row under that lock
// (*postgres.Store).
type Holders interface {
	LockMediaHolder(ctx context.Context, kind string, ref uuid.UUID) (func(), error)
	MediaHeldBy(ctx context.Context, kind string, ref uuid.UUID) (postgres.MediaHolding, error)
}

// Source pages through the holder tables and media_claims (*postgres.Store).
type Source interface {
	Holders
	MediaHoldingPage(ctx context.Context, kind string, after uuid.UUID, limit int) ([]postgres.MediaHolding, error)
	ClaimedRefsPage(ctx context.Context, claimant string, after postgres.ClaimCursor, limit int) ([]postgres.ClaimedRef, postgres.ClaimCursor, bool, error)
}

// Dangling is a row naming an asset media-service does not have.
type Dangling struct {
	Kind    string
	Ref     uuid.UUID
	MediaID uuid.UUID
}

// Report is the outcome of one pass.
type Report struct {
	Rows     int // holder rows looked at (both passes)
	Claimed  int // live rows whose set was (or would be) claimed
	Released int // dead or gone rows whose claims were (or would be) released
	Orphans  int // of Released, rows found only through media_claims
	Dangling []Dangling
	Failed   int // rows media-service or the database could not answer for; re-run
	// OrphanScan says whether the orphan pass ran ("done") or why not.
	OrphanScan string
}

func (r Report) String() string {
	return fmt.Sprintf("rows=%d claimed=%d released=%d orphans=%d dangling=%d failed=%d orphan_scan=%s",
		r.Rows, r.Claimed, r.Released, r.Orphans, len(r.Dangling), r.Failed, r.OrphanScan)
}

// PageSize is how many rows one page reads.
const PageSize = 500

// Run reconciles every holder row, then the orphans. A read failure of a
// holder table stops the pass (it is safe to re-run from the start); a claim
// failure is counted and the pass goes on. With apply false nothing is sent.
func Run(ctx context.Context, src Source, c Claims, apply bool) (Report, error) {
	var rep Report
	for _, kind := range mediaclaims.Kinds {
		after := uuid.Nil
		for {
			rows, err := src.MediaHoldingPage(ctx, kind, after, PageSize)
			if err != nil {
				return rep, fmt.Errorf("read %s rows: %w", kind, err)
			}
			Reconcile(ctx, src, c, rows, apply, &rep)
			if len(rows) < PageSize {
				break
			}
			after = rows[len(rows)-1].Ref
		}
	}
	if err := reconcileOrphans(ctx, src, c, apply, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// Reconcile applies one pass over rows.
func Reconcile(ctx context.Context, h Holders, c Claims, rows []postgres.MediaHolding, apply bool, rep *Report) {
	for _, row := range rows {
		rep.Rows++
		if apply {
			reconcileLocked(ctx, h, c, row.Kind, row.Ref, rep)
			continue
		}
		if holdsNothing(row) {
			rep.Released++
		} else {
			rep.Claimed++
		}
	}
}

// reconcileOrphans releases claims whose holder row is gone. A row that is
// still there was reconciled by the table pass and is left alone.
func reconcileOrphans(ctx context.Context, src Source, c Claims, apply bool, rep *Report) error {
	cursor := postgres.ClaimCursor{}
	for {
		refs, next, more, err := src.ClaimedRefsPage(ctx, mediaclaims.Claimant, cursor, PageSize)
		if errors.Is(err, postgres.ErrClaimsTableAbsent) {
			rep.OrphanScan = "skipped: media_claims is not visible from this database"
			return nil
		}
		if err != nil {
			return fmt.Errorf("read media_claims: %w", err)
		}
		for _, ref := range refs {
			if !knownKind(ref.Kind) {
				rep.Failed++ // a kind this service never wrote: an operator looks
				continue
			}
			current, err := src.MediaHeldBy(ctx, ref.Kind, ref.Ref)
			if err != nil {
				rep.Failed++
				continue
			}
			if !holdsNothing(current) {
				continue // live: the table pass owns it
			}
			rep.Rows++
			rep.Orphans++
			if !apply {
				rep.Released++
				continue
			}
			reconcileLocked(ctx, src, c, ref.Kind, ref.Ref, rep)
		}
		if !more {
			rep.OrphanScan = "done"
			return nil
		}
		cursor = next
	}
}

func reconcileLocked(ctx context.Context, h Holders, c Claims, kind string, ref uuid.UUID, rep *Report) {
	unlock, err := h.LockMediaHolder(ctx, kind, ref)
	if err != nil {
		rep.Failed++
		return
	}
	defer unlock()
	current, err := h.MediaHeldBy(ctx, kind, ref)
	if err != nil {
		rep.Failed++
		return
	}
	reconcileRow(ctx, c, current, rep)
}

func reconcileRow(ctx context.Context, c Claims, row postgres.MediaHolding, rep *Report) {
	if holdsNothing(row) {
		if err := c.Release(ctx, row.Kind, row.Ref, nil); err != nil {
			rep.Failed++
			return
		}
		rep.Released++
		return
	}
	ids := liveIDs(row.MediaIDs)
	held, dangling, err := claimAll(ctx, c, row.Kind, row.Ref, ids)
	rep.Dangling = append(rep.Dangling, dangling...)
	if err != nil {
		rep.Failed++
		return
	}
	// Now that the current set is claimed, drop whatever else this row
	// holds (a release lost after an edit). Nothing held: release all.
	if len(held) > 0 {
		err = c.ReleaseExcept(ctx, row.Kind, row.Ref, held)
	} else {
		err = c.Release(ctx, row.Kind, row.Ref, nil)
	}
	if err != nil {
		rep.Failed++
		return
	}
	rep.Claimed++
}

// claimAll claims ids as one call; if one is missing it falls back to one
// call per id, so a single dangling id does not leave the row's other media
// unprotected.
func claimAll(ctx context.Context, c Claims, kind string, ref uuid.UUID, ids []uuid.UUID) (held []uuid.UUID, dangling []Dangling, err error) {
	err = c.Claim(ctx, kind, ref, ids)
	if err == nil {
		return ids, nil, nil
	}
	if !errors.Is(err, mediaclaims.ErrNotFound) {
		return nil, nil, err
	}
	for _, id := range ids {
		switch err := c.Claim(ctx, kind, ref, []uuid.UUID{id}); {
		case err == nil:
			held = append(held, id)
		case errors.Is(err, mediaclaims.ErrNotFound):
			dangling = append(dangling, Dangling{Kind: kind, Ref: ref, MediaID: id})
		default:
			return nil, dangling, err
		}
	}
	return held, dangling, nil
}

// holdsNothing: the row is gone or dead, or names no media. Its claims go.
func holdsNothing(row postgres.MediaHolding) bool {
	return !row.Live || len(liveIDs(row.MediaIDs)) == 0
}

func knownKind(kind string) bool {
	for _, k := range mediaclaims.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func liveIDs(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
