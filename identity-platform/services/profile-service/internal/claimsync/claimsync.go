// Package claimsync reconciles media-service's claims with the avatar and
// cover ids profile.profiles actually holds (cmd/media-claims). It mirrors
// channel-service's internal/claimsync.
//
// # Why it exists
//
// The claim protocol (internal/service/media_claims.go) is live only for
// writes made after it shipped. Two things need a pass over the table:
//
//   - BACKFILL: every avatar and cover set before the protocol has no claim,
//     so its owner can still delete it out from under the profile;
//   - REPAIR: a best-effort release that failed (media-service down after the
//     row was written) leaves a stale claim, which refuses the owner a delete
//     they are entitled to.
//
// # What one pass does, per (profile, kind)
//
//   - the column names an image: claim it (idempotent), then release anything
//     else the profile holds of that kind. Claim first: releasing first would
//     leave a moment where the profile names an unclaimed asset. An id
//     media-service no longer has is reported as DANGLING (already broken
//     before the pass; an operator decides whether to clear it).
//   - the column is empty or the profile is gone: release all it holds.
//
// A purged profile has no row, so the pass cannot find claims it left behind
// if the purge's release failed; media-service's own account erasure clears
// every claim on the user's assets, which covers that case.
//
// Dry run by default: nothing is sent, the report says what would be.
package claimsync

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/identity-profile-service/internal/mediaclient"
	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
)

// Claims is the media-service claim API (mediaclient.Client).
type Claims interface {
	Claim(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error
	Release(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error
	ReleaseExcept(ctx context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error
}

// Dangling is a profile naming an asset media-service does not have.
type Dangling struct {
	Kind    string
	Ref     uuid.UUID
	MediaID uuid.UUID
}

// Report is the outcome of one pass.
type Report struct {
	Rows     int // (profile, kind) rows looked at
	Claimed  int // rows whose image was (or would be) claimed
	Released int // rows whose claims were (or would be) released
	Dangling []Dangling
	Failed   int // rows media-service could not be asked about; re-run
}

func (r Report) String() string {
	return fmt.Sprintf("rows=%d claimed=%d released=%d dangling=%d failed=%d",
		r.Rows, r.Claimed, r.Released, len(r.Dangling), r.Failed)
}

// Holders serialises with the profile-service writers and re-reads a row
// under that lock (store.Store).
type Holders interface {
	LockMediaHolder(ctx context.Context, ref uuid.UUID) (func(), error)
	MediaClaimRowFor(ctx context.Context, kind string, ref uuid.UUID) (store.MediaClaimRow, error)
}

// Reconcile applies one pass over rows. With apply false nothing is sent.
//
// Applying, each row is locked and RE-READ first: the page was read earlier,
// and acting on a stale id would release the claim a save made since relies
// on — the very defect the claims exist to prevent.
func Reconcile(ctx context.Context, h Holders, c Claims, rows []store.MediaClaimRow, apply bool, rep *Report) {
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

func reconcileLocked(ctx context.Context, h Holders, c Claims, row store.MediaClaimRow, rep *Report) {
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

func reconcileRow(ctx context.Context, c Claims, row store.MediaClaimRow, rep *Report) {
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
	// Now that the current image is claimed, drop whatever else this
	// profile holds of the kind (a release lost after a change).
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

// claimAll claims ids as one call; if one is missing it falls back to one call
// per id, so a dangling id does not leave the rest unprotected.
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

// Source pages through profile.profiles (store.Store).
type Source interface {
	Holders
	ProfileMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]store.MediaClaimRow, uuid.UUID, error)
}

// PageSize is how many profiles one page reads.
const PageSize = 500

// Run reconciles every profile that names an avatar or cover. A read failure
// stops the pass (it is safe to re-run from the start); a claim failure is
// counted and the pass goes on.
func Run(ctx context.Context, src Source, c Claims, apply bool) (Report, error) {
	var rep Report
	after := uuid.Nil
	for {
		rows, last, err := src.ProfileMediaClaimRows(ctx, after, PageSize)
		if err != nil {
			return rep, fmt.Errorf("read profiles: %w", err)
		}
		Reconcile(ctx, src, c, rows, apply, &rep)
		if last == after { // no profile read: done
			break
		}
		after = last
	}
	return rep, nil
}
