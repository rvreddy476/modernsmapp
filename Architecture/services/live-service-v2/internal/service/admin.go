package service

// Admin actions (1 Oct 2026), reached only through the admin-service token
// family (internal/http/admin_token.go). Every write records an append-only
// live_admin_audit row in the same transaction as the change.

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// Audit actions.
const (
	AuditStreamStop    = "stream.stop"
	AuditReportResolve = "report.resolve"
	AuditChatRemove    = "chat.remove"
	AuditUserLiveBan   = "user.live_ban"
	AuditUserLiveUnban = "user.live_unban"
)

// Report resolutions.
const (
	ResolveDismiss       = "dismiss"
	ResolveRemoveMessage = "remove_message"
	ResolveBanUser       = "ban_user"
)

// AdminListStreams lists streams for the console by status: live,
// reconnecting, starting, or all (= every on-air status). Empty = all.
func (s *Service) AdminListStreams(ctx context.Context, status string, limit int) ([]*postgres.AdminStream, error) {
	var statuses []string
	switch status {
	case "", "all":
		statuses = []string{stStarting, stLive, stReconnecting}
	case stLive, stReconnecting, stStarting:
		statuses = []string{status}
	default:
		return nil, ErrInvalidStatusFilter
	}
	return s.store.ListByStatuses(ctx, statuses, limit)
}

// AdminListReports lists reports by status: open (default), resolved, all.
func (s *Service) AdminListReports(ctx context.Context, status string, limit int) ([]*postgres.Report, error) {
	switch status {
	case "":
		status = "open"
	case "open", "resolved", "all":
	default:
		return nil, ErrInvalidStatusFilter
	}
	return s.store.ListReports(ctx, status, limit)
}

// AdminStopResult is the stop answer.
type AdminStopResult struct {
	Stream     *postgres.LiveStream `json:"stream"`
	RoomClosed bool                 `json:"room_closed"`
}

// AdminStopStream closes the LiveKit room and ends the stream with
// ended_reason admin_stopped (audited). The status is written even when
// LiveKit could not be reached — room_closed says whether it was.
func (s *Service) AdminStopStream(ctx context.Context, actor, streamID uuid.UUID, reason string) (*AdminStopResult, error) {
	reason, err := validReason(reason, true)
	if err != nil {
		return nil, err
	}
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if postgres.IsTerminal(st.Status) {
		return nil, ErrStateConflict
	}
	return s.adminStop(ctx, actor, st, reason, nil)
}

func (s *Service) adminStop(ctx context.Context, actor uuid.UUID, st *postgres.LiveStream, reason string, detail map[string]any) (*AdminStopResult, error) {
	closed := false
	if s.livekit != nil {
		if err := s.livekit.DeleteRoom(ctx, st.LiveKitRoom); err != nil {
			slog.Warn("live-v2 admin stop: close room", "stream_id", st.ID, "err", err)
		} else {
			closed = true
		}
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["room_closed"] = closed
	detail["host_user_id"] = st.CreatorUserID.String()
	res, err := s.transition(ctx, st.ID, TrigAdminStop, &postgres.AuditEntry{
		ActorID: actor, Action: AuditStreamStop, TargetType: "stream", TargetID: st.ID.String(),
		Reason: reason, Detail: marshalDetail(detail),
	})
	if err != nil {
		return nil, err
	}
	return &AdminStopResult{Stream: res.Next, RoomClosed: closed}, nil
}

// AdminResolveInput is a report resolution.
type AdminResolveInput struct {
	Action string
	Reason string
}

// ResolvePermissionExtra is the extra admin permission an action needs on
// top of live:reports.act (the HTTP layer checks it on the token).
func ResolvePermissionExtra(action string) string {
	switch action {
	case ResolveBanUser:
		return "live:users.ban"
	case ResolveRemoveMessage:
		return "live:chat.moderate"
	}
	return ""
}

// AdminResolveReport closes an open report: dismiss, remove the reported
// message, or ban the reported message's author from that stream. A report
// about the stream itself has no message, so it can only be dismissed (a
// host is live-banned through the live-ban route).
func (s *Service) AdminResolveReport(ctx context.Context, actor, reportID uuid.UUID, in AdminResolveInput) (*postgres.Report, error) {
	switch in.Action {
	case ResolveDismiss, ResolveRemoveMessage, ResolveBanUser:
	default:
		return nil, ErrInvalidAction
	}
	reason, err := validReason(in.Reason, true)
	if err != nil {
		return nil, err
	}
	check := func(r *postgres.Report) error {
		if in.Action != ResolveDismiss && r.MessageID == nil {
			return ErrInvalidAction
		}
		return nil
	}
	rep, err := s.store.AdminResolveReport(ctx, reportID, postgres.ResolveAction{Action: in.Action, Reason: reason}, check,
		postgres.AuditEntry{
			ActorID: actor, Action: AuditReportResolve, TargetType: "report", TargetID: reportID.String(),
			Reason: reason, Detail: marshalDetail(map[string]any{"action": in.Action}),
		})
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		return nil, ErrReportNotFound
	case errors.Is(err, postgres.ErrReportResolved):
		return nil, ErrReportResolved
	case err != nil:
		return nil, err
	}
	switch in.Action {
	case ResolveRemoveMessage:
		s.publishChatRemoved(ctx, rep.StreamID, *rep.MessageID, RoleAdmin)
	case ResolveBanUser:
		if st, err := s.store.GetByID(ctx, rep.StreamID); err == nil {
			s.kick(ctx, st, rep.TargetUserID)
		}
		s.publish(ctx, rep.StreamID, EventModerationBan, map[string]any{
			"stream_id": rep.StreamID.String(),
			"user_id":   rep.TargetUserID.String(),
			"by_role":   RoleAdmin,
		})
	}
	return rep, nil
}

