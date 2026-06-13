// Privacy service — §P1-3 (PRODUCTION_GAP_ANALYSIS.md).
//
// Thin wrapper over store.GetPrivacy / store.UpdatePrivacy. Mutations
// also drop the viewer's cached pulse deck so the privacy change
// takes effect on the next discovery refresh — the deck currently
// caches the masked response shape, so a stale row would surface
// pre-toggle values.
package service

import (
	"context"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// GetPrivacy returns the caller's current §P1-3 privacy settings.
func (s *Service) GetPrivacy(ctx context.Context, userID uuid.UUID) (*store.Privacy, error) {
	return s.store.GetPrivacy(ctx, userID)
}

// UpdatePrivacy applies a partial update and returns the post-update
// row. Always invalidates the viewer's pulse deck — the cached
// response carries privacy-derived fields (distance_bucket,
// last_active_at masking, blur application) that go stale on any
// toggle. For the candidate-side flags (incognito,
// blur_photos_until_match) we also fan out to OTHER viewers' decks
// because their cached cards include this candidate.
func (s *Service) UpdatePrivacy(ctx context.Context, userID uuid.UUID, u store.PrivacyUpdate) (*store.Privacy, error) {
	out, err := s.store.UpdatePrivacy(ctx, userID, u)
	if err != nil {
		return nil, err
	}
	// Drop the viewer's own deck — verified_only_filter +
	// approximate_location + hide_last_active are viewer-side
	// presentation flags whose effects live entirely in this user's
	// cached response.
	s.InvalidatePulseCache(ctx, userID)
	// Candidate-side flags also need to clear OTHER viewers' decks.
	// Be conservative: any change to incognito or
	// blur_photos_until_match triggers the fan-out so a
	// "show as incognito" toggle takes effect immediately.
	if u.Incognito != nil || u.BlurPhotosUntilMatch != nil {
		s.InvalidateDecksForCandidate(ctx, userID)
	}
	return out, nil
}
