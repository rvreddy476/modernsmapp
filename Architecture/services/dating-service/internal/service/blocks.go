// Blocks (lane D10) — the caller's own block list and undoing a block.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// BlockedPersonCard is a compact person WITHOUT a photo: a blocked person's
// pictures are exactly what the user chose to stop seeing, so the list shows
// only who it is and when the block landed.
type BlockedPersonCard struct {
	UserID    uuid.UUID `json:"user_id"`
	FirstName string    `json:"first_name"`
	Age       int       `json:"age"`
	BlockedAt time.Time `json:"blocked_at"`
}

// ListBlocks returns the people the caller has blocked, newest first.
func (s *Service) ListBlocks(ctx context.Context, userID uuid.UUID) ([]BlockedPersonCard, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	rows, err := s.store.ListBlockedPeople(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]BlockedPersonCard, 0, len(rows))
	for _, r := range rows {
		card := BlockedPersonCard{UserID: r.UserID, BlockedAt: r.BlockedAt}
		if r.FirstName != nil {
			card.FirstName = *r.FirstName
		}
		if r.BirthDate != nil {
			card.Age = store.AgeOn(*r.BirthDate, time.Now())
		}
		out = append(out, card)
	}
	return out, nil
}

// Unblock removes the caller's block. It restores NOTHING the block severed:
// the match it closed stays closed and the sparks it deleted stay deleted —
// the two have to find each other again. A block a report raised is undone
// the same way, by the reporter: the report, its evidence and the grievance
// are untouched records of what happened, and keeping the person blocked
// forever is the reporter's choice, not the system's.
//
// Idempotent: unblocking someone who is not blocked answers removed=false.
func (s *Service) Unblock(ctx context.Context, userID, targetID uuid.UUID) (bool, error) {
	if userID == uuid.Nil || targetID == uuid.Nil {
		return false, fmt.Errorf("invalid: user ids required")
	}
	if userID == targetID {
		return false, fmt.Errorf("invalid: you cannot block yourself")
	}
	removed, err := s.store.UnblockUser(ctx, userID, targetID)
	if err != nil {
		return false, err
	}
	if removed {
		// Both decks were filtered on the block; recompute them.
		s.InvalidatePulseCache(ctx, userID)
		s.InvalidatePulseCache(ctx, targetID)
	}
	return removed, nil
}
