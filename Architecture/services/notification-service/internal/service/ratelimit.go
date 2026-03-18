package service

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// MaxNotifPerSourcePerHour caps how many notifications a single actor can
	// generate per hour, preventing spam/abuse from a single user.
	MaxNotifPerSourcePerHour = 100

	// MaxPushPerRecipientPerHour caps how many push notifications a single
	// recipient receives per hour, preventing notification fatigue.
	MaxPushPerRecipientPerHour = 10

	// MaxFanoutPerSecond is the global fanout rate limit for the notification
	// service. This protects downstream push providers and Redis from overload.
	MaxFanoutPerSecond = 5000

	// MaxUnreadDisplay caps the unread badge number shown in the UI.
	MaxUnreadDisplay = 99
)

// CheckSourceRateLimit returns true if the source user has NOT exceeded the
// per-hour notification generation limit. Uses Redis INCR with a 1-hour TTL.
func CheckSourceRateLimit(ctx context.Context, rdb *redis.Client, sourceUserID string) bool {
	key := fmt.Sprintf("rl:notif:source:%s", sourceUserID)
	count, err := rdb.Incr(ctx, key).Result()
	if err != nil {
		// On Redis error, allow the notification (fail open).
		return true
	}
	if count == 1 {
		rdb.Expire(ctx, key, time.Hour)
	}
	return count <= MaxNotifPerSourcePerHour
}

// CheckPushRateLimit returns true if the recipient has NOT exceeded the
// per-hour push notification limit. Prevents notification fatigue.
func CheckPushRateLimit(ctx context.Context, rdb *redis.Client, recipientID string) bool {
	key := fmt.Sprintf("rl:push:recipient:%s", recipientID)
	count, err := rdb.Incr(ctx, key).Result()
	if err != nil {
		return true
	}
	if count == 1 {
		rdb.Expire(ctx, key, time.Hour)
	}
	return count <= MaxPushPerRecipientPerHour
}

// ClampUnreadCount caps the unread display at 99+.
func ClampUnreadCount(count int) string {
	if count > MaxUnreadDisplay {
		return "99+"
	}
	return fmt.Sprintf("%d", count)
}
