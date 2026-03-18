package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// NotificationPreferences stores granular per-user notification settings.
type NotificationPreferences struct {
	UserID              string  `json:"user_id"`
	PushEnabled         bool    `json:"push_enabled"`
	EmailEnabled        bool    `json:"email_enabled"`
	QuietHoursEnabled   bool    `json:"quiet_hours_enabled"`
	QuietHoursStart     *string `json:"quiet_hours_start,omitempty"`
	QuietHoursEnd       *string `json:"quiet_hours_end,omitempty"`
	QuietHoursTZ        *string `json:"quiet_hours_tz,omitempty"`
	PushLikes           bool    `json:"push_likes"`
	PushSuperLikes      bool    `json:"push_super_likes"`
	PushComments        bool    `json:"push_comments"`
	PushReplies         bool    `json:"push_replies"`
	PushMentions        bool    `json:"push_mentions"`
	PushFollows         bool    `json:"push_follows"`
	PushFriendRequests  bool    `json:"push_friend_requests"`
	PushGroupPosts      bool    `json:"push_group_posts"`
	PushGroupMentions   bool    `json:"push_group_mentions"`
	PushChannelUpdates  bool    `json:"push_channel_updates"`
	PushChannelUrgent   bool    `json:"push_channel_urgent"`
	PushCommunityPosts  bool    `json:"push_community_posts"`
	PushCommunityMentions bool  `json:"push_community_mentions"`
	PushEventReminders  bool    `json:"push_event_reminders"`
	PushSystem          bool    `json:"push_system"`
	EmailDigest         string  `json:"email_digest"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// GetNotificationPreferences returns the v2 notification preferences for a user.
// Returns sensible defaults if no row exists (upsert pattern).
func (s *Store) GetNotificationPreferences(ctx context.Context, userID string) (*NotificationPreferences, error) {
	var p NotificationPreferences
	err := s.db.QueryRow(ctx, `
		SELECT user_id, push_enabled, email_enabled, quiet_hours_enabled,
			quiet_hours_start, quiet_hours_end, quiet_hours_tz,
			push_likes, push_super_likes, push_comments, push_replies,
			push_mentions, push_follows, push_friend_requests,
			push_group_posts, push_group_mentions,
			push_channel_updates, push_channel_urgent,
			push_community_posts, push_community_mentions,
			push_event_reminders, push_system,
			email_digest, updated_at
		FROM notification_preferences
		WHERE user_id = $1
	`, userID).Scan(
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
		if err == pgx.ErrNoRows {
			return defaultPreferencesV2(userID), nil
		}
		return nil, err
	}
	return &p, nil
}

// UpdateNotificationPreferences upserts v2 notification preferences.
func (s *Store) UpdateNotificationPreferences(ctx context.Context, p *NotificationPreferences) error {
	p.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO notification_preferences (
			user_id, push_enabled, email_enabled, quiet_hours_enabled,
			quiet_hours_start, quiet_hours_end, quiet_hours_tz,
			push_likes, push_super_likes, push_comments, push_replies,
			push_mentions, push_follows, push_friend_requests,
			push_group_posts, push_group_mentions,
			push_channel_updates, push_channel_urgent,
			push_community_posts, push_community_mentions,
			push_event_reminders, push_system,
			email_digest, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
			$15, $16, $17, $18, $19, $20, $21, $22, $23, $24
		)
		ON CONFLICT (user_id) DO UPDATE SET
			push_enabled = $2, email_enabled = $3, quiet_hours_enabled = $4,
			quiet_hours_start = $5, quiet_hours_end = $6, quiet_hours_tz = $7,
			push_likes = $8, push_super_likes = $9, push_comments = $10, push_replies = $11,
			push_mentions = $12, push_follows = $13, push_friend_requests = $14,
			push_group_posts = $15, push_group_mentions = $16,
			push_channel_updates = $17, push_channel_urgent = $18,
			push_community_posts = $19, push_community_mentions = $20,
			push_event_reminders = $21, push_system = $22,
			email_digest = $23, updated_at = $24
	`, p.UserID, p.PushEnabled, p.EmailEnabled, p.QuietHoursEnabled,
		p.QuietHoursStart, p.QuietHoursEnd, p.QuietHoursTZ,
		p.PushLikes, p.PushSuperLikes, p.PushComments, p.PushReplies,
		p.PushMentions, p.PushFollows, p.PushFriendRequests,
		p.PushGroupPosts, p.PushGroupMentions,
		p.PushChannelUpdates, p.PushChannelUrgent,
		p.PushCommunityPosts, p.PushCommunityMentions,
		p.PushEventReminders, p.PushSystem,
		p.EmailDigest, p.UpdatedAt,
	)
	return err
}

func defaultPreferencesV2(userID string) *NotificationPreferences {
	return &NotificationPreferences{
		UserID:              userID,
		PushEnabled:         true,
		EmailEnabled:        false,
		QuietHoursEnabled:   false,
		PushLikes:           false,
		PushSuperLikes:      true,
		PushComments:        true,
		PushReplies:         true,
		PushMentions:        true,
		PushFollows:         true,
		PushFriendRequests:  true,
		PushGroupPosts:      true,
		PushGroupMentions:   true,
		PushChannelUpdates:  true,
		PushChannelUrgent:   true,
		PushCommunityPosts:  false,
		PushCommunityMentions: true,
		PushEventReminders:  true,
		PushSystem:          true,
		EmailDigest:         "weekly",
		UpdatedAt:           time.Now(),
	}
}
