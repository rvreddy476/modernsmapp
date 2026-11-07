// Membership changes on an existing conversation: adding and removing
// members (including the managed-group entry points used by other
// services) and renaming the conversation.

package service

import (
	"context"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// --- Member Management ---

func (s *Service) AddMember(ctx context.Context, userID, conversationID, targetUserID uuid.UUID) error {
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conv == nil {
		return notFound("conversation not found")
	}
	if conv.Type != "group" {
		return invalidInput("can only add members to group conversations")
	}

	role, err := s.convStore.GetMemberRole(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if role != "admin" {
		return forbidden("only admins can add members")
	}
	targetIsMember, err := s.convStore.CheckMembership(ctx, conversationID, targetUserID)
	if err != nil {
		return err
	}
	if targetIsMember {
		return conflict("user is already a conversation member")
	}

	// Blocker-2 final correction: even this legacy (currently unrouted)
	// method uses the conversation-locked writer, so a future re-wiring can
	// never reintroduce an unserialized membership add.
	adder := userID
	if err := s.groupStore().AddMemberCapped(ctx, conversationID, targetUserID, "member", GroupMemberCap,
		postgres.JoinOrigin{Method: postgres.JoinMethodAdded, AddedBy: &adder}); err != nil {
		return err
	}

	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MemberAdded, sharedEvents.MemberAddedPayload{
		ConversationID: conversationID.String(),
		UserID:         targetUserID.String(),
		AddedBy:        userID.String(),
		Role:           "member",
		AddedAt:        time.Now(),
	})

	return nil
}

// ManagedAddGroupMember is the narrow group-service reconciliation path.
// Authentication is enforced by the exact /internal route; group-service is
// authoritative for social-group membership, so chat must not re-evaluate a
// chat-admin role that can drift from it.
func (s *Service) ManagedAddGroupMember(ctx context.Context, conversationID, userID uuid.UUID) error {
	conversation, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conversation == nil || conversation.Type != "group" {
		return notFound("managed group conversation not found")
	}
	// Blocker-2 final correction: the legacy AddMember took no conversation
	// lock, so a managed add could race a removal outside the generation
	// serialization every other membership write obeys. AddMemberCapped is
	// the conversation-locked writer (and managed groups obey the same
	// launch cap as every group). The /internal route carries no actor, so
	// the newcomer's note reads "Someone added you".
	return s.groupStore().AddMemberCapped(ctx, conversationID, userID, "member", GroupMemberCap,
		postgres.JoinOrigin{Method: postgres.JoinMethodAdded})
}

func (s *Service) ManagedRemoveGroupMember(ctx context.Context, conversationID, userID uuid.UUID) error {
	conversation, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conversation == nil || conversation.Type != "group" {
		return notFound("managed group conversation not found")
	}
	// Final-verification P0-4: this path severed through the legacy store
	// and acknowledged success with no revocation, leaving the removed
	// member's room subscription and token live. Every sever now goes
	// through the one revocation protocol.
	return s.severMemberWithRevocation(ctx, conversationID, userID)
}

func (s *Service) RemoveMember(ctx context.Context, userID, conversationID, targetUserID uuid.UUID) error {
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conv == nil {
		return notFound("conversation not found")
	}
	if conv.Type != "group" {
		return invalidInput("can only remove members from group conversations")
	}
	requesterIsMember, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !requesterIsMember {
		return ErrNotMember
	}
	targetIsMember, err := s.convStore.CheckMembership(ctx, conversationID, targetUserID)
	if err != nil {
		return err
	}
	if !targetIsMember {
		return invalidInput("target user is not a conversation member")
	}
	targetRole, err := s.convStore.GetMemberRole(ctx, conversationID, targetUserID)
	if err != nil {
		return err
	}

	// Self-removal is always allowed; otherwise must be admin
	if userID != targetUserID {
		role, err := s.convStore.GetMemberRole(ctx, conversationID, userID)
		if err != nil {
			return err
		}
		if role != "admin" {
			return forbidden("only admins can remove other members")
		}
	}
	if targetRole == "admin" {
		members, err := s.convStore.GetMembers(ctx, conversationID)
		if err != nil {
			return err
		}
		adminCount := 0
		for _, member := range members {
			if member.Role == "admin" {
				adminCount++
			}
		}
		if adminCount <= 1 {
			return conflict("cannot remove the last admin")
		}
	}

	// Blocker-2 final correction: even this legacy (currently unrouted)
	// method severs through the revocation protocol, so a future re-wiring
	// can never reintroduce an unrevoked sever.
	if err := s.severMemberWithRevocation(ctx, conversationID, targetUserID); err != nil {
		return err
	}

	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MemberRemoved, sharedEvents.MemberRemovedPayload{
		ConversationID: conversationID.String(),
		UserID:         targetUserID.String(),
		RemovedBy:      userID.String(),
		RemovedAt:      time.Now(),
	})

	return nil
}

func (s *Service) UpdateTitle(ctx context.Context, userID, conversationID uuid.UUID, title string) error {
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conv == nil {
		return notFound("conversation not found")
	}
	if conv.Type != "group" {
		return invalidInput("can only update title of group conversations")
	}

	role, err := s.convStore.GetMemberRole(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if role != "admin" {
		return forbidden("only admins can update title")
	}

	return s.convStore.UpdateTitle(ctx, conversationID, title)
}
