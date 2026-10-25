// Package claimsync reconciles media-service's claims with the media ids this
// service's tables actually hold (cmd/media-claims). The same rules as
// channel-service's internal/claimsync.
//
// # Why it exists
//
// The claim protocol (internal/mediaclaims) is live only for writes made after
// it shipped. Two things need a pass over the tables:
//
//   - BACKFILL: every row written before the protocol names media with no
//     claim, so its owner can still delete it out from under the row;
//   - REPAIR: a best-effort release that failed (media-service down after the
//     row was written, or a delete path that does not release, such as a
//     moderation removal) leaves a stale claim, which refuses the owner a
//     delete they are entitled to.
//
// # What one pass does, per row
//
//   - a LIVE row with media: claim its current set (idempotent), then release
//     anything else the row holds. Claim first: releasing first would leave a
//     moment where the row names an unclaimed asset. An id media-service no
//     longer has is reported as DANGLING (already broken before the pass; an
//     operator decides whether to strip it) and the row's other ids are still
//     claimed one by one.
//   - a DEAD row (deleted, or its parent deleted): release all it holds.
//
// Applying, each row is locked with the writers' own lock and RE-READ first:
// the page was read earlier, and acting on a stale set would release a claim
// an edit made since relies on.
//
// Dry run by default: nothing is sent, the report says what would be.
package claimsync

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/qa-service/internal/mediaclient"
	"github.com/google/uuid"
)

// Row is one holder that holds (or held) media. Live is false when the row,
// or what it belongs to, is deleted: its claims should be released.
type Row struct {
	Kind     string // mediaclient.Kind*
	Ref      uuid.UUID
	MediaIDs []uuid.UUID
	Live     bool
}

// Claims is the media-service claim API (mediaclient.Client).
type Claims interface {
	Claim(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error
	Release(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error
	ReleaseExcept(ctx context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error
}

// Holders serialises with the writers and re-reads a row under that lock
// (store.Store).
type Holders interface {
	LockMediaHolder(ctx context.Context, ref uuid.UUID) (func(), error)
	MediaClaimRowFor(ctx context.Context, kind string, ref uuid.UUID) (Row, error)
}

// Pager reads one page of holders after the cursor `after`, in ref order,
// and returns the last ref it read (the next cursor; `after` when it read
// nothing, which ends the pass). One ref may yield several rows.
type Pager func(ctx context.Context, after uuid.UUID, limit int) ([]Row, uuid.UUID, error)

// Dangling is a row naming an asset media-service does not have.
type Dangling struct {
	Kind    string
	Ref     uuid.UUID
	MediaID uuid.UUID
}

// Report is the outcome of one pass.
type Report struct {
	Rows     int // rows looked at
	Claimed  int // live rows whose set was (or would be) claimed
	Released int // dead rows whose claims were (or would be) released
	Dangling []Dangling
	Failed   int // rows that could not be reconciled; re-run
}

func (r Report) String() string {
	return fmt.Sprintf("rows=%d claimed=%d released=%d dangling=%d failed=%d",
		r.Rows, r.Claimed, r.Released, len(r.Dangling), r.Failed)
}

// PageSize is how many holders one page reads.
const PageSize = 500

// Run reconciles every holder each pager yields. A read failure stops the
// pass (it is safe to re-run from the start); a per-row failure is counted
// and the pass goes on. With apply false, c is never called and may be nil.
func Run(ctx context.Context, h Holders, c Claims, pagers []Pager, apply bool) (Report, error) {
	var rep Report
	for i, page := range pagers {
		after := uuid.Nil
		for {
			rows, last, err := page(ctx, after, PageSize)
			if err != nil {
				return rep, fmt.Errorf("read holders (table %d): %w", i+1, err)
			}
			Reconcile(ctx, h, c, rows, apply, &rep)
			if last == after {
				break
			}
			after = last
		}
	}
	return rep, nil
}

// Reconcile applies one pass over rows. With apply false nothing is sent.
func Reconcile(ctx context.Context, h Holders, c Claims, rows []Row, apply bool, rep *Report) {
	for _, row := range rows {
		rep.Rows++
		if apply {
			reconcileLocked(ctx, h, c, row, rep)
			continue
		}
		if !row.Live || len(liveIDs(row.MediaIDs)) == 0 {
			rep.Released++
		} else {
			rep.Claimed++
		}
	}
}

func reconcileLocked(ctx context.Context, h Holders, c Claims, row Row, rep *Report) {
	unlock, err := h.LockMediaHolder(ctx, row.Ref)
	if err != nil {
		rep.Failed++
		return
	}
	defer unlock()
	current, err := h.MediaClaimRowFor(ctx, row.Kind, row.Ref)
	if err != nil {
		rep.Failed++
		return
	}
	reconcileRow(ctx, c, current, rep)
}

func reconcileRow(ctx context.Context, c Claims, row Row, rep *Report) {
	ids := liveIDs(row.MediaIDs)
	if !row.Live || len(ids) == 0 {
		if err := c.Release(ctx, row.Kind, row.Ref, nil); err != nil {
			rep.Failed++
			return
		}
		rep.Released++
		return
	}
	held, dangling, err := claimAll(ctx, c, row.Kind, row.Ref, ids)
	rep.Dangling = append(rep.Dangling, dangling...)
	if err != nil {
		rep.Failed++
		return
	}
	// Now that the current set is claimed, drop whatever else this row holds
	// (a release lost after an edit). Nothing held: release all.
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
	if !errors.Is(err, mediaclient.ErrNotFound) {
		return nil, nil, err
	}
	for _, id := range ids {
		switch err := c.Claim(ctx, kind, ref, []uuid.UUID{id}); {
		case err == nil:
			held = append(held, id)
		case errors.Is(err, mediaclient.ErrNotFound):
			dangling = append(dangling, Dangling{Kind: kind, Ref: ref, MediaID: id})
		default:
			return nil, dangling, err
		}
	}
	return held, dangling, nil
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
