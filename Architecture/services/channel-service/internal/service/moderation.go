package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
)

// Communities invite-only pilot (2026-09-12): the moderation controls the
// founder's decision requires.
//
// Before this pass the `banned` role was enforced on every read and
// engagement path but there was no route that could set it — store.BanMember
// and events.PublishChannelMemberBanned were both dead code. A reporter got
// 202 and nobody ever looked at channel_reports. Suspension was a CHECK
// value nothing wrote.

// guardMemberModeration is the pure authority rule for removing or banning
// a member. Roles are the channel_members values; ownerID is the channel's
// owner_id, which outranks any row.
//
//	only owner/admin may act
//	nobody may act on themselves (unsubscribe instead)
//	nobody may act on the owner
//	an admin may not act on another admin; the owner may
func guardMemberModeration(actorRole, targetRole string, actorID, targetID, ownerID uuid.UUID) error {
	// The owner's authority comes from owner_id, not from the member row,
	// because the owner row can be missing in older databases.
	if actorID == ownerID {
		actorRole = "owner"
	}
	if targetID == ownerID {
		targetRole = "owner"
	}
	if actorRole != "owner" && actorRole != "admin" {
		return fmt.Errorf("forbidden: only admins and above can remove or ban members")
	}
	if actorID == targetID {
		return ErrCannotModerateSelf
	}
	if targetRole == "owner" {
		return ErrCannotModerateOwner
	}
	if actorRole == "admin" && targetRole == "admin" {
		return ErrCannotModeratePeerAdmin
	}
	return nil
}

// loadForModeration resolves the channel plus both member rows and applies
// guardMemberModeration.
func (s *Service) loadForModeration(ctx context.Context, channelID, actorID, targetID uuid.UUID) (*store.BroadcastChannel, *store.ChannelMember, error) {
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, nil, err
	}
	if ch == nil {
		return nil, nil, ErrChannelNotFound
	}
	actor, err := s.store.GetMember(ctx, channelID, actorID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to check membership: %w", err)
	}
	actorRole := ""
	if actor != nil {
		actorRole = actor.Role
	}
	target, err := s.store.GetMember(ctx, channelID, targetID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to check membership: %w", err)
	}
	targetRole := ""
	if target != nil {
		targetRole = target.Role
	}
	if err := guardMemberModeration(actorRole, targetRole, actorID, targetID, ch.OwnerID); err != nil {
		return nil, nil, err
	}
	return ch, target, nil
}

// RemoveMember drops a member's subscription (owner/admin). The user may
// rejoin with a new invite — use BanMember to keep them out.
func (s *Service) RemoveMember(ctx context.Context, channelID, actorID, targetID uuid.UUID) error {
	_, target, err := s.loadForModeration(ctx, channelID, actorID, targetID)
	if err != nil {
		return err
	}
	if target == nil {
		return ErrNotAMember
	}
	if err := s.store.RemoveMember(ctx, channelID, targetID); err != nil {
		return fmt.Errorf("failed to remove member: %w", err)
	}
	// A banned row was already excluded from every count, so removing it
	// must not decrement again. Matches store.CountSubscribers.
	if target.Role != "banned" {
		if err := s.adjustSubscriberCount(ctx, channelID, -1); err != nil {
			slog.Warn("failed to decrement subscriber count", "error", err)
		}
	}
	if s.producer != nil {
		if err := s.producer.PublishChannelUnsubscribed(ctx, channelID, targetID); err != nil {
			slog.Warn("failed to publish channel.unsubscribed event", "error", err)
		}
	}
	return nil
}

// BanMember sets the enforced `banned` role (owner/admin) and emits
// channel.member.banned. Idempotent.
func (s *Service) BanMember(ctx context.Context, channelID, actorID, targetID uuid.UUID) error {
	_, target, err := s.loadForModeration(ctx, channelID, actorID, targetID)
	if err != nil {
		return err
	}
	if target == nil {
		return ErrNotAMember
	}
	if target.Role == "banned" {
		return nil // idempotent
	}
	if err := s.store.BanMember(ctx, channelID, targetID); err != nil {
		return fmt.Errorf("failed to ban member: %w", err)
	}
	// Every read counts non-banned rows only (store.CountSubscribers,
	// ListSubscribers, the fan-out roster), so the materialised count has
	// to shed the banned row too or it drifts upward forever.
	if err := s.adjustSubscriberCount(ctx, channelID, -1); err != nil {
		slog.Warn("failed to decrement subscriber count", "error", err)
	}
	if s.producer != nil {
		if err := s.producer.PublishChannelMemberBanned(ctx, channelID, targetID, actorID); err != nil {
			slog.Warn("failed to publish channel.member.banned event", "error", err)
		}
	}
	return nil
}

