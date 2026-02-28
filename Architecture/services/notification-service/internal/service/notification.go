package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/facebook-like/notification-service/internal/store/postgres"
	"github.com/facebook-like/notification-service/internal/store/scylla"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	scyllaStore *scylla.NotificationStore
	pgStore     *postgres.Store
	rdb         *redis.Client
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

	return nil
}

// GetNotifications
func (s *Service) GetNotifications(ctx context.Context, userID uuid.UUID, limit int) ([]scylla.Notification, error) {
	return s.scyllaStore.GetNotifications(ctx, userID, limit)
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

// GetPreferences returns notification preferences for a user.
func (s *Service) GetPreferences(ctx context.Context, userID uuid.UUID) (*postgres.NotificationPreferences, error) {
	if s.pgStore == nil {
		return &postgres.NotificationPreferences{UserID: userID, EmailEnabled: true, PushEnabled: true}, nil
	}
	return s.pgStore.GetPreferences(ctx, userID)
}

// UpdatePreferences updates notification preferences for a user.
func (s *Service) UpdatePreferences(ctx context.Context, prefs *postgres.NotificationPreferences) error {
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
