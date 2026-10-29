// Package mediaclaims is the attach gate and the claim protocol for every
// media id this service writes into its own tables (the same rules as
// channel-service's internal/service/update_media.go and media_claims.go).
//
// # The hole
//
// These columns carry no foreign key into media_assets, which lives in
// media-service. Without a gate a writer could attach another user's asset by
// lifting its id from any payload, or an unconfirmed upload that the orphan
// sweep then reclaims; and nothing stopped the uploader deleting the asset
// afterwards (DELETE /v1/media/{id}), leaving the row pointing at nothing.
//
// # The gate (Attach, before anything is claimed)
//
// Every id a write ADDS must be:
//   - an asset media-service admits to a keyed caller;
//   - uploaded by the actor;
//   - CONFIRMED: `uploaded`, `processing` or `ready` (an allowlist, so
//     `pending_upload`, `failed`, `deleted`, empty and unknown are refused);
//   - not refused by moderation (`rejected`, or empty, is refused).
//
// Ids the row already holds are not re-checked: re-sending them attaches
// nothing new, and they may be another admin's upload.
//
// # The protocol (media-service internal/store/postgres/media_claims.go)
//
//  1. CLAIM every added id, before the write. media-service locks the asset
//     row while it records the claim, so an owner delete racing the attach
//     either lands first (the claim is refused, the write never happens) or
//     waits and is refused.
//  2. WRITE. If the write fails, RELEASE exactly what step 1 claimed.
//  3. After a successful write, RELEASE every id the row no longer holds.
//
// A failure in 2–3 leaves an EXTRA claim (the owner is refused a delete the
// reconcile tool, cmd/media-claims, later allows), never a missing one.
//
// # Fail closed
//
// No client, an unreachable media-service or an unreadable answer refuses a
// write that ADDS media with ErrMediaUnavailable (503). A write that only
// removes media is never refused for that reason; its release is best effort.
package mediaclaims

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/group-service/internal/mediaclient"
	"github.com/google/uuid"
)

var (
	// ErrMediaNotFound: media-service has no such asset (or will not admit it).
	ErrMediaNotFound = errors.New("media does not exist")
	// ErrMediaNotOwned is worded to say nothing about the foreign asset.
	ErrMediaNotOwned = errors.New("media cannot be attached by this user")
	// ErrMediaNotReady: the upload is unconfirmed, failed, or was refused by
	// moderation.
	ErrMediaNotReady = errors.New("media has not finished uploading")
	// ErrMediaUnavailable: the check or the claim could not be made. Retryable.
	ErrMediaUnavailable = errors.New("media-service is unavailable")
)

// MaxPerCall bounds the ids one change may add: media-service refuses more
// than 50 in one claim, and each new id is one lookup made while a user waits.
const MaxPerCall = 20

// Lookup reads an asset's record (mediaclient.Client).
type Lookup interface {
	Get(ctx context.Context, mediaID uuid.UUID) (*mediaclient.Asset, error)
}

