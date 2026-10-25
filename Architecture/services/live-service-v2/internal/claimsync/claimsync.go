// Package claimsync reconciles media-service's claims with the media ids this
// service's tables actually hold (cmd/media-claims). channel-service's
// internal/claimsync, for holders that name at most one asset each.
//
// # Why it exists
//
// The claim protocol (service/media_claims.go) is live only for writes made
// after it shipped:
//
//   - BACKFILL: every row written before it names media with no claim, so its
//     owner can still delete the asset out from under the row;
//   - REPAIR: a best-effort release that failed (media-service down after the
//     row was written) leaves a stale claim, which refuses the owner a delete
//     they are entitled to.
//
// # What one pass does, per row
//
//   - a row that names media: claim it (idempotent), then release anything
//     else the row holds. Claim first: releasing first would leave a moment
//     where the row names an unclaimed asset. An id media-service no longer
//     has is reported as DANGLING (broken before the pass; an operator decides
//     whether to clear it) and the row's claims are released.
//   - a row that names nothing (cleared, or gone): release all it holds.
//
// Applying, each row is locked (the writers' per-row lock) and RE-READ first:
// the page was read earlier, and acting on a stale value would release a
// claim an edit made since relies on.
//
// Dry run by default: nothing is sent, the report says what would be.
package claimsync

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/mediaclient"
)

// Row is one holder of media: what kind of row, which row, and the asset it
// names now (nil: none).
type Row struct {
	Kind    string
	Ref     string
	MediaID *uuid.UUID
}

func (r Row) held() (uuid.UUID, bool) {
	if r.MediaID == nil || *r.MediaID == uuid.Nil {
		return uuid.Nil, false
	}
	return *r.MediaID, true
}

// Claims is media-service's claim API (mediaclient.Client).
type Claims interface {
	Claim(ctx context.Context, kind, ref string, ids []uuid.UUID) error
	Release(ctx context.Context, kind, ref string, ids []uuid.UUID) error
	ReleaseExcept(ctx context.Context, kind, ref string, keep []uuid.UUID) error
}

// Holders serialises with the writers and re-reads a row under that lock.
type Holders interface {
	Lock(ctx context.Context, row Row) (unlock func(), err error)
	Reread(ctx context.Context, row Row) (Row, error)
}

// Source pages through the holder tables.
type Source interface {
	Holders
	// Kinds is every kind Page can read, in pass order.
	Kinds() []string
	// Page returns up to limit rows of kind after the cursor, and the next
	// cursor ("" when there are no more rows).
	Page(ctx context.Context, kind, after string, limit int) (rows []Row, next string, err error)
}

// Dangling is a row naming an asset media-service does not have.
type Dangling struct {
	Kind, Ref string
	MediaID   uuid.UUID
}

// Report is the outcome of one pass.
type Report struct {
	Rows     int // rows looked at
	Claimed  int // rows whose asset was (or would be) claimed
	Released int // rows whose claims were (or would be) released
	Dangling []Dangling
	Failed   int // rows media-service could not be asked about; re-run
}

func (r Report) String() string {
	return fmt.Sprintf("rows=%d claimed=%d released=%d dangling=%d failed=%d",
		r.Rows, r.Claimed, r.Released, len(r.Dangling), r.Failed)
}

// Reconcile applies one pass over rows. With apply false nothing is sent.
func Reconcile(ctx context.Context, h Holders, c Claims, rows []Row, apply bool, rep *Report) {
	for _, row := range rows {
		rep.Rows++
		if !apply {
			if _, ok := row.held(); ok {
				rep.Claimed++
			} else {
				rep.Released++
			}
			continue
		}
		reconcileLocked(ctx, h, c, row, rep)
	}
}

func reconcileLocked(ctx context.Context, h Holders, c Claims, row Row, rep *Report) {
	unlock, err := h.Lock(ctx, row)
	if err != nil {
		rep.Failed++
		return
	}
	defer unlock()
	current, err := h.Reread(ctx, row)
	if err != nil {
		rep.Failed++
		return
	}
	reconcileRow(ctx, c, current, rep)
}

func reconcileRow(ctx context.Context, c Claims, row Row, rep *Report) {
	id, ok := row.held()
	if !ok {
		if err := c.Release(ctx, row.Kind, row.Ref, nil); err != nil {
			rep.Failed++
			return
		}
		rep.Released++
		return
	}
	switch err := c.Claim(ctx, row.Kind, row.Ref, []uuid.UUID{id}); {
	case errors.Is(err, mediaclient.ErrNotFound):
		rep.Dangling = append(rep.Dangling, Dangling{Kind: row.Kind, Ref: row.Ref, MediaID: id})
		if err := c.Release(ctx, row.Kind, row.Ref, nil); err != nil {
			rep.Failed++
		}
		return
	case err != nil:
		rep.Failed++
		return
	}
	// The current asset is claimed: drop whatever else the row holds (a
	// release lost after an edit).
	if err := c.ReleaseExcept(ctx, row.Kind, row.Ref, []uuid.UUID{id}); err != nil {
		rep.Failed++
		return
	}
	rep.Claimed++
}

// PageSize is how many rows one page reads.
const PageSize = 500

// Run reconciles every holder row src lists. A read failure stops the pass
// (it is safe to re-run from the start); a claim failure is counted and the
// pass goes on.
func Run(ctx context.Context, src Source, c Claims, apply bool) (Report, error) {
	var rep Report
	for _, kind := range src.Kinds() {
		after := ""
		for {
			rows, next, err := src.Page(ctx, kind, after, PageSize)
			if err != nil {
				return rep, fmt.Errorf("read %s rows: %w", kind, err)
			}
			Reconcile(ctx, src, c, rows, apply, &rep)
			if next == "" || len(rows) == 0 {
				break
			}
			after = next
		}
	}
	return rep, nil
}
