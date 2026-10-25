package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Cross-service media claims — the protocol is described on
// postgres.ClaimMedia (internal/store/postgres/media_claims.go).
//
// This layer decides WHO may claim and WHAT they may call it. Claims are
// written by keyed service callers only; the claimant has already decided the
// reference is allowed (channel-service checks the asset is the admin's own
// finished upload before it claims). media-service only refuses to record a
// reference to an asset that does not exist.

var (
	// ErrClaimInvalid: an unknown claimant or kind, an empty or oversized
	// ref, or too many ids. A 400: the caller is misbuilt, not unlucky.
	ErrClaimInvalid = errors.New("invalid media claim")
	// ErrClaimMediaNotFound: an id names no asset (or a deleted one). The
	// claimant must not write its reference.
	ErrClaimMediaNotFound = errors.New("claimed media does not exist")
	// ErrMediaInUse: the owner delete was refused because a claim exists.
	// errors.As a *postgres.MediaInUseError for the holders.
	ErrMediaInUse = postgres.ErrMediaInUse
	// ErrMediaReferenced: the owner delete was refused by a foreign key this
	// service does not attribute to a claimant (a chat reservation, a post).
	ErrMediaReferenced = errors.New("media is still referenced")
)

// claimKinds is every (claimant, kind) this service records, with the words
// the owner reads when a delete is refused. A claimant outside it is refused:
// a claim nobody can name would block a delete with no way to explain it.
var claimKinds = map[string]map[string]claimLabel{
	"channel-service": {
		"channel_update": {"channel update", "channel updates"},
		"channel_avatar": {"channel picture", "channel pictures"},
		"channel_banner": {"channel banner", "channel banners"},
	},
}

type claimLabel struct{ one, many string }

// maxClaimIDs bounds one claim call. channel-service attaches at most 20.
const maxClaimIDs = 50

// maxClaimRefLen bounds claim_ref; every claimant today sends a UUID.
const maxClaimRefLen = 128

func validateClaim(c postgres.MediaClaim, ids []uuid.UUID, forRelease bool) error {
	kinds, ok := claimKinds[c.Claimant]
	if !ok {
		return fmt.Errorf("%w: unknown claimant %q", ErrClaimInvalid, c.Claimant)
	}
	if _, ok := kinds[c.Kind]; !ok {
		return fmt.Errorf("%w: unknown kind %q for %s", ErrClaimInvalid, c.Kind, c.Claimant)
	}
	if strings.TrimSpace(c.Ref) == "" || len(c.Ref) > maxClaimRefLen {
		return fmt.Errorf("%w: ref must be 1..%d characters", ErrClaimInvalid, maxClaimRefLen)
	}
	if !forRelease && len(ids) == 0 {
		return fmt.Errorf("%w: media_ids is empty", ErrClaimInvalid)
	}
	if len(ids) > maxClaimIDs {
		return fmt.Errorf("%w: at most %d media ids per call", ErrClaimInvalid, maxClaimIDs)
	}
	for _, id := range ids {
		if id == uuid.Nil {
			return fmt.Errorf("%w: nil media id", ErrClaimInvalid)
		}
	}
	return nil
}

// ClaimMedia records c on every id, all or nothing, idempotently.
func (s *Service) ClaimMedia(ctx context.Context, c postgres.MediaClaim, ids []uuid.UUID) error {
	if err := validateClaim(c, ids, false); err != nil {
		return err
	}
	return claimStoreError(s.pgStore.ClaimMedia(ctx, c, ids))
}

// claimStoreError translates the store's answer to a claim.
func claimStoreError(err error) error {
	if errors.Is(err, postgres.ErrMediaNotFound) {
		return fmt.Errorf("%w: %v", ErrClaimMediaNotFound, err)
	}
	return err
}

// ownerDeleteStoreError translates the store's answer to an owner delete.
// The in-use error passes through whole: the handler reads its holders.
func ownerDeleteStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, postgres.ErrMediaInUse):
		return err
	case errors.Is(err, postgres.ErrMediaStillReferenced):
		return fmt.Errorf("%w: %v", ErrMediaReferenced, err)
	case errors.Is(err, postgres.ErrMediaNotFound):
		return errOwnerDeleteNotFound
	}
	return fmt.Errorf("delete media record: %w", err)
}

// errOwnerDeleteNotFound keeps the message the owner-delete handler matches.
var errOwnerDeleteNotFound = errors.New("media not found")

// ReleaseMediaClaims removes c's claims on ids (all of c's when ids is empty).
// Releasing what is not claimed is a success.
func (s *Service) ReleaseMediaClaims(ctx context.Context, c postgres.MediaClaim, ids []uuid.UUID) (int64, error) {
	if err := validateClaim(c, ids, true); err != nil {
		return 0, err
	}
	return s.pgStore.ReleaseMediaClaims(ctx, c, ids)
}

// ReleaseMediaClaimsExcept removes c's claims on everything but keep. An
// empty keep is refused: "keep nothing" is a plain release, and taking it
// here would make a client bug that dropped the list release everything.
func (s *Service) ReleaseMediaClaimsExcept(ctx context.Context, c postgres.MediaClaim, keep []uuid.UUID) (int64, error) {
	if err := validateClaim(c, keep, false); err != nil {
		return 0, err
	}
	return s.pgStore.ReleaseMediaClaimsExcept(ctx, c, keep)
}

// MediaInUseMessage is what the owner reads when a delete is refused:
// where the asset is used, counted, and what to do about it.
func MediaInUseMessage(holders []postgres.ClaimHolder) string {
	parts := make([]string, 0, len(holders))
	for _, h := range holders {
		label, ok := claimKinds[h.Claimant][h.Kind]
		if !ok {
			label = claimLabel{"other place", "other places"}
		}
		word := label.many
		if h.Count == 1 {
			word = label.one
		}
		parts = append(parts, fmt.Sprintf("%d %s", h.Count, word))
	}
	if len(parts) == 0 {
		return "This media is still in use and cannot be deleted."
	}
	return "This media is still used in " + joinWithAnd(parts) +
		". Remove it there first, then delete it."
}

// InUseDetail is one entry of the refusal's details.in_use_by.
type InUseDetail struct {
	Service string `json:"service"`
	Kind    string `json:"kind"`
	Count   int    `json:"count"`
}

// MediaInUseDetails is the machine-readable half of the refusal.
func MediaInUseDetails(holders []postgres.ClaimHolder) []InUseDetail {
	out := make([]InUseDetail, 0, len(holders))
	for _, h := range holders {
		out = append(out, InUseDetail{Service: h.Claimant, Kind: h.Kind, Count: h.Count})
	}
	return out
}

func joinWithAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}
