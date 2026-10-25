// Package claimsync reconciles media-service's claims with the media ids
// user-service's tables actually hold (cmd/media-claims, and the in-server
// drain of media_claim_pending).
//
// # Why it exists
//
// The claim protocol (internal/service/media_claims.go) is live only for
// writes made after it shipped, and its releases are best effort:
//
//   - BACKFILL: every row written before the protocol names media with no
//     claim, so its owner can still delete it out from under the row;
//   - REPAIR: a release that failed (media-service down after the row was
//     written or deleted) leaves a stale claim, which refuses the owner a
//     delete they are entitled to. Deleted rows are found through
//     media_claim_pending, written in the delete's own transaction.
//
// # What one pass does, per holder
//
//   - a LIVE row with media: claim its current set (idempotent), then release
//     anything else the holder has. Claim first: releasing first would leave a
//     moment where the row names an unclaimed asset. An id media-service no
//     longer has is reported DANGLING (broken before the pass; an operator
//     decides whether to clear it); a TEXT value that is not a media id at
//     all is reported INVALID. Neither is claimed.
//   - a row that is gone, terminal (a disabled page) or empty: release all.
//
// Dry run by default (cmd/media-claims): nothing is sent, the report says
// what would be.
package claimsync

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/user-service/internal/mediaclient"
	"github.com/atpost/user-service/internal/store"
	"github.com/google/uuid"
)

// Claims is the media-service claim API (mediaclient.Client).
type Claims interface {
	Claim(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error
	Release(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error
	ReleaseExcept(ctx context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error
}

// Dangling is a row naming an asset media-service does not have.
type Dangling struct {
	Kind    string
	Ref     uuid.UUID
	MediaID uuid.UUID
}

// Invalid is a TEXT media column holding something that is not a media id.
type Invalid struct {
	Kind  string
	Ref   uuid.UUID
	Value string
}

// Report is the outcome of one pass.
type Report struct {
	Rows     int // holders looked at
	Claimed  int // live holders whose set was (or would be) claimed
	Released int // dead or empty holders whose claims were (or would be) released
	Pending  int // media_claim_pending entries settled (or that would be)
	Dangling []Dangling
	Invalid  []Invalid
	Failed   int // holders media-service could not be asked about; re-run
}

func (r Report) String() string {
	return fmt.Sprintf("rows=%d claimed=%d released=%d pending=%d dangling=%d invalid=%d failed=%d",
		r.Rows, r.Claimed, r.Released, r.Pending, len(r.Dangling), len(r.Invalid), r.Failed)
}

// Holders serialises with the user-service writers and re-reads a holder
// under that lock (store.Store).
type Holders interface {
	LockMediaHolder(ctx context.Context, ref uuid.UUID) (func(), error)
	MediaClaimRowFor(ctx context.Context, kind string, ref uuid.UUID) (store.MediaClaimRow, error)
}

// Reconcile applies one pass over rows. With apply false nothing is sent.
//
// Applying, each holder is locked and RE-READ first: the page was read
// earlier, and acting on a stale set would release a claim an edit made
// since relies on — the very defect the claims exist to prevent.
func Reconcile(ctx context.Context, h Holders, c Claims, rows []store.MediaClaimRow, apply bool, rep *Report) {
	for _, row := range rows {
		rep.Rows++
		if apply {
			ReconcileHolder(ctx, h, c, row.Kind, row.Ref, rep)
			continue
		}
		reportInvalid(row, rep)
		if !row.Live || len(liveIDs(row.MediaIDs)) == 0 {
			rep.Released++
		} else {
			rep.Claimed++
		}
	}
}

// ReconcileHolder settles one holder under its lock. It reports whether the
// holder is now settled (false: re-run).
func ReconcileHolder(ctx context.Context, h Holders, c Claims, kind string, ref uuid.UUID, rep *Report) bool {
	unlock, err := h.LockMediaHolder(ctx, ref)
	if err != nil {
		rep.Failed++
		return false
	}
	defer unlock()
	current, err := h.MediaClaimRowFor(ctx, kind, ref)
	if err != nil {
		rep.Failed++
		return false
	}
	return reconcileRow(ctx, c, current, rep)
}

func reportInvalid(row store.MediaClaimRow, rep *Report) {
	for _, v := range row.Invalid {
		rep.Invalid = append(rep.Invalid, Invalid{Kind: row.Kind, Ref: row.Ref, Value: v})
	}
}

func reconcileRow(ctx context.Context, c Claims, row store.MediaClaimRow, rep *Report) bool {
	if c == nil {
		rep.Failed++
		return false
	}
	reportInvalid(row, rep)
	ids := liveIDs(row.MediaIDs)
	if !row.Live || len(ids) == 0 {
		if err := c.Release(ctx, row.Kind, row.Ref, nil); err != nil {
			rep.Failed++
			return false
		}
		rep.Released++
		return true
	}
	held, dangling, err := claimAll(ctx, c, row.Kind, row.Ref, ids)
	rep.Dangling = append(rep.Dangling, dangling...)
	if err != nil {
		rep.Failed++
		return false
	}
	// The current set is claimed: drop whatever else this holder has (a
	// release lost after an edit). Nothing held: release all.
	if len(held) > 0 {
		err = c.ReleaseExcept(ctx, row.Kind, row.Ref, held)
	} else {
		err = c.Release(ctx, row.Kind, row.Ref, nil)
	}
	if err != nil {
		rep.Failed++
		return false
	}
	rep.Claimed++
	return true
}

// claimAll claims ids as one call; if one is missing it falls back to one
// call per id, so a single dangling id does not leave the others unprotected.
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

// Pending is the media_claim_pending queue (store.Store).
type Pending interface {
	Holders
	PendingMediaClaimHolders(ctx context.Context, limit int) ([]store.PendingMediaClaimHolder, error)
	ClearMediaClaimHolderPending(ctx context.Context, p store.PendingMediaClaimHolder) error
}

// Drain settles up to limit pending holders, always applying: each entry
// was written because a claim needs releasing (or re-claiming), and an entry
// is removed only once its holder is settled. Returns how many it removed.
func Drain(ctx context.Context, src Pending, c Claims, limit int, rep *Report) (int, error) {
	pending, err := src.PendingMediaClaimHolders(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("read pending media claims: %w", err)
	}
	cleared := 0
	for _, p := range pending {
		rep.Rows++
		if !ReconcileHolder(ctx, src, c, p.Kind, p.Ref, rep) {
			continue // left queued; the next drain retries it
		}
		if err := src.ClearMediaClaimHolderPending(ctx, p); err != nil {
			rep.Failed++
			continue
		}
		rep.Pending++
		cleared++
	}
	return cleared, nil
}

// Source pages through the holder tables (store.Store).
type Source interface {
	Pending
	ChannelMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]store.MediaClaimRow, uuid.UUID, error)
	PortfolioMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]store.MediaClaimRow, uuid.UUID, error)
	BusinessPageMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]store.MediaClaimRow, uuid.UUID, error)
}

