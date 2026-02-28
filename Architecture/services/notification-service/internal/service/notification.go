package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/facebook-like/notification-service/internal/store/scylla"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	scyllaStore *scylla.NotificationStore
	rdb         *redis.Client
}

func New(scylla *scylla.NotificationStore, rdb *redis.Client) *Service {
	return &Service{
		scyllaStore: scylla,
		rdb:         rdb,
	}
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

// MarkRead
func (s *Service) MarkRead(ctx context.Context, userID uuid.UUID, bucket int, ts string) error {
	tsUUID, err := gocql.ParseUUID(ts)
	if err != nil {
		return err
	}
	return s.scyllaStore.MarkRead(ctx, userID, bucket, tsUUID)
}
