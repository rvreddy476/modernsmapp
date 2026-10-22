package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/chat-message-service/internal/store/postgres"
)

// A group newcomer sees only the messages sent after they join, as on
// WhatsApp (founder, 22 Oct 2026). At the top of that shortened history the
// client shows one small line saying how they got in:
//
//	invite_link  "You joined using this group's invite link · 22 Oct"
//	added        "<adder> added you · 22 Oct" ("Someone" when unknown)
//	invitation   "You joined · 22 Oct"
//
// my_join on GET /v1/chat/conversations/:id carries what the line needs.

// MyJoin is the viewer's own join, on the wire as `my_join`.
type MyJoin struct {
	JoinedAt time.Time `json:"joined_at"`
	// invite_link | added | invitation ("created" is never sent: the
	// creator has the whole history and no note).
	Method        string     `json:"method"`
	AddedByUserID *uuid.UUID `json:"added_by_user_id,omitempty"`
}

// memberJoinReader is the optional store read behind my_join; a store
// without it (older fakes) simply yields no note.
type memberJoinReader interface {
	GetMemberJoin(ctx context.Context, conversationID, userID uuid.UUID) (*postgres.MemberJoin, error)
}

// attachMyJoin fills resp.MyJoin for the viewer. Best effort: a failed read
// drops the note, never the conversation.
func (s *Service) attachMyJoin(ctx context.Context, viewerID uuid.UUID, resp *ConversationResponse) {
	if resp == nil || resp.Type != "group" {
		return
	}
	reader, ok := s.convStore.(memberJoinReader)
	if !ok {
		return
	}
	row, err := reader.GetMemberJoin(ctx, resp.ID, viewerID)
	if err != nil {
		s.log.Warn("member join read failed — join note dropped", "err", err, "conversation_id", resp.ID)
		return
	}
	resp.MyJoin = myJoinFor(viewerID, resp.Type, resp.CreatedBy, row)
}

// myJoinFor is the rule: a note only for a group member whose visible
// history was cut at their own join by a known way in. No note for the
// creator, for a member with full history (no cutoff), for a pre-migration
// row (no method), or when the cutoff came from something else — a later
// "clear chat" moves cleared_before past joined_at, which AddMemberCapped
// stamps with the same NOW().
func myJoinFor(viewerID uuid.UUID, convType string, createdBy *uuid.UUID, row *postgres.MemberJoin) *MyJoin {
	if convType != "group" || row == nil || row.ClearedBefore == nil || row.JoinMethod == nil {
		return nil
	}
	if createdBy != nil && *createdBy == viewerID {
		return nil
	}
	switch *row.JoinMethod {
	case postgres.JoinMethodInviteLink, postgres.JoinMethodAdded, postgres.JoinMethodInvitation:
	default:
		return nil
	}
	if !row.ClearedBefore.Equal(row.JoinedAt) {
		return nil
	}
	out := &MyJoin{JoinedAt: row.JoinedAt, Method: *row.JoinMethod}
	if row.AddedBy != nil && *row.JoinMethod != postgres.JoinMethodInviteLink {
		by := *row.AddedBy
		out.AddedByUserID = &by
	}
	return out
}
