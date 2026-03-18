package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"strconv"
	"time"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	scyllaStore *scylla.NotificationStore
	pgStore     *postgres.Store
	rdb         *redis.Client
	pusher      push.Pusher
}

func New(scyllaStore *scylla.NotificationStore, rdb *redis.Client) *Service {
	return &Service{
		scyllaStore: scyllaStore,
		rdb:         rdb,
	}
}

func (s *Service) SetPGStore(pg *postgres.Store) {
	s.pgStore = pg
}

// SetPusher sets the push notification dispatcher.
func (s *Service) SetPusher(p push.Pusher) {
	s.pusher = p
}

// CreateNotification
func (s *Service) CreateNotification(ctx context.Context, userID, actorID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time) error {
	id := uuid.New()

	// 1. Save to Scylla (Inbox)
	n := &scylla.Notification{
		UserID:         userID,
		NotificationID: id,
		Type:           notifType,
		ActorUserID:    actorID,
		EntityType:     entityType,
		EntityID:       entityID,
		DeepLink:       deepLink,
		IsRead:         false,
		CreatedAt:      createdAt,
	}

	if err := s.scyllaStore.CreateNotification(ctx, n); err != nil {
		return fmt.Errorf("failed to create notification in scylla: %w", err)
	}

	// 2. Push to Redis (Realtime)
	// Channel: notify:{user_id}
	channel := fmt.Sprintf("notify:%s", userID.String())
	payload, _ := json.Marshal(map[string]interface{}{
		"type":    "notification",
		"payload": n,
	})

	if err := s.rdb.Publish(ctx, channel, payload).Err(); err != nil {
		// Log error but don't fail the operation
		fmt.Printf("failed to publish to redis: %v\n", err)
	}

	// 3. Send push notification if pusher is configured
	if s.pusher != nil && s.pgStore != nil {
		tokens, err := s.pgStore.GetUserDevices(ctx, userID)
		if err == nil && len(tokens) > 0 {
			prefs, _ := s.pgStore.GetPreferences(ctx, userID)
			quietStart := ""
			quietEnd := ""
			if prefs != nil {
				if prefs.QuietHoursStart != nil {
					quietStart = *prefs.QuietHoursStart
				}
				if prefs.QuietHoursEnd != nil {
					quietEnd = *prefs.QuietHoursEnd
				}
				if prefs.PushEnabled && !isQuietHours(quietStart, quietEnd) {
					title, body := notifTitleBody(notifType)
					pushData := map[string]string{"type": notifType}
					// Compute collapse key so repeated notifications (e.g. many likes)
					// replace each other on the device instead of flooding.
					if ck := GetCollapseKey(notifType, entityID.String(), userID.String()); ck != "" {
						pushData["collapse_key"] = ck
					}
					for _, t := range tokens {
						if err := s.pusher.Send(ctx, t.PushToken, t.Platform, title, body, pushData); err != nil {
							slog.Warn("push: send failed", "error", err, "platform", t.Platform)
						}
					}
				}
			}
		}
	}

	return nil
}

// notifTitleBody returns a human-readable title and body for a notification type.
func notifTitleBody(notifType string) (string, string) {
	switch notifType {
	case "follow":
		return "New Follower", "Someone followed you"
	case "reaction":
		return "New Reaction", "Someone reacted to your post"
	case "comment":
		return "New Comment", "Someone commented on your post"
	case "comment_reaction":
		return "New Reaction", "Someone reacted to your comment"
	case "friend_request":
		return "Friend Request", "You have a new friend request"
	case "friend_accepted":
		return "Friend Request Accepted", "Your friend request was accepted"
	case "endorsement":
		return "New Endorsement", "Someone endorsed you"
	case "business_review":
		return "New Review", "Your business page has a new review"
	case "new_subscriber":
		return "New Subscriber", "Someone subscribed to your content"
	case "mention":
		return "You were mentioned", "Someone mentioned you in a post"
	default:
		return "New Notification", "You have a new notification"
	}
}

// isQuietHours returns true if the current time is within the quiet hours range.
func isQuietHours(start, end string) bool {
	if start == "" || end == "" {
		return false
	}
	// TODO: implement proper quiet hours check using HH:MM format
	return false
}

