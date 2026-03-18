package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// DeliveryDecision determines which delivery channels to use for a notification.
type DeliveryDecision struct {
	CreateInbox   bool // always true unless blocked by mute
	SendWebSocket bool // send real-time via WS
	SendPush      bool // send push notification
	SendEmail     bool // send email digest/alert
	DeferPush     bool // queue for after quiet hours end
}

// ResolveDelivery implements the 5-level preference resolution tree:
//
//  1. Critical events override everything.
//  2. Global push toggle — if off, no push for anything.
//  3. Quiet hours — defer push (don't drop) unless critical/system.
//  4. Per-category toggle — check the specific event type toggle.
//  5. Context mute — if muted, suppress everything unless template overrides.
//
// Mention and announcement overrides are applied after the main chain.
func ResolveDelivery(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, recipientID, eventType, contextType, contextID string, isMuted bool) DeliveryDecision {
	template := GetTemplate(eventType)
	decision := DeliveryDecision{
		CreateInbox:   true,
		SendWebSocket: true,
		SendPush:      template.PushEligible,
		SendEmail:     false,
	}

	// Critical events override everything — always push.
	if template.Priority == "critical" {
		decision.SendPush = true
		return decision
	}

	// Load user preferences from the v2 table.
	prefs, err := loadNotifPreferences(ctx, db, recipientID)
	if err != nil {
		slog.Warn("preference resolution: failed to load prefs, using template defaults",
			"user", recipientID, "error", err)
		return decision
	}

	// Level 1: Global push disabled.
	if !prefs.PushEnabled {
		decision.SendPush = false
	}

	// Level 2: Quiet hours — defer push, don't drop.
	if decision.SendPush && prefs.QuietHoursEnabled && isInQuietHours(prefs) {
		// Security/system alerts ignore quiet hours.
		if template.Priority != "critical" && template.EventType != "system.login_alert" {
			decision.SendPush = false
			decision.DeferPush = true
		}
	}

	// Level 3: Per-category toggle.
	if decision.SendPush {
		decision.SendPush = checkCategoryToggle(prefs, eventType)
	}

	// Level 4: Context mute check.
	if isMuted && !template.OverrideMute {
		decision.CreateInbox = false
		decision.SendWebSocket = false
		decision.SendPush = false
		decision.DeferPush = false
		return decision
	}

	// Mention/announcement overrides — re-enable delivery channels.
	if template.OverridePrefs {
		decision.SendPush = template.PushEligible
		decision.SendWebSocket = true
		decision.CreateInbox = true
	}
	if template.OverrideMute {
		decision.CreateInbox = true
		decision.SendWebSocket = true
	}

	// Email for high/critical priority when email is enabled.
	if prefs.EmailEnabled && (template.Priority == "high" || template.Priority == "critical") {
		decision.SendEmail = true
	}

	return decision
}

