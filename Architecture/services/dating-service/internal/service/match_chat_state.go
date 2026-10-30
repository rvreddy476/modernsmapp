package service

import (
	"context"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// MatchChatState answers whether matchID's members may chat now (audit H2
// backstop for chat-service's send gate): the match open, the pair
// unblocked, neither member suspended, deleted or on a chat-blocking risk
// level. store.ErrMatchNotFound for an unknown match.
func (s *Service) MatchChatState(ctx context.Context, matchID uuid.UUID) (*store.MatchChatState, error) {
	return s.store.GetMatchChatState(ctx, matchID)
}
