// Privacy service — §P1-3 (PRODUCTION_GAP_ANALYSIS.md), lane D7.
//
// Thin wrapper over store.GetPrivacy / store.UpdatePrivacy. Mutations
// also drop the viewer's cached pulse deck so the privacy change
// takes effect on the next discovery refresh — the deck currently
// caches the masked response shape, so a stale row would surface
// pre-toggle values.
package service

import (
	"context"
	"log/slog"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// EchoesConsentType is the dating_consent_log consent_type for Echoes.
const EchoesConsentType = "echoes"

// GetPrivacy returns the caller's current §P1-3 privacy settings.
func (s *Service) GetPrivacy(ctx context.Context, userID uuid.UUID) (*store.Privacy, error) {
	return s.store.GetPrivacy(ctx, userID)
}

// UpdatePrivacy applies a partial update and returns the post-update
// row. Always invalidates the viewer's pulse deck (verified_only_filter
// shapes it). For the candidate-side flags (incognito,
// blur_photos_until_match, hide_last_active) it also fans out to OTHER
// viewers' decks, because their cached cards include this candidate.
// An Echoes opt-in or opt-out is recorded in the consent log.
func (s *Service) UpdatePrivacy(ctx context.Context, userID uuid.UUID, u store.PrivacyUpdate) (*store.Privacy, error) {
	out, err := s.store.UpdatePrivacy(ctx, userID, u)
	if err != nil {
		return nil, err
	}
	s.InvalidatePulseCache(ctx, userID)
	// Candidate-side flags clear OTHER viewers' decks so the toggle takes
	// effect immediately. hide_last_active is candidate-side too: other
	// viewers' cached cards carry this profile's last-active bucket.
	if u.Incognito != nil || u.BlurPhotosUntilMatch != nil || u.HideLastActive != nil {
		s.InvalidateDecksForCandidate(ctx, userID)
	}
	if u.EchoesConsent != nil {
		if cerr := s.RecordConsent(ctx, userID, EchoesConsentType, *u.EchoesConsent); cerr != nil {
			slog.Warn("privacy: record echoes consent failed", "user_id", userID, "error", cerr)
		}
	}
	return out, nil
}
