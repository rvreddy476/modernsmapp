package presence

// Connection-counted online state and last-active timestamps (chat parity
// §4, 18 Oct 2026).
//
// The legacy global marker `presence:{userID}` was written on connect and
// DELETED on every disconnect, so closing one browser tab reported a user
// offline while their phone was still connected. Online state is now derived
// from a per-user set of live connections:
//
//	presconns:{userID}  ZSET(connID -> expiry unix seconds)
//
// A connection's score is pushed forward on every heartbeat; a crashed
// gateway's connections simply age out. The legacy marker is still written
// (readers in message-service and user-service use it) but is deleted only
// when the LAST connection closes.
//
// Last active is a plain key so any service can MGET it:
//
//	lastactive:{userID} = unix milliseconds   EX 30 days
//
// It is written on connect, on every heartbeat and on disconnect. It is RAW
// data: every reader must gate it per viewer (graph see_last_seen; a paused
// chat_availability denies it there) before disclosing it.

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// LastActiveTTL bounds how long a last-active timestamp is kept after the
// user's final activity.
const LastActiveTTL = 30 * 24 * time.Hour

func connsKey(userID string) string { return "presconns:" + userID }

// LastActiveKey is the Redis key holding userID's last-active time (unix ms).
func LastActiveKey(userID string) string { return "lastactive:" + userID }

// UserPresence is the raw (ungated) presence of one user.
type UserPresence struct {
	Online bool
	// LastActiveAt is zero when nothing was ever recorded (or it expired).
	LastActiveAt time.Time
}

// ConnectionOpened registers connID for userID, refreshes the legacy online
// marker and last-active, and reports whether this was the user's first live
// connection (an offline -> online transition).
func (s *RedisStore) ConnectionOpened(ctx context.Context, userID, connID string, now time.Time) (becameOnline bool, err error) {
	key := connsKey(userID)
	pipe := s.rdb.TxPipeline()
	pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(now.Unix(), 10))
	before := pipe.ZCard(ctx, key)
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now.Add(s.opts.GlobalUserTTL).Unix()), Member: connID})
	pipe.Expire(ctx, key, s.opts.GlobalUserTTL)
	pipe.Set(ctx, userKey(userID), "1", s.opts.GlobalUserTTL)
	pipe.Set(ctx, LastActiveKey(userID), strconv.FormatInt(now.UnixMilli(), 10), LastActiveTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("presence connection opened: %w", err)
	}
	return before.Val() == 0, nil
}

// ConnectionHeartbeat extends connID's lifetime and refreshes the online
// marker and last-active. Called on every pong.
func (s *RedisStore) ConnectionHeartbeat(ctx context.Context, userID, connID string, now time.Time) error {
	key := connsKey(userID)
	pipe := s.rdb.TxPipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now.Add(s.opts.GlobalUserTTL).Unix()), Member: connID})
	pipe.Expire(ctx, key, s.opts.GlobalUserTTL)
	pipe.Set(ctx, userKey(userID), "1", s.opts.GlobalUserTTL)
	pipe.Set(ctx, LastActiveKey(userID), strconv.FormatInt(now.UnixMilli(), 10), LastActiveTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("presence connection heartbeat: %w", err)
	}
	return nil
}

// ConnectionClosed removes connID, stamps last-active, and reports whether
// it was the user's last live connection (an online -> offline transition).
// Only then is the legacy online marker deleted.
func (s *RedisStore) ConnectionClosed(ctx context.Context, userID, connID string, now time.Time) (becameOffline bool, err error) {
	key := connsKey(userID)
	pipe := s.rdb.TxPipeline()
	pipe.ZRem(ctx, key, connID)
	pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(now.Unix(), 10))
	remaining := pipe.ZCard(ctx, key)
	pipe.Set(ctx, LastActiveKey(userID), strconv.FormatInt(now.UnixMilli(), 10), LastActiveTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("presence connection closed: %w", err)
	}
	if remaining.Val() > 0 {
		return false, nil
	}
	if err := s.rdb.Del(ctx, userKey(userID)).Err(); err != nil {
		return false, fmt.Errorf("presence clear online: %w", err)
	}
	return true, nil
}

// LastActiveAt reads the raw last-active times of userIDs in one MGET.
// Users with nothing recorded are absent from the map. The result is NOT
// privacy-gated: callers must apply graph see_last_seen per viewer.
func LastActiveAt(ctx context.Context, rdb redis.Cmdable, userIDs []string) (map[string]time.Time, error) {
	out := make(map[string]time.Time, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	keys := make([]string, len(userIDs))
	for i, id := range userIDs {
		keys[i] = LastActiveKey(id)
	}
	values, err := rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("presence last active: %w", err)
	}
	for i, v := range values {
		if at, ok := parseMillis(v); ok {
			out[userIDs[i]] = at
		}
	}
	return out, nil
}

// Lookup reads online state and last-active for userIDs in one round trip.
// Every requested id is present in the result. NOT privacy-gated.
func Lookup(ctx context.Context, rdb redis.Cmdable, userIDs []string) (map[string]UserPresence, error) {
	out := make(map[string]UserPresence, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	keys := make([]string, 0, 2*len(userIDs))
	for _, id := range userIDs {
		keys = append(keys, userKey(id), LastActiveKey(id))
	}
	values, err := rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("presence lookup: %w", err)
	}
	for i, id := range userIDs {
		p := UserPresence{Online: values[2*i] != nil}
		if at, ok := parseMillis(values[2*i+1]); ok {
			p.LastActiveAt = at
		}
		out[id] = p
	}
	return out, nil
}

func parseMillis(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok || s == "" {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(ms).UTC(), true
}