// loadNotifPreferences queries the notification_preferences table.
// On error or missing row, returns sensible defaults.
func loadNotifPreferences(ctx context.Context, db *pgxpool.Pool, userID string) (*postgres.NotificationPreferences, error) {
	var p postgres.NotificationPreferences
	err := db.QueryRow(ctx, `SELECT
		user_id, push_enabled, email_enabled, quiet_hours_enabled,
		quiet_hours_start, quiet_hours_end, quiet_hours_tz,
		push_likes, push_super_likes, push_comments, push_replies,
		push_mentions, push_follows, push_friend_requests,
		push_group_posts, push_group_mentions,
		push_channel_updates, push_channel_urgent,
		push_community_posts, push_community_mentions,
		push_event_reminders, push_system,
		email_digest, updated_at
		FROM notification_preferences WHERE user_id = $1`, userID).Scan(
		&p.UserID, &p.PushEnabled, &p.EmailEnabled, &p.QuietHoursEnabled,
		&p.QuietHoursStart, &p.QuietHoursEnd, &p.QuietHoursTZ,
		&p.PushLikes, &p.PushSuperLikes, &p.PushComments, &p.PushReplies,
		&p.PushMentions, &p.PushFollows, &p.PushFriendRequests,
		&p.PushGroupPosts, &p.PushGroupMentions,
		&p.PushChannelUpdates, &p.PushChannelUrgent,
		&p.PushCommunityPosts, &p.PushCommunityMentions,
		&p.PushEventReminders, &p.PushSystem,
		&p.EmailDigest, &p.UpdatedAt,
	)
	if err != nil {
		// Return defaults — likes off, system on, etc.
		return &postgres.NotificationPreferences{
			UserID:                userID,
			PushEnabled:           true,
			EmailEnabled:          false,
			PushLikes:             false,
			PushSuperLikes:        true,
			PushComments:          true,
			PushReplies:           true,
			PushMentions:          true,
			PushFollows:           true,
			PushFriendRequests:    true,
			PushGroupPosts:        true,
			PushGroupMentions:     true,
			PushChannelUpdates:    true,
			PushChannelUrgent:     true,
			PushCommunityPosts:    false,
			PushCommunityMentions: true,
			PushEventReminders:    true,
			PushSystem:            true,
		}, nil
	}
	return &p, nil
}

// isInQuietHours checks whether the current moment falls within the user's quiet window.
// Handles cross-midnight ranges (e.g. 22:00–07:00).
func isInQuietHours(p *postgres.NotificationPreferences) bool {
	if p.QuietHoursStart == nil || p.QuietHoursEnd == nil {
		return false
	}

	tz := "UTC"
	if p.QuietHoursTZ != nil {
		tz = *p.QuietHoursTZ
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}

	now := time.Now().In(loc)
	h, m, _ := now.Clock()
	nowMins := h*60 + m

	startMins := parseTimeMins(*p.QuietHoursStart)
	endMins := parseTimeMins(*p.QuietHoursEnd)

	if startMins < 0 || endMins < 0 {
		return false
	}

	if startMins < endMins {
		// Same-day range (e.g. 08:00–17:00).
		return nowMins >= startMins && nowMins < endMins
	}
	// Cross-midnight range (e.g. 22:00–07:00).
	return nowMins >= startMins || nowMins < endMins
}

// parseTimeMins parses "HH:MM" into minutes since midnight. Returns -1 on failure.
func parseTimeMins(s string) int {
	if len(s) < 5 {
		return -1
	}
	t, err := time.Parse("15:04", s[:5])
	if err != nil {
		return -1
	}
	return t.Hour()*60 + t.Minute()
}

// checkCategoryToggle checks whether the per-category push toggle allows this event type.
func checkCategoryToggle(p *postgres.NotificationPreferences, eventType string) bool {
	switch eventType {
	case "post.liked":
		return p.PushLikes
	case "post.super_liked":
		return p.PushSuperLikes
	case "post.commented", "comment.replied":
		return p.PushComments
	case "mention.created", "group.mention", "community.mention":
		return p.PushMentions
	case "post.shared":
		return p.PushFollows // reuse follows toggle for shares
	case "user.followed":
		return p.PushFollows
	case "user.friend_request", "user.friend_accepted":
		return p.PushFriendRequests
	case "group.post.published", "group.post.submitted", "group.post.approved", "group.post.rejected",
		"group.announcement", "group.member.joined", "group.invite.received",
		"group.join_request", "group.join_approved", "group.poll.created":
		return p.PushGroupPosts
	case "group.event.created", "group.event.reminder":
		return p.PushEventReminders
	case "channel.update.published", "channel.event.created", "channel.event.reminder":
		return p.PushChannelUpdates
	case "channel.urgent.info", "channel.urgent.warning", "channel.urgent.critical":
		return p.PushChannelUrgent
	case "community.post.published", "community.answer_accepted", "community.expert_answer",
		"community.invite", "community.join_approved", "community.announcement":
		return p.PushCommunityPosts
	case "system.login_alert", "system.verification", "system.report_result":
		return p.PushSystem
	default:
		return true
	}
}
