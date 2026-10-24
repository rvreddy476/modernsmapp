package service

import (
	"context"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/google/uuid"
)

// Hidden chats (contract pinned 24 Oct 2026): a member can hide a
// conversation for themself. A call from it rings "Incoming call" with no
// name (CallInvited.hidden_chat, pending invites' hidden_chat) and is left out
// of that member's call history. The other members are unaffected.

// CallHiddenChecker answers whether userID hid the conversation of a call
// with otherID. uuid.Nil as otherID checks the source conversation only.
// Implementations should fail closed (true) on a lookup failure.
type CallHiddenChecker interface {
	HiddenFor(ctx context.Context, userID, otherID uuid.UUID, sourceConversationID *uuid.UUID) bool
}

// WithCallHiddenChecker enables hidden-chat handling for calls.
func (s *Service) WithCallHiddenChecker(c CallHiddenChecker) *Service {
	s.hiddenChecker = c
	return s
}

// callHiddenFor: a DIRECT call counts its source chat and the pair's DM; a
// GROUP call counts only its source chat — a hidden DM with one participant
// does not hide a group call.
func (s *Service) callHiddenFor(ctx context.Context, viewerID, otherID uuid.UUID, callType, sourceType string, sourceID *uuid.UUID) bool {
	if s.hiddenChecker == nil {
		return false
	}
	var source *uuid.UUID
	if sourceType == domain.SourceTypeChat && sourceID != nil {
		source = sourceID
	}
	if !isDirectCallType(callType) {
		if source == nil {
			return false
		}
		otherID = uuid.Nil
	}
	return s.hiddenChecker.HiddenFor(ctx, viewerID, otherID, source)
}

func isDirectCallType(t string) bool {
	return t == domain.CallTypeDirectAudio || t == domain.CallTypeDirectVideo
}

// historyHidden: the call history row's "other side" for a direct call is
// the other participant (the initiator when incoming).
func (s *Service) historyHidden(ctx context.Context, viewerID, initiatorID uuid.UUID, callType, sourceType string, sourceID *uuid.UUID, participants []domain.CallParticipant) bool {
	if s.hiddenChecker == nil {
		return false
	}
	other := initiatorID
	if initiatorID == viewerID {
		other = uuid.Nil
		for _, p := range participants {
			if p.UserID != viewerID {
				other = p.UserID
				break
			}
		}
	}
	return s.callHiddenFor(ctx, viewerID, other, callType, sourceType, sourceID)
}
