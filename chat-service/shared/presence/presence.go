// Package presence implements per-conversation presence + typing
// indicators on Redis ZSETs.
//
// The chat platform tracks two presence axes:
//
//  1. Global online — "is this user connected to any ws-gateway?"
//     Stored as a single key per user with TTL refreshed by pong:
//     presence:user:{userID} = "1" EX 90
//
//  2. Per-conversation presence — "is this user currently *viewing*
//     this conversation?" Stored as a ZSET per conversation, score
//     is the last-seen unix timestamp:
//     presence:conv:{convID} = ZSET(userID -> last_seen_sec)
//
// The ZSET shape gives per-user logical expiry: even when the client
// crashes without sending a `conversation.leave`, the next read calls
// ZREMRANGEBYSCORE to evict anything older than the active window
// (default 45s). The whole key also carries a 120s EXPIRE so empty
// conversations get garbage-collected.
//
// Typing indicators follow the same pattern with a shorter window
// (8s active, 15s key TTL).
//
// The Store interface intentionally hides Redis so the chat-service
// can swap in a dedicated presence broker (Centrifugo, NATS, custom)
// later without rewiring callers — Phase 3 of the production
// realtime-architecture decision doc.
package presence

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store abstracts the presence backend so callers don't import Redis.
type Store interface {
	// SetUserOnline refreshes the global "user is connected somewhere"
	// marker. Called on connect + on every pong so a missed disconnect
	// is bounded by the TTL.
	SetUserOnline(ctx context.Context, userID string) error

	// ClearUserOnline drops the global marker. Called on a clean
	// disconnect; the TTL on SetUserOnline is the safety net for
	// crashes.
	ClearUserOnline(ctx context.Context, userID string) error

	// EnterConversation marks the user active in convID.
	EnterConversation(ctx context.Context, convID, userID string) error

	// HeartbeatConversation refreshes the user's last-seen score and
	// evicts any other members that have aged out. Cheap enough to call
	// on every 15s client heartbeat.
	HeartbeatConversation(ctx context.Context, convID, userID string) error

	// LeaveConversation removes the user immediately. Heartbeat expiry
	// is the safety net if a Leave event is missed.
	LeaveConversation(ctx context.Context, convID, userID string) error

	// ActiveUsers returns up to `limit` userIDs currently active. Use
	// for 1:1 + small-group UX; large communities should call
	// ActiveCount instead to avoid leaking a full member list.
	ActiveUsers(ctx context.Context, convID string, limit int) ([]string, error)

	// ActiveCount returns just the cardinality — safe to expose in
	// every UX tier.
	ActiveCount(ctx context.Context, convID string) (int64, error)

	// SetTyping marks the user as currently typing in convID.
	SetTyping(ctx context.Context, convID, userID string) error

	// TypingUsers returns the currently-typing userIDs, limited to
	// `limit`. Older entries beyond the typing window are evicted on
	// read.
	TypingUsers(ctx context.Context, convID string, limit int) ([]string, error)
}

// Options tunes the eviction windows + key TTLs. Defaults match the
// production realtime architecture doc.
type Options struct {
	// GlobalUserTTL is how long the presence:user:{userID} marker
	// stays alive after the last refresh. Default 90s — the pong
	// handler in ws-gateway refreshes it.
	GlobalUserTTL time.Duration

	// ConversationActiveWindow is the cutoff for "still in this chat".
	// On every heartbeat we ZREMRANGEBYSCORE everything older than
	// now - ConversationActiveWindow. Default 45s — 3× the 15s
	// heartbeat cadence so a single packet loss doesn't kick a user.
	ConversationActiveWindow time.Duration

	// ConversationKeyTTL is the safety-net expiry on the conv ZSET
	// itself so empty conversations get garbage-collected. Default
	// 120s — long enough that an idle conv survives a slow heartbeat
	// but short enough that a closed chat doesn't linger.
	ConversationKeyTTL time.Duration

	// TypingActiveWindow is the typing-indicator cutoff. Default 8s —
	// matches the "user paused" UX.
	TypingActiveWindow time.Duration

	// TypingKeyTTL is the typing-ZSET safety expiry. Default 15s.
	TypingKeyTTL time.Duration
}

func (o Options) withDefaults() Options {
	if o.GlobalUserTTL <= 0 {
		o.GlobalUserTTL = 90 * time.Second
	}
	if o.ConversationActiveWindow <= 0 {
		o.ConversationActiveWindow = 45 * time.Second
	}
	if o.ConversationKeyTTL <= 0 {
		o.ConversationKeyTTL = 120 * time.Second
	}
	if o.TypingActiveWindow <= 0 {
		o.TypingActiveWindow = 8 * time.Second
	}
	if o.TypingKeyTTL <= 0 {
		o.TypingKeyTTL = 15 * time.Second
	}
	return o
}

// RedisStore is the Phase 1 implementation. Single Redis instance,
// ZSET per conversation. Memory cost is bounded by
// (active conversations) × (avg viewers per conv) × ~32 bytes.
type RedisStore struct {
	rdb  *redis.Client
	opts Options
}

