package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

// --- Membership ---

// JoinGroup allows a user to join a group based on its join_mode.
//
// The gate runs in card mode: a private group the caller is not in answers
// as missing, and a banned caller is refused before any join path.
func (s *Service) JoinGroup(ctx context.Context, actorID, groupID uuid.UUID) (string, error) {
	acc, err := s.authorize(ctx, groupID, actorID, accessCard)
	if err != nil {
		return "", err
	}
	g := acc.g

	// Check if banned
	if isBanned(acc.member) {
		return "", ErrGroupBanned
	}

	// Audit HG6: the max-members cap is enforced atomically inside the
	// store's AddMemberWithMax under a row-level lock, so two
	// concurrent joins can no longer both pass a stale precheck and
	// push the group over its limit.

	switch g.JoinMode {
	case "open":
		if err := s.store.AddMemberWithMax(ctx, groupID, actorID, "member", g.MaxMembers); err != nil {
			return "", err
		}
		if g.ChatConversationID != nil {
			if err := s.syncMemberToChat(ctx, *g.ChatConversationID, actorID, true); err != nil {
				return "", err
			}
		}
		s.publishEvent(func() error {
			return s.producer.PublishGroupMemberJoined(ctx, groupID, actorID, "member")
		})
		s.invalidateGroupCache(ctx, groupID)
		return "joined", nil

	case "request":
		// Rate limit: 20 join requests/hr
		if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:join_req:%s", actorID), 20, time.Hour) {
			return "", fmt.Errorf("rate_limited: too many join requests")
		}
		// Check if already a member
		if isActive(acc.member) {
			return "", fmt.Errorf("already a member of this group")
		}
		jr := &store.GroupJoinRequest{
			GroupID: groupID,
			UserID:  actorID,
		}
		if err := s.store.CreateJoinRequest(ctx, jr); err != nil {
			return "", err
		}
		s.store.IncrementPendingRequestCount(ctx, groupID)
		s.publishEvent(func() error {
			return s.producer.PublishGroupJoinRequested(ctx, groupID, actorID, jr.ID)
		})
		return "request_pending", nil

	case "invite_only":
		return "", fmt.Errorf("forbidden: this group is invite-only")

	default:
		// Fallback: check old visibility field
		if g.Visibility == "private" {
			return "", fmt.Errorf("forbidden: private groups require an invite")
		}
		if err := s.store.AddMember(ctx, groupID, actorID, "member"); err != nil {
			return "", err
		}
		if g.ChatConversationID != nil {
			if err := s.syncMemberToChat(ctx, *g.ChatConversationID, actorID, true); err != nil {
				return "", err
			}
		}
		s.publishEvent(func() error {
			return s.producer.PublishGroupMemberJoined(ctx, groupID, actorID, "member")
		})
		s.invalidateGroupCache(ctx, groupID)
		return "joined", nil
	}
}

// ErrCreatorCannotLeave: groups.creator_id is the owner's authority and the
// group has no ownership transfer, so the creator leaving would leave a group
// whose owner is not in it (G-L2). Deleting or archiving is their way out.
var ErrCreatorCannotLeave = errors.New("forbidden: the group owner cannot leave; delete or archive the group instead")

// LeaveGroup allows a user to leave a group.
//
// A banned member has no active row, so they cannot leave: the ban row stays
// and keeps them out (RemoveMember only touches active rows).
func (s *Service) LeaveGroup(ctx context.Context, actorID, groupID uuid.UUID) error {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return ErrGroupNotFound
	}
	if g.CreatorID == actorID {
		return ErrCreatorCannotLeave
	}

	member, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if member == nil {
		return fmt.Errorf("not a member of this group")
	}

	if member.Role == "admin" {
		// One EXISTS over every member, not a scan of the first 100 (G-L2).
		otherAdmin, err := s.store.HasOtherActiveAdmin(ctx, groupID, actorID)
		if err != nil {
			return err
		}
		if !otherAdmin {
			return fmt.Errorf("must transfer admin role first")
		}
	}

	if g.ChatConversationID != nil {
		if err := s.syncMemberToChat(ctx, *g.ChatConversationID, actorID, false); err != nil {
			return err
		}
	}

	if err := s.store.RemoveMember(ctx, groupID, actorID); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupMemberLeft(ctx, groupID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	s.invalidateMembershipCache(ctx, groupID, actorID)
	return nil
}