// NotificationsPage holds a page of notifications with a cursor for the next page.
type NotificationsPage struct {
	Items      []scylla.Notification `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

// GetNotifications returns notifications without cursor (legacy).
func (s *Service) GetNotifications(ctx context.Context, userID uuid.UUID, limit int) ([]scylla.Notification, error) {
	return s.scyllaStore.GetNotifications(ctx, userID, limit)
}

// GetNotificationsPage returns a cursor-paginated page of notifications.
// Cursor format: "bucket:timeuuid" (e.g. "202603:550e8400-e29b-41d4-a716-446655440000").
func (s *Service) GetNotificationsPage(ctx context.Context, userID uuid.UUID, limit int, cursor string) (*NotificationsPage, error) {
	var cursorBucket int
	var cursorTS *gocql.UUID

	if cursor != "" {
		parts := splitCursor(cursor)
		if len(parts) == 2 {
			if b, err := strconv.Atoi(parts[0]); err == nil {
				cursorBucket = b
			}
			if ts, err := gocql.ParseUUID(parts[1]); err == nil {
				cursorTS = &ts
			}
		}
	}

	notifs, err := s.scyllaStore.GetNotificationsWithCursor(ctx, userID, cursorBucket, cursorTS, limit+1)
	if err != nil {
		return nil, err
	}

	page := &NotificationsPage{}
	if len(notifs) > limit {
		page.Items = notifs[:limit]
		last := notifs[limit-1]
		page.NextCursor = fmt.Sprintf("%d:%s", last.Bucket, last.TS.String())
	} else {
		page.Items = notifs
	}

	return page, nil
}

// splitCursor splits "bucket:timeuuid" on the first colon.
func splitCursor(cursor string) []string {
	idx := -1
	for i, c := range cursor {
		if c == ':' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	return []string{cursor[:idx], cursor[idx+1:]}
}

// MarkRead marks a single notification as read and decrements the unread counter.
func (s *Service) MarkRead(ctx context.Context, userID uuid.UUID, bucket int, ts string) error {
	tsUUID, err := gocql.ParseUUID(ts)
	if err != nil {
		return err
	}
	if err := s.scyllaStore.MarkRead(ctx, userID, bucket, tsUUID); err != nil {
		return err
	}
	// Decrement unread counter in Redis
	key := fmt.Sprintf("unread:%s", userID.String())
	s.rdb.Decr(ctx, key)
	return nil
}

// GetUnreadCount returns the count of unread notifications from Redis.
func (s *Service) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int64, error) {
	key := fmt.Sprintf("unread:%s", userID.String())
	count, err := s.rdb.Get(ctx, key).Int64()
	if err != nil {
		if err.Error() == "redis: nil" {
			// Recompute from Scylla
			notifs, err := s.scyllaStore.GetNotifications(ctx, userID, 100)
			if err != nil {
				return 0, err
			}
			var unread int64
			for _, n := range notifs {
				if !n.IsRead {
					unread++
				}
			}
			s.rdb.Set(ctx, key, unread, time.Minute)
			return unread, nil
		}
		return 0, err
	}
	return count, nil
}

// MarkAllRead marks all notifications as read for a user.
func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID) error {
	notifs, err := s.scyllaStore.GetNotifications(ctx, userID, 200)
	if err != nil {
		return err
	}
	for _, n := range notifs {
		if !n.IsRead {
			if err := s.scyllaStore.MarkRead(ctx, userID, n.Bucket, n.TS); err != nil {
				log.Printf("Warning: failed to mark notification read: %v", err)
			}
		}
	}
	// Reset unread counter
	key := fmt.Sprintf("unread:%s", userID.String())
	s.rdb.Set(ctx, key, 0, time.Minute)
	return nil
}

// DeleteNotification removes a notification.
func (s *Service) DeleteNotification(ctx context.Context, userID uuid.UUID, bucket int, ts string) error {
	tsUUID, err := gocql.ParseUUID(ts)
	if err != nil {
		return err
	}
	return s.scyllaStore.DeleteNotification(ctx, userID, bucket, tsUUID)
}

// GetPreferences returns legacy notification preferences for a user.
func (s *Service) GetPreferences(ctx context.Context, userID uuid.UUID) (*postgres.NotificationPreferencesLegacy, error) {
	if s.pgStore == nil {
		return &postgres.NotificationPreferencesLegacy{UserID: userID, EmailEnabled: true, PushEnabled: true}, nil
	}
	return s.pgStore.GetPreferences(ctx, userID)
}

// UpdatePreferences updates legacy notification preferences for a user.
func (s *Service) UpdatePreferences(ctx context.Context, prefs *postgres.NotificationPreferencesLegacy) error {
	if s.pgStore == nil {
		return fmt.Errorf("PG store not configured")
	}
	return s.pgStore.UpsertPreferences(ctx, prefs)
}

// RegisterDevice registers a push notification device.
func (s *Service) RegisterDevice(ctx context.Context, userID uuid.UUID, platform, pushToken string) (*postgres.UserDevice, error) {
	if s.pgStore == nil {
		return nil, fmt.Errorf("PG store not configured")
	}
	return s.pgStore.RegisterDevice(ctx, userID, platform, pushToken)
}

// UnregisterDevice removes a registered device.
func (s *Service) UnregisterDevice(ctx context.Context, deviceID, userID uuid.UUID) error {
	if s.pgStore == nil {
		return fmt.Errorf("PG store not configured")
	}
	return s.pgStore.UnregisterDevice(ctx, deviceID, userID)
}

// GetNotifPreferences returns the granular v2 notification preferences for a user.
func (s *Service) GetNotifPreferences(ctx context.Context, userID string) (*postgres.NotificationPreferences, error) {
	if s.pgStore == nil {
		return nil, fmt.Errorf("PG store not configured")
	}
	return s.pgStore.GetNotificationPreferences(ctx, userID)
}

// UpdateNotifPreferences updates the granular v2 notification preferences for a user.
func (s *Service) UpdateNotifPreferences(ctx context.Context, prefs *postgres.NotificationPreferences) error {
	if s.pgStore == nil {
		return fmt.Errorf("PG store not configured")
	}
	return s.pgStore.UpdateNotificationPreferences(ctx, prefs)
}

// DeleteNotificationsForUser removes all notifications for the given user (GDPR erasure).
func (s *Service) DeleteNotificationsForUser(ctx context.Context, userID uuid.UUID) error {
	return s.scyllaStore.DeleteNotificationsForUser(ctx, userID)
}

// DeactivateDeviceTokens deactivates all push-notification device tokens for the given user (GDPR erasure).
func (s *Service) DeactivateDeviceTokens(ctx context.Context, userID uuid.UUID) error {
	if s.pgStore == nil {
		return fmt.Errorf("PG store not configured")
	}
	return s.pgStore.DeactivateDeviceTokens(ctx, userID)
}
