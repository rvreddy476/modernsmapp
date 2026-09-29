package service

import (
	"context"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Case-specific restrictions (Copyright Match plan, section 6.2). The
// service adds exactly one thing to the store's transaction: dropping the
// cached post body after commit, so the next read revalidates against the
// row (the access-state read carries the restriction count anyway; the
// drop just saves the stale-body window).

// ApplyPostRestriction applies one verified restriction command.
func (s *Service) ApplyPostRestriction(ctx context.Context, in postgres.RestrictionCommand) (*postgres.RestrictionOutcome, error) {
	out, err := s.pgStore.ApplyPostRestriction(ctx, in)
	if err != nil {
		return nil, err
	}
	if out.Changed && !out.Replayed {
		s.InvalidatePostBodyCache(ctx, in.PostID)
	}
	return out, nil
}

// ListPostRestrictions is trust-safety's reconciliation read.
func (s *Service) ListPostRestrictions(ctx context.Context, f postgres.RestrictionListFilter) ([]postgres.PostRestriction, string, error) {
	return s.pgStore.ListPostRestrictions(ctx, f)
}

// RepairRestrictionCounts is the nightly count self-check (section 9.5).
// Every repaired post's body cache is dropped too.
func (s *Service) RepairRestrictionCounts(ctx context.Context) ([]uuid.UUID, error) {
	repaired, err := s.pgStore.RepairRestrictionCounts(ctx)
	for _, id := range repaired {
		s.InvalidatePostBodyCache(ctx, id)
	}
	return repaired, err
}