// UnbanMember returns a banned user to subscriber standing (owner/admin).
// Idempotent for a user who is not banned.
func (s *Service) UnbanMember(ctx context.Context, channelID, actorID, targetID uuid.UUID) error {
	_, target, err := s.loadForModeration(ctx, channelID, actorID, targetID)
	if err != nil {
		return err
	}
	if target == nil {
		return ErrNotAMember
	}
	if target.Role != "banned" {
		return nil // idempotent
	}
	lifted, err := s.store.UnbanMember(ctx, channelID, targetID)
	if err != nil {
		return fmt.Errorf("failed to unban member: %w", err)
	}
	if lifted {
		if err := s.adjustSubscriberCount(ctx, channelID, 1); err != nil {
			slog.Warn("failed to increment subscriber count", "error", err)
		}
	}
	return nil
}

// ListMembers is the moderator's roster view (owner/admin only, same gate as
// ListSubscribers). role "" lists everyone who is not banned — the legacy
// /subscribers behaviour; "all" lists every row including banned; any other
// value lists exactly that role, which is how a moderator finds who is
// banned. Every row carries its role.
func (s *Service) ListMembers(ctx context.Context, channelID, actorID uuid.UUID, role string, limit, offset int) ([]store.ChannelMember, error) {
	member, err := s.store.GetMember(ctx, channelID, actorID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "admin") {
		return nil, fmt.Errorf("forbidden: only admins and above can list subscribers")
	}
	switch role = strings.ToLower(strings.TrimSpace(role)); role {
	case "":
		return s.store.ListSubscribers(ctx, channelID, limit, offset)
	case "all":
		return s.store.ListAllMembers(ctx, channelID, limit, offset)
	default:
		if !validMemberRoles[role] {
			return nil, fmt.Errorf("invalid: role is not valid")
		}
		return s.store.ListMembersByRole(ctx, channelID, role, limit, offset)
	}
}

// --- Report review (internal key only; see internal/http/handler_moderation.go) ---

// ListReports pages the moderation queue newest-first. status "" defaults to
// "open"; "all" lists every state.
func (s *Service) ListReports(ctx context.Context, status string, limit int, cursor string) ([]store.ReportListItem, string, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "open"
	}
	if status != "all" && !store.ValidReportStatuses[status] {
		return nil, "", fmt.Errorf("invalid: status must be open, reviewed, dismissed or all")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	items, err := s.store.ListReports(ctx, status, limit, cursor)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) == limit {
		last := items[len(items)-1]
		next = store.EncodeReportCursor(last.CreatedAt, last.ID)
	}
	return items, next, nil
}

// ReviewReport writes the status that nothing wrote before, plus who
// reviewed it and when.
func (s *Service) ReviewReport(ctx context.Context, reportID, reviewerID uuid.UUID, status, note string) (*store.ChannelReport, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "reviewed" && status != "dismissed" {
		return nil, fmt.Errorf("invalid: status must be reviewed or dismissed")
	}
	note = strings.TrimSpace(note)
	if len(note) > reportDetailsMaxRunes {
		return nil, fmt.Errorf("invalid: note must be at most %d characters", reportDetailsMaxRunes)
	}
	r, err := s.store.ReviewReport(ctx, reportID, reviewerID, status, note)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("report not found")
	}
	return r, nil
}

// --- Emergency disable, one channel ---

// SuspendChannel writes the `suspended` status the CHECK already allowed and
// nothing ever wrote. A suspended channel disappears from /discover, GET
// /{id}, /my and the fan-out roster. Internal key only.
func (s *Service) SuspendChannel(ctx context.Context, channelID uuid.UUID, reason string) error {
	return s.setChannelStatus(ctx, channelID, "suspended", reason)
}

// UnsuspendChannel lifts a suspension. Internal key only.
func (s *Service) UnsuspendChannel(ctx context.Context, channelID uuid.UUID) error {
	return s.setChannelStatus(ctx, channelID, "active", "")
}

func (s *Service) setChannelStatus(ctx context.Context, channelID uuid.UUID, status, reason string) error {
	if err := s.store.SetChannelStatus(ctx, channelID, status); err != nil {
		return err
	}
	// cachedChannelByID caches the row for 60s with no invalidation, so
	// without this a suspension would not take effect for a minute — the
	// one thing an emergency switch must not do.
	s.invalidateChannelMeta(ctx, channelID)
	slog.Warn("channel status changed by moderation",
		"channel_id", channelID, "status", status, "reason", reason)
	return nil
}

// invalidateChannelMeta drops the cache-aside entry written by
// cachedChannelByID.
func (s *Service) invalidateChannelMeta(ctx context.Context, channelID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	if err := s.rdb.Del(ctx, "channel:meta:"+channelID.String()).Err(); err != nil {
		slog.Warn("failed to invalidate channel meta cache", "channel_id", channelID, "error", err)
	}
}
