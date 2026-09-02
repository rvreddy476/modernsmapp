package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NotificationPreferences stores granular per-user notification settings.
//
// Every category has TWO toggles (migration 005, TikTok-style split):
//   - push_*  — device push for that category
//   - inapp_* — the durable inbox row + realtime WS event for that category
//
// Turning inapp_* off means the notification simply does not exist for the
// user; turning only push_* off keeps it in the inbox but the device stays
// quiet.
type NotificationPreferences struct {
	UserID            string  `json:"user_id"`
	PushEnabled       bool    `json:"push_enabled"`
	EmailEnabled      bool    `json:"email_enabled"`
	QuietHoursEnabled bool    `json:"quiet_hours_enabled"`
	QuietHoursStart   *string `json:"quiet_hours_start,omitempty"`
	QuietHoursEnd     *string `json:"quiet_hours_end,omitempty"`
	QuietHoursTZ      *string `json:"quiet_hours_tz,omitempty"`

	PushLikes             bool `json:"push_likes"`
	PushSuperLikes        bool `json:"push_super_likes"`
	PushComments          bool `json:"push_comments"`
	PushReplies           bool `json:"push_replies"`
	PushMentions          bool `json:"push_mentions"`
	PushFollows           bool `json:"push_follows"`
	PushFriendRequests    bool `json:"push_friend_requests"`
	PushGroupPosts        bool `json:"push_group_posts"`
	PushGroupMentions     bool `json:"push_group_mentions"`
	PushChannelUpdates    bool `json:"push_channel_updates"`
	PushChannelUrgent     bool `json:"push_channel_urgent"`
	PushCommunityPosts    bool `json:"push_community_posts"`
	PushCommunityMentions bool `json:"push_community_mentions"`
	PushEventReminders    bool `json:"push_event_reminders"`
	PushSystem            bool `json:"push_system"`
	PushReposts           bool `json:"push_reposts"`
	PushLive              bool `json:"push_live"`
	PushMessages          bool `json:"push_messages"`

	InappLikes             bool `json:"inapp_likes"`
	InappSuperLikes        bool `json:"inapp_super_likes"`
	InappComments          bool `json:"inapp_comments"`
	InappReplies           bool `json:"inapp_replies"`
	InappMentions          bool `json:"inapp_mentions"`
	InappFollows           bool `json:"inapp_follows"`
	InappFriendRequests    bool `json:"inapp_friend_requests"`
	InappGroupPosts        bool `json:"inapp_group_posts"`
	InappGroupMentions     bool `json:"inapp_group_mentions"`
	InappChannelUpdates    bool `json:"inapp_channel_updates"`
	InappChannelUrgent     bool `json:"inapp_channel_urgent"`
	InappCommunityPosts    bool `json:"inapp_community_posts"`
	InappCommunityMentions bool `json:"inapp_community_mentions"`
	InappEventReminders    bool `json:"inapp_event_reminders"`
	InappSystem            bool `json:"inapp_system"`
	InappReposts           bool `json:"inapp_reposts"`
	InappLive              bool `json:"inapp_live"`
	InappMessages          bool `json:"inapp_messages"`

	EmailDigest string    `json:"email_digest"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// notifPrefsColumns is the canonical column list, shared by SELECT and
// INSERT so the two can never drift. Order is load-bearing: it matches
// scanNotifPrefs and the upsert parameter list.
const notifPrefsColumns = `user_id, push_enabled, email_enabled, quiet_hours_enabled,
	quiet_hours_start, quiet_hours_end, quiet_hours_tz,
	push_likes, push_super_likes, push_comments, push_replies,
	push_mentions, push_follows, push_friend_requests,
	push_group_posts, push_group_mentions,
	push_channel_updates, push_channel_urgent,
	push_community_posts, push_community_mentions,
	push_event_reminders, push_system,
	push_reposts, push_live, push_messages,
	inapp_likes, inapp_super_likes, inapp_comments, inapp_replies,
	inapp_mentions, inapp_follows, inapp_friend_requests,
	inapp_group_posts, inapp_group_mentions,
	inapp_channel_updates, inapp_channel_urgent,
	inapp_community_posts, inapp_community_mentions,
	inapp_event_reminders, inapp_system,
	inapp_reposts, inapp_live, inapp_messages,
	email_digest, updated_at`

// scanNotifPrefs returns scan destinations in notifPrefsColumns order.
func scanNotifPrefs(p *NotificationPreferences) []any {
	return []any{
		&p.UserID, &p.PushEnabled, &p.EmailEnabled, &p.QuietHoursEnabled,
		&p.QuietHoursStart, &p.QuietHoursEnd, &p.QuietHoursTZ,
		&p.PushLikes, &p.PushSuperLikes, &p.PushComments, &p.PushReplies,
		&p.PushMentions, &p.PushFollows, &p.PushFriendRequests,
		&p.PushGroupPosts, &p.PushGroupMentions,
		&p.PushChannelUpdates, &p.PushChannelUrgent,
		&p.PushCommunityPosts, &p.PushCommunityMentions,
		&p.PushEventReminders, &p.PushSystem,
		&p.PushReposts, &p.PushLive, &p.PushMessages,
		&p.InappLikes, &p.InappSuperLikes, &p.InappComments, &p.InappReplies,
		&p.InappMentions, &p.InappFollows, &p.InappFriendRequests,
		&p.InappGroupPosts, &p.InappGroupMentions,
		&p.InappChannelUpdates, &p.InappChannelUrgent,
		&p.InappCommunityPosts, &p.InappCommunityMentions,
		&p.InappEventReminders, &p.InappSystem,
		&p.InappReposts, &p.InappLive, &p.InappMessages,
		&p.EmailDigest, &p.UpdatedAt,
	}
}

// notifPrefsValues returns bind parameters in notifPrefsColumns order.
func notifPrefsValues(p *NotificationPreferences) []any {
	return []any{
		p.UserID, p.PushEnabled, p.EmailEnabled, p.QuietHoursEnabled,
		p.QuietHoursStart, p.QuietHoursEnd, p.QuietHoursTZ,
		p.PushLikes, p.PushSuperLikes, p.PushComments, p.PushReplies,
		p.PushMentions, p.PushFollows, p.PushFriendRequests,
		p.PushGroupPosts, p.PushGroupMentions,
		p.PushChannelUpdates, p.PushChannelUrgent,
		p.PushCommunityPosts, p.PushCommunityMentions,
		p.PushEventReminders, p.PushSystem,
		p.PushReposts, p.PushLive, p.PushMessages,
		p.InappLikes, p.InappSuperLikes, p.InappComments, p.InappReplies,
		p.InappMentions, p.InappFollows, p.InappFriendRequests,
		p.InappGroupPosts, p.InappGroupMentions,
		p.InappChannelUpdates, p.InappChannelUrgent,
		p.InappCommunityPosts, p.InappCommunityMentions,
		p.InappEventReminders, p.InappSystem,
		p.InappReposts, p.InappLive, p.InappMessages,
		p.EmailDigest, p.UpdatedAt,
	}
}

// notifPrefsUpsertSQL is built once from the shared column list so the
// INSERT can never drift from the SELECT.
var notifPrefsUpsertSQL = buildNotifPrefsUpsertSQL()

func buildNotifPrefsUpsertSQL() string {
	var cols []string
	for _, c := range strings.Split(notifPrefsColumns, ",") {
		cols = append(cols, strings.TrimSpace(c))
	}
	params := make([]string, len(cols))
	var updates []string
	for i, c := range cols {
		params[i] = fmt.Sprintf("$%d", i+1)
		if c != "user_id" {
			updates = append(updates, fmt.Sprintf("%s = $%d", c, i+1))
		}
	}
	return fmt.Sprintf(
		"INSERT INTO notification_preferences (%s) VALUES (%s) ON CONFLICT (user_id) DO UPDATE SET %s",
		strings.Join(cols, ", "), strings.Join(params, ", "), strings.Join(updates, ", "),
	)
}

// GetNotificationPreferences returns the v2 notification preferences for a user.
// Returns sensible defaults if no row exists (upsert pattern).
func (s *Store) GetNotificationPreferences(ctx context.Context, userID string) (*NotificationPreferences, error) {
	return QueryNotificationPreferences(ctx, s.db, userID)
}

// QueryNotificationPreferences is the pool-level query, shared with the
// delivery-resolution path in the service layer (which holds a bare pool).
func QueryNotificationPreferences(ctx context.Context, db *pgxpool.Pool, userID string) (*NotificationPreferences, error) {
	var p NotificationPreferences
	err := db.QueryRow(ctx,
		"SELECT "+notifPrefsColumns+" FROM notification_preferences WHERE user_id = $1",
		userID,
	).Scan(scanNotifPrefs(&p)...)
	if err != nil {
		if err == pgx.ErrNoRows {
			return DefaultNotificationPreferences(userID), nil
		}
		return nil, err
	}
	return &p, nil
}

// UpdateNotificationPreferences upserts v2 notification preferences.
func (s *Store) UpdateNotificationPreferences(ctx context.Context, p *NotificationPreferences) error {
	p.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, notifPrefsUpsertSQL, notifPrefsValues(p)...)
	return err
}

// DefaultNotificationPreferences mirrors the column defaults in migrations
// 003 + 005: everything on except community-posts push; like pushes are ON
// (TikTok parity, 005); ALL in-app toggles default on.
func DefaultNotificationPreferences(userID string) *NotificationPreferences {
	return &NotificationPreferences{
		UserID:            userID,
		PushEnabled:       true,
		EmailEnabled:      false,
		QuietHoursEnabled: false,

		PushLikes:             true,
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
		PushReposts:           true,
		PushLive:              true,
		PushMessages:          true,

		InappLikes:             true,
		InappSuperLikes:        true,
		InappComments:          true,
		InappReplies:           true,
		InappMentions:          true,
		InappFollows:           true,
		InappFriendRequests:    true,
		InappGroupPosts:        true,
		InappGroupMentions:     true,
		InappChannelUpdates:    true,
		InappChannelUrgent:     true,
		InappCommunityPosts:    true,
		InappCommunityMentions: true,
		InappEventReminders:    true,
		InappSystem:            true,
		InappReposts:           true,
		InappLive:              true,
		InappMessages:          true,

		EmailDigest: "weekly",
		UpdatedAt:   time.Now(),
	}
}