func NewRedisStore(rdb *redis.Client, opts Options) *RedisStore {
	return &RedisStore{rdb: rdb, opts: opts.withDefaults()}
}

func convKey(convID string) string { return "presence:conv:" + convID }

// userKey keeps the legacy `presence:{userID}` naming because
// user-service (internal/presence) and message-service both read it
// directly. The per-conversation keys are net-new and use the new
// presence:conv: prefix without collision risk.
func userKey(userID string) string   { return "presence:" + userID }
func typingKey(convID string) string { return "typing:conv:" + convID }

func (s *RedisStore) SetUserOnline(ctx context.Context, userID string) error {
	return s.rdb.Set(ctx, userKey(userID), "1", s.opts.GlobalUserTTL).Err()
}

func (s *RedisStore) ClearUserOnline(ctx context.Context, userID string) error {
	return s.rdb.Del(ctx, userKey(userID)).Err()
}

func (s *RedisStore) EnterConversation(ctx context.Context, convID, userID string) error {
	now := float64(time.Now().Unix())
	pipe := s.rdb.Pipeline()
	pipe.ZAdd(ctx, convKey(convID), redis.Z{Score: now, Member: userID})
	pipe.Expire(ctx, convKey(convID), s.opts.ConversationKeyTTL)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("presence enter: %w", err)
	}
	return nil
}

func (s *RedisStore) HeartbeatConversation(ctx context.Context, convID, userID string) error {
	now := time.Now().Unix()
	cutoff := now - int64(s.opts.ConversationActiveWindow/time.Second)
	pipe := s.rdb.Pipeline()
	pipe.ZAdd(ctx, convKey(convID), redis.Z{Score: float64(now), Member: userID})
	// Evict members that have aged out. Range is (-inf, cutoff].
	pipe.ZRemRangeByScore(ctx, convKey(convID), "0", fmt.Sprintf("%d", cutoff))
	pipe.Expire(ctx, convKey(convID), s.opts.ConversationKeyTTL)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("presence heartbeat: %w", err)
	}
	return nil
}

func (s *RedisStore) LeaveConversation(ctx context.Context, convID, userID string) error {
	if err := s.rdb.ZRem(ctx, convKey(convID), userID).Err(); err != nil {
		return fmt.Errorf("presence leave: %w", err)
	}
	return nil
}

func (s *RedisStore) ActiveUsers(ctx context.Context, convID string, limit int) ([]string, error) {
	cutoff := time.Now().Unix() - int64(s.opts.ConversationActiveWindow/time.Second)
	// Lazy cleanup on read so stale members don't bleed into the
	// returned list when nobody's heartbeating.
	if err := s.rdb.ZRemRangeByScore(ctx, convKey(convID), "0", fmt.Sprintf("%d", cutoff)).Err(); err != nil {
		return nil, fmt.Errorf("presence cleanup: %w", err)
	}
	if limit <= 0 {
		limit = 100
	}
	users, err := s.rdb.ZRange(ctx, convKey(convID), 0, int64(limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("presence list: %w", err)
	}
	return users, nil
}

func (s *RedisStore) ActiveCount(ctx context.Context, convID string) (int64, error) {
	cutoff := time.Now().Unix() - int64(s.opts.ConversationActiveWindow/time.Second)
	if err := s.rdb.ZRemRangeByScore(ctx, convKey(convID), "0", fmt.Sprintf("%d", cutoff)).Err(); err != nil {
		return 0, fmt.Errorf("presence cleanup: %w", err)
	}
	n, err := s.rdb.ZCard(ctx, convKey(convID)).Result()
	if err != nil {
		return 0, fmt.Errorf("presence count: %w", err)
	}
	return n, nil
}

func (s *RedisStore) SetTyping(ctx context.Context, convID, userID string) error {
	now := time.Now().Unix()
	cutoff := now - int64(s.opts.TypingActiveWindow/time.Second)
	pipe := s.rdb.Pipeline()
	pipe.ZAdd(ctx, typingKey(convID), redis.Z{Score: float64(now), Member: userID})
	pipe.ZRemRangeByScore(ctx, typingKey(convID), "0", fmt.Sprintf("%d", cutoff))
	pipe.Expire(ctx, typingKey(convID), s.opts.TypingKeyTTL)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("typing set: %w", err)
	}
	return nil
}

func (s *RedisStore) TypingUsers(ctx context.Context, convID string, limit int) ([]string, error) {
	cutoff := time.Now().Unix() - int64(s.opts.TypingActiveWindow/time.Second)
	if err := s.rdb.ZRemRangeByScore(ctx, typingKey(convID), "0", fmt.Sprintf("%d", cutoff)).Err(); err != nil {
		return nil, fmt.Errorf("typing cleanup: %w", err)
	}
	if limit <= 0 {
		limit = 20
	}
	users, err := s.rdb.ZRange(ctx, typingKey(convID), 0, int64(limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("typing list: %w", err)
	}
	return users, nil
}
