package service

// Stream moderation v2 (1 Oct 2026): remove a message, ban / unban, the
// host's moderators, and viewer reports.

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// Roles in moderation events.
const (
	RoleHost      = "host"
	RoleModerator = "moderator"
	RoleAdmin     = "admin"
)

// requireHostOrModerator loads the stream and returns the actor's role.
func (s *Service) requireHostOrModerator(ctx context.Context, streamID, actorID uuid.UUID) (*postgres.LiveStream, string, error) {
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, "", mapStoreErr(err)
	}
	if actorID != uuid.Nil && actorID == st.CreatorUserID {
		return st, RoleHost, nil
	}
	if actorID == uuid.Nil {
		return nil, "", ErrNotModerator
	}
	isMod, err := s.store.IsModerator(ctx, streamID, actorID)
	if err != nil {
		return nil, "", err
	}
	if !isMod {
		return nil, "", ErrNotModerator
	}
	return st, RoleModerator, nil
}

// RemoveChatMessage hides a message for everyone (host or moderator) and
// publishes chat.removed. Removing a removed message is a no-op success.
func (s *Service) RemoveChatMessage(ctx context.Context, streamID, actorID, messageID uuid.UUID) error {
	_, role, err := s.requireHostOrModerator(ctx, streamID, actorID)
	if err != nil {
		return err
	}
	removed, err := s.store.RemoveChatMessage(ctx, streamID, messageID, actorID)
	if errors.Is(err, postgres.ErrNotFound) {
		return ErrMessageNotFound
	}
	if err != nil {
		return err
	}
	if removed {
		s.publishChatRemoved(ctx, streamID, messageID, role)
	}
	return nil
}

func (s *Service) publishChatRemoved(ctx context.Context, streamID, messageID uuid.UUID, role string) {
	s.publish(ctx, streamID, EventChatRemoved, map[string]any{
		"stream_id":  streamID.String(),
		"message_id": messageID.String(),
		"by_role":    role,
	})
}

func validReason(reason string, required bool) (string, error) {
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 500 || (required && reason == "") {
		return "", ErrReasonRequired
	}
	return reason, nil
}

// Ban bans targetID from the stream (host or moderator): no chat, no viewer
// token, no room subscription, and an open LiveKit connection is dropped.
// Nobody can ban the host; only the host can ban a moderator.
func (s *Service) Ban(ctx context.Context, streamID, actorID, targetID uuid.UUID, reason string) error {
	st, role, err := s.requireHostOrModerator(ctx, streamID, actorID)
	if err != nil {
		return err
	}
	if targetID == uuid.Nil || targetID == st.CreatorUserID || targetID == actorID {
		return ErrInvalidTarget
	}
	reason, err = validReason(reason, false)
	if err != nil {
		return err
	}
	targetIsMod, err := s.store.IsModerator(ctx, streamID, targetID)
	if err != nil {
		return err
	}
	if targetIsMod && role == RoleModerator {
		return ErrNotCreator
	}
	if err := s.store.BanFromStream(ctx, streamID, targetID, actorID, reason); err != nil {
		return err
	}
	s.kick(ctx, st, targetID)
	s.publish(ctx, streamID, EventModerationBan, map[string]any{
		"stream_id": streamID.String(),
		"user_id":   targetID.String(),
		"by_role":   role,
	})
	if targetIsMod {
		// The ban dropped a moderator seat: clients re-badge from the new list.
		if mods, err := s.store.ListModerators(ctx, streamID); err != nil {
			slog.Warn("live-v2: list moderators after ban", "stream_id", streamID, "err", err)
		} else {
			s.publishModerators(ctx, streamID, mods)
		}
	}
	return nil
}

// kick drops the user's live connection, best-effort.
func (s *Service) kick(ctx context.Context, st *postgres.LiveStream, userID uuid.UUID) {
	if s.livekit == nil || !onAir(st.Status) {
		return
	}
	if err := s.livekit.RemoveParticipant(ctx, st.LiveKitRoom, userID.String()); err != nil {
		slog.Warn("live-v2: remove banned participant", "stream_id", st.ID, "err", err)
	}
}

// Unban lifts a stream ban (host or moderator).
func (s *Service) Unban(ctx context.Context, streamID, actorID, targetID uuid.UUID) error {
	_, role, err := s.requireHostOrModerator(ctx, streamID, actorID)
	if err != nil {
		return err
	}
	if err := s.store.UnbanFromStream(ctx, streamID, targetID); err != nil {
		return err
	}
	s.publish(ctx, streamID, EventModerationUnban, map[string]any{
		"stream_id": streamID.String(),
		"user_id":   targetID.String(),
		"by_role":   role,
	})
	return nil
}