// Claims is media-service's claim API (mediaclient.Client).
type Claims interface {
	Claim(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error
	Release(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error
}

// Gate is the attach gate plus the claim protocol. A nil *Gate, or one built
// without a lookup or claims, refuses every write that adds media.
type Gate struct {
	lookup Lookup
	claims Claims
}

// New builds a gate. Either argument may be nil (fail closed).
func New(lookup Lookup, claims Claims) *Gate {
	return &Gate{lookup: lookup, claims: claims}
}

// FromClient builds the gate on one media-service client, which is both the
// lookup and the claim API. A nil client yields a gate that refuses adds.
func FromClient(c *mediaclient.Client) *Gate {
	if c == nil {
		return New(nil, nil)
	}
	return New(c, c)
}

// Change is what one write does to one holder's media: the ids it adds and
// the ids it stops holding.
type Change struct {
	Kind      string
	Ref       uuid.UUID
	Add, Drop []uuid.UUID
}

// releaseTimeout bounds one best-effort release. Releases run on a context
// detached from the request: a client hanging up after its write committed
// must not leave the compensation undone.
const releaseTimeout = 5 * time.Second

var confirmedStatuses = map[string]bool{"uploaded": true, "processing": true, "ready": true}

// CheckAsset is the per-asset decision, as a pure function.
func CheckAsset(a *mediaclient.Asset, actorID uuid.UUID) error {
	if a.UploaderID != actorID {
		return ErrMediaNotOwned
	}
	if !confirmedStatuses[a.ProcessingStatus] {
		return fmt.Errorf("%w: %s is %q", ErrMediaNotReady, a.ID, a.ProcessingStatus)
	}
	if a.ModerationStatus == "" || a.ModerationStatus == "rejected" {
		return fmt.Errorf("%w: %s moderation is %q", ErrMediaNotReady, a.ID, a.ModerationStatus)
	}
	return nil
}

// verify checks every added id. It stops at the first refusal: reporting
// which ids of a batch belong to someone else would be an ownership oracle.
func (g *Gate) verify(ctx context.Context, actorID uuid.UUID, changes []Change) error {
	for _, ch := range changes {
		if len(ch.Add) > MaxPerCall {
			return fmt.Errorf("invalid: at most %d media items", MaxPerCall)
		}
		for _, id := range ch.Add {
			if id == uuid.Nil {
				return ErrMediaNotFound
			}
			if g == nil || g.lookup == nil {
				return fmt.Errorf("%w: no media-service client configured", ErrMediaUnavailable)
			}
			a, err := g.lookup.Get(ctx, id)
			switch {
			case errors.Is(err, mediaclient.ErrNotFound):
				return ErrMediaNotFound
			case err != nil:
				return fmt.Errorf("%w: %v", ErrMediaUnavailable, err)
			case a == nil:
				return fmt.Errorf("%w: empty answer", ErrMediaUnavailable)
			}
			if err := CheckAsset(a, actorID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Verify is the gate on its own: every added id must be the actor's own
// confirmed upload. For a writer that has a step to run between the check and
// the claim (an anonymous post scopes its attachments there); it then calls
// Write. Attach is Verify then Write.
func (g *Gate) Verify(ctx context.Context, actorID uuid.UUID, changes []Change) error {
	return g.verify(ctx, actorID, changes)
}

// Attach checks every added id is the actor's own confirmed upload, then runs
// write under the claim protocol.
func (g *Gate) Attach(ctx context.Context, actorID uuid.UUID, changes []Change, write func() error) error {
	if err := g.verify(ctx, actorID, changes); err != nil {
		return err
	}
	return g.Write(ctx, changes, write)
}

// Write runs write under the claim protocol, without the ownership check: for
// a writer whose ids were checked elsewhere, or that only removes media.
func (g *Gate) Write(ctx context.Context, changes []Change, write func() error) error {
	// 1. Claim, in order; a refusal part way releases what was claimed.
	var claimed []Change
	for _, ch := range changes {
		if len(ch.Add) == 0 {
			continue
		}
		if g == nil || g.claims == nil { // no claim client: refuse the add
			g.releaseClaimed(ctx, claimed) // compensate earlier claims
			return fmt.Errorf("%w: no media-service claim client configured", ErrMediaUnavailable)
		}
		if err := g.claims.Claim(ctx, ch.Kind, ch.Ref, ch.Add); err != nil {
			g.releaseClaimed(ctx, claimed) // compensate a partial claim
			if errors.Is(err, mediaclient.ErrNotFound) {
				return ErrMediaNotFound
			}
			return fmt.Errorf("%w: claim: %v", ErrMediaUnavailable, err)
		}
		claimed = append(claimed, ch)
	}

	// 2. Write; compensate on failure.
	if err := write(); err != nil {
		g.releaseClaimed(ctx, claimed) // compensate the failed write
		return err
	}

	// 3. Release what the row no longer holds.
	for _, ch := range changes {
		if len(ch.Drop) > 0 {
			g.Release(ctx, ch.Kind, ch.Ref, ch.Drop)
		}
	}
	return nil
}

func (g *Gate) releaseClaimed(ctx context.Context, claimed []Change) {
	for _, ch := range claimed {
		g.Release(ctx, ch.Kind, ch.Ref, ch.Add)
	}
}

// Release is best effort: a failure leaves a stale claim (the owner's delete
// is refused until the reconcile tool clears it), never a dangling reference.
// ids nil releases everything ref holds of kind. It reports success.
func (g *Gate) Release(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) bool {
	if g == nil || g.claims == nil {
		slog.Warn(mediaclient.Claimant+": media claim not released (no claim client); run the reconcile tool",
			"kind", kind, "ref", ref)
		return false
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := g.claims.Release(rctx, kind, ref, ids); err != nil {
		slog.Warn(mediaclient.Claimant+": media claim release failed; the owner cannot delete it until the reconcile tool runs",
			"kind", kind, "ref", ref, "media", len(ids), "error", err)
		return false
	}
	return true
}

// Diff returns the ids in next that are not in prev (added) and the ids in
// prev that are not in next (removed). uuid.Nil is not media and is ignored;
// duplicates collapse.
func Diff(prev, next []uuid.UUID) (added, removed []uuid.UUID) {
	inPrev := map[uuid.UUID]bool{}
	for _, id := range prev {
		if id != uuid.Nil {
			inPrev[id] = true
		}
	}
	inNext := map[uuid.UUID]bool{}
	for _, id := range next {
		if id == uuid.Nil || inNext[id] {
			continue
		}
		inNext[id] = true
		if !inPrev[id] {
			added = append(added, id)
		}
	}
	for _, id := range prev {
		if id != uuid.Nil && !inNext[id] {
			removed = append(removed, id)
			inNext[id] = true // collapse duplicates in prev
		}
	}
	return added, removed
}

// OptionalDiff is Diff for a single optional column.
func OptionalDiff(prev, next *uuid.UUID) (added, removed []uuid.UUID) {
	var p, n []uuid.UUID
	if prev != nil {
		p = []uuid.UUID{*prev}
	}
	if next != nil {
		n = []uuid.UUID{*next}
	}
	return Diff(p, n)
}

// Unique drops uuid.Nil and duplicates, keeping order.
func Unique(ids []uuid.UUID) []uuid.UUID {
	added, _ := Diff(nil, ids)
	return added
}
