package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Dating mechanic M9 — in-match extras: read receipts for members who opted
// in with a pass, and calls only once both members have written. See
// store/postgres/dating_extras.go for the rules.

// DatingMatchOptions are the rules dating-service sets when it creates a
// match's conversation.
type DatingMatchOptions struct {
	// FirstMovers (M5): who may send the first message; empty = anyone.
	FirstMovers []uuid.UUID
	// ReceiptsGated (M9): read receipts only for members dating allows.
	ReceiptsGated bool
	// CallAfterExchange (M9): calls only once both members have written.
	CallAfterExchange bool
}

type datingExtrasStore interface {
	SetDatingConversationRules(ctx context.Context, conversationID uuid.UUID, receiptsGated, callAfterExchange bool) error
	MarkMemberSent(ctx context.Context, conversationID, userID uuid.UUID) error
	SetDatingReceiptsUntil(ctx context.Context, matchID, userID uuid.UUID, until *time.Time) (bool, error)
	DatingReceiptsAllowed(ctx context.Context, conversationID, viewerID uuid.UUID) (bool, error)
}

func (s *Service) datingExtras() (datingExtrasStore, error) {
	st, ok := s.convStore.(datingExtrasStore)
	if !ok {
		return nil, fmt.Errorf("conversation store cannot hold dating conversation rules")
	}
	return st, nil
}

// SetDatingReceiptsUntil records until when userID may see read receipts in
// matchID's conversation. ErrDatingConversationNotFound when the match has no
// conversation with that member.
func (s *Service) SetDatingReceiptsUntil(ctx context.Context, matchID, userID uuid.UUID, until *time.Time) error {
	st, err := s.datingExtras()
	if err != nil {
		return err
	}
	found, err := st.SetDatingReceiptsUntil(ctx, matchID, userID, until)
	if err != nil {
		return err
	}
	if !found {
		return ErrDatingConversationNotFound
	}
	return nil
}

// datingReceiptsAllowed is DatingReceiptsAllowed; a lookup error withholds
// the receipt (disclosure fails closed).
func (s *Service) datingReceiptsAllowed(ctx context.Context, conversationID, viewerID uuid.UUID) bool {
	st, err := s.datingExtras()
	if err != nil {
		return false
	}
	ok, err := st.DatingReceiptsAllowed(ctx, conversationID, viewerID)
	if err != nil {
		s.log.Warn("dating receipts lookup failed; withholding", "err", err, "conversation_id", conversationID)
		return false
	}
	return ok
}