// ListBans lists the stream's bans (host or moderator).
func (s *Service) ListBans(ctx context.Context, streamID, actorID uuid.UUID) ([]postgres.StreamBan, error) {
	if _, _, err := s.requireHostOrModerator(ctx, streamID, actorID); err != nil {
		return nil, err
	}
	return s.store.ListStreamBans(ctx, streamID)
}

// SetModerators replaces the stream's moderators (host only, at most 5,
// never the host, never a banned user).
func (s *Service) SetModerators(ctx context.Context, streamID, hostID uuid.UUID, userIDs []uuid.UUID) ([]uuid.UUID, error) {
	st, err := s.requireCreator(ctx, streamID, hostID)
	if err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]bool{}
	ids := make([]uuid.UUID, 0, len(userIDs))
	for _, id := range userIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if id == uuid.Nil || id == st.CreatorUserID {
			return nil, ErrInvalidTarget
		}
		ids = append(ids, id)
	}
	if len(ids) > MaxModerators {
		return nil, ErrTooManyModerators
	}
	for _, id := range ids {
		banned, err := s.store.IsBannedFromStream(ctx, streamID, id)
		if err != nil {
			return nil, err
		}
		if banned {
			return nil, ErrInvalidTarget
		}
	}
	if err := s.store.ReplaceModerators(ctx, streamID, ids, hostID); err != nil {
		return nil, err
	}
	out, err := s.store.ListModerators(ctx, streamID)
	if err != nil {
		return nil, err
	}
	s.publishModerators(ctx, streamID, out)
	return out, nil
}

// publishModerators sends moderation.moderators with the full user_ids list
// (PUT /moderators, and a ban that drops a moderator seat).
func (s *Service) publishModerators(ctx context.Context, streamID uuid.UUID, ids []uuid.UUID) {
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = id.String()
	}
	s.publish(ctx, streamID, EventModerationModerators, map[string]any{
		"stream_id": streamID.String(),
		"user_ids":  strs,
	})
}

// ListModerators returns the moderators to anyone who passes the viewer
// gate (clients badge them in chat).
func (s *Service) ListModerators(ctx context.Context, streamID, viewerID uuid.UUID) ([]uuid.UUID, error) {
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if err := s.canSee(ctx, st, viewerID); err != nil {
		return nil, err
	}
	return s.store.ListModerators(ctx, streamID)
}

// ReportReasons is the closed set of report reasons.
var ReportReasons = map[string]bool{
	"spam": true, "harassment": true, "hate": true, "nudity": true,
	"violence": true, "scam": true, "other": true,
}

// ReportInput is a viewer report.
type ReportInput struct {
	Reason    string
	MessageID *uuid.UUID
	Note      string
}

// Report files a viewer report about the stream or one of its messages.
// Anyone who can see the stream may report (a stream ban does not take that
// away), once per target, rate limited. Nobody reports themselves.
func (s *Service) Report(ctx context.Context, streamID, reporterID uuid.UUID, in ReportInput) (*postgres.Report, error) {
	if reporterID == uuid.Nil {
		return nil, ErrInvalidTarget
	}
	reason := strings.ToLower(strings.TrimSpace(in.Reason))
	if !ReportReasons[reason] {
		return nil, ErrInvalidReportReason
	}
	note := strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(note) > 500 {
		return nil, ErrInvalidNote
	}
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if err := s.canSee(ctx, st, reporterID); err != nil {
		return nil, err
	}
	target := st.CreatorUserID
	if in.MessageID != nil {
		msg, err := s.store.GetChatMessage(ctx, streamID, *in.MessageID)
		if errors.Is(err, postgres.ErrNotFound) {
			return nil, ErrMessageNotFound
		}
		if err != nil {
			return nil, err
		}
		target = msg.UserID
	}
	if target == reporterID {
		return nil, ErrInvalidTarget
	}
	rep, err := s.store.CreateReport(ctx, postgres.NewReport{
		StreamID:     streamID,
		ReporterID:   reporterID,
		MessageID:    in.MessageID,
		TargetUserID: target,
		Reason:       reason,
		Note:         note,
	}, reportsPerWindow, reportWindow)
	switch {
	case errors.Is(err, postgres.ErrDuplicate):
		return nil, ErrAlreadyReported
	case errors.Is(err, postgres.ErrReportRateLimited):
		return nil, ErrReportRateLimited
	case err != nil:
		return nil, err
	}
	return rep, nil
}
