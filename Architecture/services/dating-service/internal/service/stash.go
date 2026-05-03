// Stash service — Add/Remove/List with event emission.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// stashDefaultTTL is the spec §7 default expiry for a stashed candidate.
const stashDefaultTTL = 14 * 24 * time.Hour

// AddStash records a soft-intent stash entry. Default expiresAt is now+14d.
func (s *Service) AddStash(ctx context.Context, userID, candidateID uuid.UUID) (*store.Stash, error) {
	if userID == uuid.Nil || candidateID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user and candidate ids required")
	}
	if userID == candidateID {
		return nil, fmt.Errorf("invalid: cannot stash yourself")
	}
	// Validate that the candidate exists. A missing profile is a 400, not 500.
	if _, err := s.store.GetProfile(ctx, candidateID); err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			return nil, fmt.Errorf("not_found: candidate profile not found")
		}
		return nil, fmt.Errorf("load candidate profile: %w", err)
	}

	expiresAt := time.Now().Add(stashDefaultTTL)
	if err := s.store.AddStash(ctx, userID, candidateID, expiresAt); err != nil {
		return nil, err
	}
	if s.producer != nil {
		if perr := s.producer.PublishStashAdded(ctx, userID, candidateID, expiresAt); perr != nil {
			slog.Warn("publish stash.added failed", "error", perr)
		}
	}
	// Return a synthetic Stash row so the handler can echo it back — the
	// store helper doesn't return the row to keep the write-path narrow.
	return &store.Stash{
		UserID: userID, CandidateID: candidateID,
		StashedAt: time.Now(), ExpiresAt: expiresAt,
	}, nil
}

// RemoveStash drops the entry and emits dating.stash.removed.
func (s *Service) RemoveStash(ctx context.Context, userID, candidateID uuid.UUID, reason string) error {
	if err := s.store.RemoveStash(ctx, userID, candidateID, reason); err != nil {
		return err
	}
	if s.producer != nil {
		if perr := s.producer.PublishStashRemoved(ctx, userID, candidateID, reason); perr != nil {
			slog.Warn("publish stash.removed failed", "error", perr)
		}
	}
	return nil
}

// ListStash returns the user's active stash entries (newest first).
func (s *Service) ListStash(ctx context.Context, userID uuid.UUID) ([]*store.Stash, error) {
	return s.store.ListStash(ctx, userID)
}
