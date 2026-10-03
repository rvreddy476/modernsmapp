package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Dating mechanic M5 — first move. dating-service decides, per match, who
// may send the first message (a user who opted in to moving first) and tells
// chat when it creates the conversation. Until the first message lands, only
// those users may send. The other person's one way in is answering one of
// the first mover's opening questions; dating-service validates the answer
// and posts it through SendDatingOpeningAnswer, the only caller allowed past
// the gate.

var (
	// ErrFirstMovePending: the conversation is waiting for its first message
	// from someone else. Mapped to 403 FIRST_MOVE_PENDING.
	ErrFirstMovePending = errors.New("the other person sends the first message in this match")
	// ErrOpeningAnswerNotAllowed: the opening answer route was used outside
	// the one case it serves. Mapped to 409 OPENING_ANSWER_NOT_ALLOWED.
	ErrOpeningAnswerNotAllowed = errors.New("an opening answer is not allowed in this conversation now")
	// ErrDatingConversationNotFound re-exports the store sentinel (404).
	ErrDatingConversationNotFound = postgres.ErrDatingConversationNotFound
	// ErrFirstMoverNotInPair: a first mover who is not one of the pair.
	ErrFirstMoverNotInPair = errors.New("first movers must be members of the match")
)

// validateFirstMovers refuses a first mover outside the matched pair.
func validateFirstMovers(userA, userB uuid.UUID, movers []uuid.UUID) error {
	for _, m := range movers {
		if m != userA && m != userB {
			return ErrFirstMoverNotInPair
		}
	}
	return nil
}

// datingFirstMoveStore is the part of the conversation store mechanic M5
// needs; the Postgres store implements it.
type datingFirstMoveStore interface {
	SetDatingFirstMovers(ctx context.Context, conversationID uuid.UUID, movers []uuid.UUID) error
	DatingConversationByMatch(ctx context.Context, matchID uuid.UUID) (uuid.UUID, error)
}

type firstMoveBypassKey struct{}

// withFirstMoveBypass marks a send as the validated opening answer.
func withFirstMoveBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, firstMoveBypassKey{}, true)
}

func firstMoveBypassed(ctx context.Context) bool {
	v, _ := ctx.Value(firstMoveBypassKey{}).(bool)
	return v
}

// firstMoveBlocks reports whether meta's first-move rule refuses a send from
// userID: a dating conversation with first movers, no message yet, and a
// sender who is not one of them.
func firstMoveBlocks(meta *postgres.ConversationMeta, userID uuid.UUID) bool {
	if meta == nil || meta.SourceApp != "dating" || len(meta.FirstMovers) == 0 || meta.LastMessageAt != nil {
		return false
	}
	for _, m := range meta.FirstMovers {
		if m == userID {
			return false
		}
	}
	return true
}

// setDatingFirstMovers stores the first movers for a dating conversation.
func (s *Service) setDatingFirstMovers(ctx context.Context, conversationID uuid.UUID, movers []uuid.UUID) error {
	store, ok := s.convStore.(datingFirstMoveStore)
	if !ok {
		return fmt.Errorf("conversation store cannot record dating first movers")
	}
	return store.SetDatingFirstMovers(ctx, conversationID, movers)
}

// SendDatingOpeningAnswer posts the opening answer dating-service validated:
// the first message of a first-move conversation, sent by the member who is
// NOT a first mover. Anything else is ErrOpeningAnswerNotAllowed, so this
// route can never be used to send an ordinary message as someone.
func (s *Service) SendDatingOpeningAnswer(ctx context.Context, matchID, senderID uuid.UUID, text, idempotencyKey string) (*MessageResponse, error) {
	if matchID == uuid.Nil || senderID == uuid.Nil || strings.TrimSpace(text) == "" {
		return nil, ErrOpeningAnswerNotAllowed
	}
	store, ok := s.convStore.(datingFirstMoveStore)
	if !ok {
		return nil, fmt.Errorf("conversation store cannot look up dating conversations")
	}
	convID, err := store.DatingConversationByMatch(ctx, matchID)
	if err != nil {
		return nil, err
	}
	meta, err := s.convStore.GetConversationMeta(ctx, convID)
	if err != nil {
		return nil, err
	}
	if meta == nil || meta.ClosedAt != nil || !firstMoveBlocks(meta, senderID) {
		return nil, ErrOpeningAnswerNotAllowed
	}
	return s.SendMessage(withFirstMoveBypass(ctx), senderID, convID, "text", text, nil, nil, idempotencyKey)
}