// PageSize is how many holder rows one page reads.
const PageSize = 500

type pager func(ctx context.Context, after uuid.UUID, limit int) ([]store.MediaClaimRow, uuid.UUID, error)

// Run reconciles every holder row that names media and, applying, drains the
// pending queue (rows already deleted). A read failure stops the pass (it is
// safe to re-run from the start); a claim failure is counted and the pass
// goes on.
func Run(ctx context.Context, src Source, c Claims, apply bool) (Report, error) {
	var rep Report
	for _, tbl := range []struct {
		name string
		page pager
	}{
		{"channels", src.ChannelMediaClaimRows},
		{"portfolio items", src.PortfolioMediaClaimRows},
		{"business pages", src.BusinessPageMediaClaimRows},
	} {
		name, page := tbl.name, tbl.page
		after := uuid.Nil
		for {
			rows, last, err := page(ctx, after, PageSize)
			if err != nil {
				return rep, fmt.Errorf("read %s: %w", name, err)
			}
			Reconcile(ctx, src, c, rows, apply, &rep)
			if last == after { // nothing read: done
				break
			}
			after = last
		}
	}
	if !apply {
		pending, err := src.PendingMediaClaimHolders(ctx, PageSize)
		if err != nil {
			return rep, fmt.Errorf("read pending media claims: %w", err)
		}
		rep.Pending = len(pending)
		return rep, nil
	}
	for {
		n, err := Drain(ctx, src, c, PageSize, &rep)
		if err != nil {
			return rep, err
		}
		if n == 0 { // empty, or nothing more can be settled this run
			break
		}
	}
	return rep, nil
}
