// Purge completeness (Dating plan lane D9).
//
// The database purge (store.PurgeUserDataWithOutcome) closes open matches,
// which PurgeProfile announces as dating.match.closed so chat-service's
// dating consumer closes each conversation by match_id. What Redis holds is
// dropped here, after the database commit:
//
//   - the purged user's own cached deck, and their entries in the reverse
//     index of every candidate that deck showed;
//   - every other viewer's cached deck that shows the purged user (through
//     the reverse index), and the index itself;
//   - the user's boost rate-limit and boost token keys.
package service

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// dropUserCaches removes the purged user's Redis state. Best-effort: a Redis
// failure is logged, and everything left expires on its own TTL except boost
// tokens, which carry none.
func (s *Service) dropUserCaches(ctx context.Context, userID uuid.UUID) {
	if s.rdb == nil || userID == uuid.Nil {
		return
	}
	if own := s.readPulseCache(ctx, userID); own != nil && len(own.Data) > 0 {
		pipe := s.rdb.Pipeline()
		for _, card := range own.Data {
			pipe.SRem(ctx, deckMembershipKey(card.CandidateID), userID.String())
		}
		if _, err := pipe.Exec(ctx); err != nil {
			slog.Warn("purge: deck reverse-index cleanup failed", "user_id", userID, "error", err)
		}
	}
	s.InvalidatePulseCache(ctx, userID)
	s.InvalidateDecksForCandidate(ctx, userID)
	if err := s.rdb.Del(ctx, boostRateLimitKey(userID), boostTokenKey(userID)).Err(); err != nil {
		slog.Warn("purge: boost keys not removed", "user_id", userID, "error", err)
	}
}
