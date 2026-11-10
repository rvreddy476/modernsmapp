package service

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MatchClosedFrameType is the real-time frame that tells an open Pulse
// thread its match has ended (unmatch, expiry, a block either way, a
// suspension or deletion) so the app can leave the thread:
//
//	{"type":"match_closed","payload":{"conversation_id":"…","match_id":"…","closed_at":"…"}}
//
// It goes to both members' personal channels (unless they hid the
// conversation) and to convroom:<conversation_id>, like every other
// conversation frame. It carries no reason on purpose: the other person
// must not learn whether they were blocked or unmatched. match_id is left
// out when the conversation has none. Sends to the conversation keep
// failing with 409 MATCH_CLOSED regardless; the frame only saves the wait.
const MatchClosedFrameType = "match_closed"

// matchClosedFrame is the frame body; split out so the wire shape is tested.
func matchClosedFrame(conversationID, matchID uuid.UUID, closedAt time.Time) map[string]interface{} {
	body := map[string]interface{}{
		"conversation_id": conversationID,
		"closed_at":       closedAt.UTC(),
	}
	if matchID != uuid.Nil {
		body["match_id"] = matchID
	}
	return body
}

// PublishDatingMatchClosed sends the match_closed frame for one closed
// dating conversation. Best-effort like every frame: a member lookup
// failure skips it (the send gate is the truth).
func (s *Service) PublishDatingMatchClosed(ctx context.Context, conversationID, matchID uuid.UUID, closedAt time.Time) {
	if s.rdb == nil || conversationID == uuid.Nil {
		return
	}
	members, err := s.convStore.GetMembers(ctx, conversationID)
	if err != nil {
		s.log.Warn("match_closed frame skipped: members unavailable", "conversation_id", conversationID, "err", err)
		return
	}
	s.publishToConversation(conversationID, members, MatchClosedFrameType, matchClosedFrame(conversationID, matchID, closedAt))
}
