package ws

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// TypingTTL is how long a typing indicator persists before auto-expiring.
	TypingTTL = 5 * time.Second

	// TypingThrottle is the minimum interval between typing events from the same user in the same room.
	TypingThrottle = 3 * time.Second
)

// SetTyping marks a user as typing in a room. Returns false if throttled.
func SetTyping(ctx context.Context, rdb *redis.Client, room, userID string) bool {
	throttleKey := fmt.Sprintf("typing_throttle:%s:%s", room, userID)

	// Check throttle
	ok, _ := rdb.SetNX(ctx, throttleKey, "1", TypingThrottle).Result()
	if !ok {
		return false // throttled
	}

	// Set typing indicator
	typingKey := fmt.Sprintf("typing:%s:%s", room, userID)
	rdb.Set(ctx, typingKey, "1", TypingTTL)
	return true
}

// GetTypingUsers returns user IDs currently typing in a room.
func GetTypingUsers(ctx context.Context, rdb *redis.Client, room string) []string {
	pattern := fmt.Sprintf("typing:%s:*", room)
	keys, _ := rdb.Keys(ctx, pattern).Result()
	prefix := "typing:" + room + ":"
	var users []string
	for _, key := range keys {
		if strings.HasPrefix(key, prefix) {
			userID := key[len(prefix):]
			users = append(users, userID)
		}
	}
	return users
}

// IsTypingSupported returns true if typing indicators are allowed in the given room.
// Typing indicators are ONLY supported for:
// - chat:{conversation_id} rooms (mandatory)
// - post:{post_id} rooms (comment threads, nice-to-have)
// Never for group/channel/community post composers.
func IsTypingSupported(room string) bool {
	if strings.HasPrefix(room, "chat:") {
		return true
	}
	if strings.HasPrefix(room, "post:") {
		return true
	}
	return false
}
