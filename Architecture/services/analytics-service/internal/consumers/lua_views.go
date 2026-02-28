package consumers

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// viewCounterScript atomically:
// 1. Checks if this session already counted a display view (SETNX dedup)
// 2. If new display view, increments HINCRBY on the views hash
// 3. Adds content to hot:videos set for reconciliation
//
// KEYS[1] = viewed:{sessionID}:{contentID}  (dedup key)
// KEYS[2] = post:views:{contentID}          (view counter hash)
// KEYS[3] = hot:videos                      (hot set for reconciliation)
// ARGV[1] = contentID                       (for hot set)
// ARGV[2] = TTL seconds for dedup key       (86400 = 24h)
// ARGV[3] = TTL seconds for views hash      (604800 = 7d)
//
// Returns 1 if new display view was counted, 0 if duplicate.
var viewCounterScript = redis.NewScript(`
local dedup = redis.call('SETNX', KEYS[1], '1')
if dedup == 0 then
    return 0
end
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[2]))
redis.call('HINCRBY', KEYS[2], 'display', 1)
if redis.call('TTL', KEYS[2]) == -1 then
    redis.call('EXPIRE', KEYS[2], tonumber(ARGV[3]))
end
redis.call('SADD', KEYS[3], ARGV[1])
return 1
`)

// IncrementDisplayView atomically counts a new display view for a content item.
// Returns true if the view was new (not a duplicate session).
func IncrementDisplayView(ctx context.Context, rdb *redis.Client, sessionID, contentID string) (bool, error) {
	result, err := viewCounterScript.Run(ctx, rdb,
		[]string{
			"viewed:" + sessionID + ":" + contentID,
			"post:views:" + contentID,
			"hot:videos",
		},
		contentID,
		86400,  // 24h dedup TTL
		604800, // 7d views hash TTL
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// milestoneCounterScript increments specific view bucket counters.
// KEYS[1] = post:views:{contentID}
// ARGV = list of bucket names to increment (e.g. "views_1s", "views_3s")
var milestoneCounterScript = redis.NewScript(`
for i = 1, #ARGV do
    redis.call('HINCRBY', KEYS[1], ARGV[i], 1)
end
if redis.call('TTL', KEYS[1]) == -1 then
    redis.call('EXPIRE', KEYS[1], 604800)
end
return 1
`)

// IncrementMilestoneBuckets increments the view bucket counters for reached milestones.
func IncrementMilestoneBuckets(ctx context.Context, rdb *redis.Client, contentID string, buckets []string) error {
	if len(buckets) == 0 {
		return nil
	}
	args := make([]interface{}, len(buckets))
	for i, b := range buckets {
		args[i] = b
	}
	return milestoneCounterScript.Run(ctx, rdb,
		[]string{"post:views:" + contentID},
		args...,
	).Err()
}

// anomalyIncrScript increments anomaly counters for suspicious sessions.
// KEYS[1] = sess:anomaly:{sessionID}
// ARGV[1] = field name (e.g. "repeated_milestones", "excessive_loops")
var anomalyIncrScript = redis.NewScript(`
redis.call('HINCRBY', KEYS[1], ARGV[1], 1)
if redis.call('TTL', KEYS[1]) == -1 then
    redis.call('EXPIRE', KEYS[1], 3600)
end
return redis.call('HGET', KEYS[1], ARGV[1])
`)

// IncrementAnomaly increments a specific anomaly counter for a session.
// Returns the new counter value.
func IncrementAnomaly(ctx context.Context, rdb *redis.Client, sessionID, anomalyType string) (int64, error) {
	return anomalyIncrScript.Run(ctx, rdb,
		[]string{"sess:anomaly:" + sessionID},
		anomalyType,
	).Int64()
}

// GetViewCounts reads all view counters for a content item from Redis.
func GetViewCounts(ctx context.Context, rdb *redis.Client, contentID string) (map[string]int64, error) {
	result, err := rdb.HGetAll(ctx, "post:views:"+contentID).Result()
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(result))
	for k, v := range result {
		var n int64
		fmt.Sscanf(v, "%d", &n)
		counts[k] = n
	}
	return counts, nil
}

// Helper to split a comma-separated bucket list.
func parseBuckets(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