// ErrMemberListHidden: member_list_visible=false keeps the list to the group's
// moderators and above (G-L1).
var ErrMemberListHidden = errors.New("forbidden: this group's member list is visible to its moderators only")

// memberListAllowed applies member_list_visible to the caller's rank.
func memberListAllowed(g *store.Group, rank int) bool {
	return g.MemberListVisible || rank >= rankModerator
}

// ListMembers returns the members of a group.
func (s *Service) ListMembers(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupMember, error) {
	acc, err := s.authorize(ctx, groupID, actorID, accessRead)
	if err != nil {
		return nil, err
	}
	g := acc.g
	if !memberListAllowed(g, acc.rank) {
		return nil, ErrMemberListHidden
	}

	members, err := s.store.ListMembers(ctx, groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	// The creator is stored as an admin; the Members tab should still call
	// them the owner, exactly as the cover does.
	for i := range members {
		if members[i].Status == "active" {
			members[i].Role = displayRole(members[i].Role, members[i].UserID == g.CreatorID)
		}
	}
	return members, nil
}

// assignableRoles are the roles UpdateMemberRole may set. "banned" is a
// status, set only by BanMember.
var assignableRoles = map[string]bool{"admin": true, "moderator": true, "member": true}

// targetRank is a member's standing for a moderation decision.
func targetRank(g *store.Group, target *store.GroupMember) int {
	return memberRank(g, target.UserID, target)
}

// UpdateMemberRole changes a member's role. Admins may change members and
// moderators below them; only the owner makes or unmakes admins; nobody
// changes the owner or themselves (G-M2).
func (s *Service) UpdateMemberRole(ctx context.Context, actorID, groupID, targetID uuid.UUID, role string) error {
	if !assignableRoles[role] {
		return fmt.Errorf("invalid role %q: must be admin, moderator or member", role)
	}

	acc, err := s.authorizeRank(ctx, groupID, actorID, rankAdmin,
		errors.New("forbidden: only admins can update member roles"))
	if err != nil {
		return err
	}

	target, err := s.store.GetActiveMember(ctx, groupID, targetID)
	if err != nil {
		return err
	}
	if target == nil {
		return fmt.Errorf("target user is not a member")
	}

	if err := guardRoleChange(acc.rank, targetRank(acc.g, target), roleRank(role), actorID, targetID); err != nil {
		return err
	}

	oldRole := target.Role
	if err := s.store.UpdateMemberRole(ctx, groupID, targetID, role); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupMemberRoleChanged(ctx, groupID, targetID, actorID, oldRole, role)
	})

	return nil
}