// AdminRemoveChatMessage removes a message (audited) and publishes
// chat.removed.
func (s *Service) AdminRemoveChatMessage(ctx context.Context, actor, streamID, messageID uuid.UUID, reason string) error {
	reason, err := validReason(reason, false)
	if err != nil {
		return err
	}
	removed, err := s.store.AdminRemoveChatMessage(ctx, streamID, messageID, postgres.AuditEntry{
		ActorID: actor, Action: AuditChatRemove, TargetType: "chat_message", TargetID: messageID.String(),
		Reason: reason, Detail: marshalDetail(map[string]any{"stream_id": streamID.String()}),
	})
	if errors.Is(err, postgres.ErrNotFound) {
		return ErrMessageNotFound
	}
	if err != nil {
		return err
	}
	if removed {
		s.publishChatRemoved(ctx, streamID, messageID, RoleAdmin)
	}
	return nil
}

// AdminLiveBanResult is the ban answer.
type AdminLiveBanResult struct {
	UserID         uuid.UUID   `json:"user_id"`
	Banned         bool        `json:"banned"`
	StoppedStreams []uuid.UUID `json:"stopped_streams"`
}

// AdminLiveBan bans the user from going live and chatting everywhere
// (audited) and stops any stream they have on air (each stop audited).
func (s *Service) AdminLiveBan(ctx context.Context, actor, userID uuid.UUID, reason string) (*AdminLiveBanResult, error) {
	if userID == uuid.Nil {
		return nil, ErrInvalidTarget
	}
	reason, err := validReason(reason, true)
	if err != nil {
		return nil, err
	}
	if err := s.store.AdminSetPlatformBan(ctx, userID, true, reason, postgres.AuditEntry{
		ActorID: actor, Action: AuditUserLiveBan, TargetType: "user", TargetID: userID.String(), Reason: reason,
	}); err != nil {
		return nil, err
	}
	out := &AdminLiveBanResult{UserID: userID, Banned: true, StoppedStreams: []uuid.UUID{}}
	ids, err := s.store.ActiveStreamsOf(ctx, userID)
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		st, err := s.store.GetByID(ctx, id)
		if err != nil {
			continue
		}
		if _, err := s.adminStop(ctx, actor, st, reason, map[string]any{"cause": "live_ban"}); err == nil {
			out.StoppedStreams = append(out.StoppedStreams, id)
		} else if !errors.Is(err, ErrStateConflict) {
			slog.Warn("live-v2 live ban: stop stream", "stream_id", id, "err", err)
		}
	}
	return out, nil
}

// AdminLiveUnban lifts a platform live ban (audited).
func (s *Service) AdminLiveUnban(ctx context.Context, actor, userID uuid.UUID, reason string) (*AdminLiveBanResult, error) {
	reason, err := validReason(reason, false)
	if err != nil {
		return nil, err
	}
	if err := s.store.AdminSetPlatformBan(ctx, userID, false, reason, postgres.AuditEntry{
		ActorID: actor, Action: AuditUserLiveUnban, TargetType: "user", TargetID: userID.String(), Reason: reason,
	}); err != nil {
		return nil, err
	}
	return &AdminLiveBanResult{UserID: userID, Banned: false, StoppedStreams: []uuid.UUID{}}, nil
}

// AdminBan is one platform live ban as the console lists it.
type AdminBan struct {
	UserID    uuid.UUID `json:"user_id"`
	Reason    string    `json:"reason"`
	BannedBy  uuid.UUID `json:"banned_by"`
	CreatedAt time.Time `json:"created_at"`
}

// AdminListLiveBans pages the platform live bans, newest first.
func (s *Service) AdminListLiveBans(ctx context.Context, limit, offset int) ([]AdminBan, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.store.ListPlatformBans(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]AdminBan, 0, len(rows))
	for _, b := range rows {
		out = append(out, AdminBan{UserID: b.UserID, Reason: b.Reason, BannedBy: b.BannedBy, CreatedAt: b.BannedAt})
	}
	return out, nil
}
