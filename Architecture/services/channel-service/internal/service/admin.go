package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
)

// Admin console (Wave 2 — Content, Chat): the actions behind admin-service's
// token family. The actor is the signed act claim; every write is audited by
// the store in the same transaction.

// Admin decisions on a report.
const (
	DecisionUphold  = "uphold"
	DecisionDismiss = "dismiss"
)

// adminReasonMaxRunes bounds a decision or suspension reason.
const adminReasonMaxRunes = 1000

func adminReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", fmt.Errorf("invalid: reason is required")
	}
	if utf8.RuneCountInString(reason) > adminReasonMaxRunes {
		return "", fmt.Errorf("invalid: reason must be at most %d characters", adminReasonMaxRunes)
	}
	return reason, nil
}

// AdminListReports is the queue read (same keyset as the key route).
func (s *Service) AdminListReports(ctx context.Context, status string, limit int, cursor string) ([]store.ReportListItem, string, error) {
	return s.ListReports(ctx, status, limit, cursor)
}

// AdminGetReport reads one report.
func (s *Service) AdminGetReport(ctx context.Context, reportID uuid.UUID) (*store.ReportListItem, error) {
	return s.store.AdminGetReport(ctx, reportID)
}

// AdminDecideReport upholds or dismisses an open report. Upholding records
// the report as reviewed; it does not suspend the channel — that is its own
// action with its own permission.
func (s *Service) AdminDecideReport(ctx context.Context, actor, reportID uuid.UUID, decision, reason string) (*store.ReportListItem, error) {
	var status string
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case DecisionUphold:
		status = "reviewed"
	case DecisionDismiss:
		status = "dismissed"
	default:
		return nil, fmt.Errorf("invalid: decision must be uphold or dismiss")
	}
	reason, err := adminReason(reason)
	if err != nil {
		return nil, err
	}
	return s.store.AdminDecideReport(ctx, actor, reportID, status, reason)
}

// AdminSuspendChannel suspends an active channel.
func (s *Service) AdminSuspendChannel(ctx context.Context, actor, channelID uuid.UUID, reason string) (*store.ChannelStatusChange, error) {
	return s.adminSetSuspended(ctx, actor, channelID, true, reason)
}

// AdminUnsuspendChannel restores a suspended channel.
func (s *Service) AdminUnsuspendChannel(ctx context.Context, actor, channelID uuid.UUID, reason string) (*store.ChannelStatusChange, error) {
	return s.adminSetSuspended(ctx, actor, channelID, false, reason)
}

func (s *Service) adminSetSuspended(ctx context.Context, actor, channelID uuid.UUID, suspend bool, reason string) (*store.ChannelStatusChange, error) {
	reason, err := adminReason(reason)
	if err != nil {
		return nil, err
	}
	out, err := s.store.AdminSetChannelSuspended(ctx, actor, channelID, suspend, reason)
	if err != nil {
		return nil, err
	}
	// Same reason as setChannelStatus: the 60 s meta cache would otherwise
	// keep a suspended channel visible.
	s.invalidateChannelMeta(ctx, channelID)
	slog.Warn("channel status changed by admin console",
		"channel_id", channelID, "status", out.Status, "actor_id", actor)
	return out, nil
}

// AdminStats reads the dashboard counts.
func (s *Service) AdminStats(ctx context.Context) (*store.AdminStats, error) {
	return s.store.AdminStats(ctx)
}