// RemoveMember kicks a member from the group.
func (s *Service) RemoveMember(ctx context.Context, actorID, groupID, targetID uuid.UUID) error {
	refusal := errors.New("forbidden: only admins or moderators can remove members")
	acc, err := s.authorizeRank(ctx, groupID, actorID, rankModerator, refusal)
	if err != nil {
		return err
	}

	target, err := s.store.GetActiveMember(ctx, groupID, targetID)
	if err != nil {
		return err
	}
	if target == nil {
		return fmt.Errorf("target user is not a member")
	}

	if err := guardMemberModeration(acc.rank, targetRank(acc.g, target), actorID, targetID, refusal); err != nil {
		return err
	}

	g := acc.g
	if g.ChatConversationID != nil {
		if err := s.syncMemberToChat(ctx, *g.ChatConversationID, targetID, false); err != nil {
			return err
		}
	}

	if err := s.store.KickMember(ctx, groupID, targetID, actorID); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupMemberRemoved(ctx, groupID, targetID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	s.invalidateMembershipCache(ctx, groupID, targetID)
	return nil
}

// BanMember bans a member from the group.
func (s *Service) BanMember(ctx context.Context, actorID, groupID, targetID uuid.UUID, reason string) error {
	refusal := errors.New("forbidden: only admins or moderators can ban members")
	acc, err := s.authorizeRank(ctx, groupID, actorID, rankModerator, refusal)
	if err != nil {
		return err
	}

	target, err := s.store.GetActiveMember(ctx, groupID, targetID)
	if err != nil {
		return err
	}
	if target == nil {
		return fmt.Errorf("target user is not a member")
	}

	// Role hierarchy: nobody bans themselves or the owner, and only a
	// strictly higher role bans (G-M2).
	if err := guardMemberModeration(acc.rank, targetRank(acc.g, target), actorID, targetID, refusal); err != nil {
		return err
	}

	g := acc.g
	if g.ChatConversationID != nil {
		if err := s.syncMemberToChat(ctx, *g.ChatConversationID, targetID, false); err != nil {
			return err
		}
	}

	if reason != "" {
		if err := s.store.BanMemberWithReason(ctx, groupID, targetID, actorID, reason); err != nil {
			return err
		}
	} else {
		if err := s.store.BanMember(ctx, groupID, targetID, actorID); err != nil {
			return err
		}
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupMemberBanned(ctx, groupID, targetID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	s.invalidateMembershipCache(ctx, groupID, targetID)
	return nil
}

// bannerRank is the CURRENT standing of whoever placed a ban: the owner
// through creator_id, otherwise their active role, otherwise none (they have
// since left, so their ban carries no rank of its own).
func (s *Service) bannerRank(ctx context.Context, g *store.Group, ban *store.GroupMember) (int, error) {
	if ban.RemovedByUserID == nil {
		return rankNone, nil
	}
	bannerID := *ban.RemovedByUserID
	if bannerID == g.CreatorID {
		return rankOwner, nil
	}
	banner, err := s.store.GetActiveMember(ctx, g.ID, bannerID)
	if err != nil {
		return rankNone, err
	}
	return memberRank(g, bannerID, banner), nil
}

// UnbanMember lifts a ban on a member. Admins and the owner only, and an admin
// cannot lift a ban the owner placed (G-M2).
func (s *Service) UnbanMember(ctx context.Context, actorID, groupID, targetID uuid.UUID) error {
	acc, err := s.authorizeRank(ctx, groupID, actorID, rankAdmin,
		errors.New("forbidden: only admins or the owner can unban members"))
	if err != nil {
		return err
	}

	ban, err := s.store.GetMember(ctx, groupID, targetID)
	if err != nil {
		return err
	}
	if !isBanned(ban) {
		return fmt.Errorf("user is not banned from this group")
	}

	placedBy, err := s.bannerRank(ctx, acc.g, ban)
	if err != nil {
		return err
	}
	if err := guardUnban(acc.rank, placedBy); err != nil {
		return err
	}

	if err := s.store.UnbanMember(ctx, groupID, targetID); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishMemberBanLifted(ctx, groupID, targetID, actorID)
	})

	return nil
}

// ListBannedMembers returns banned members for admin/owner view.
func (s *Service) ListBannedMembers(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupMember, error) {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankAdmin,
		errors.New("forbidden: only admins or the owner can view banned members")); err != nil {
		return nil, err
	}

	return s.store.ListBannedMembers(ctx, groupID, limit, offset)
}

// --- Member Stats ---

// GetMemberStats returns activity stats for a member in a group.
func (s *Service) GetMemberStats(ctx context.Context, actorID, groupID, userID uuid.UUID) (*store.MemberStats, error) {
	if _, err := s.authorize(ctx, groupID, actorID, accessRead); err != nil {
		return nil, err
	}

	stats, err := s.store.GetMemberStats(ctx, groupID, userID)
	if err != nil {
		return nil, err
	}
	if stats == nil {
		// Return zero stats instead of nil for convenience
		return &store.MemberStats{GroupID: groupID, UserID: userID}, nil
	}
	return stats, nil
}

// GetTopContributors returns top contributors for a group.
func (s *Service) GetTopContributors(ctx context.Context, actorID, groupID uuid.UUID, limit int) ([]store.MemberStats, error) {
	if _, err := s.authorize(ctx, groupID, actorID, accessRead); err != nil {
		return nil, err
	}

	return s.store.GetTopContributors(ctx, groupID, limit)
}
