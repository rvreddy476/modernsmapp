package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// AggregationState tracks the current aggregation window for a recipient+event+target triple.
type AggregationState struct {
	NotificationID string   `json:"notification_id"`
	Count          int      `json:"count"`
	Actors         []string `json:"actors"`
	CreatedAt      time.Time `json:"created_at"`
}

// tryAggregateScript runs the GET → mutate → SET cycle inside Redis
// itself so two concurrent likes can't both read the same Count and
// land the same increment (audit CR4: previously the pattern was
// `raw := GET; state.Count++; SET`, with two callers racing both got
// the pre-increment count and the second SET overwrote the first).
//
// KEYS[1] = aggKey
// ARGV[1] = actorID, ARGV[2] = ttlSeconds, ARGV[3] = maxActors
// Returns: { existingNotifID (or ""), newCount } when a state already
// existed; nil when the caller should create a new notification.
var tryAggregateScript = redis.NewScript(`
local raw = redis.call("GET", KEYS[1])
if not raw then
  return nil
end
local ok, state = pcall(cjson.decode, raw)
if not ok or type(state) ~= "table" then
  return nil
end
state.count = (state.count or 1) + 1
state.actors = state.actors or {}
if #state.actors < tonumber(ARGV[3]) then
  table.insert(state.actors, ARGV[1])
end
redis.call("SET", KEYS[1], cjson.encode(state), "EX", ARGV[2])
return {state.notification_id or "", state.count}
`)

// TryAggregate checks if a notification should be aggregated with an existing one.
// Returns (shouldCreateNew bool, existingNotifID string, newCount int).
func TryAggregate(ctx context.Context, rdb *redis.Client, recipientID, eventType, targetID, actorID string) (bool, string, int) {
	template := GetTemplate(eventType)
	if !template.CanAggregate {
		return true, "", 0 // always create new
	}

	aggKey := fmt.Sprintf("agg:%s:%s:%s", recipientID, eventType, targetID)
	ttlSecs := int(template.AggregateWindow.Seconds())
	if ttlSecs <= 0 {
		ttlSecs = 60
	}

	res, err := tryAggregateScript.Run(ctx, rdb, []string{aggKey}, actorID, ttlSecs, 5).Result()
	if err == redis.Nil {
		// No existing window — caller should create a new notification
		// and then call StartAggregation to seed the state.
		return true, "", 0
	}
	if err != nil {
		slog.Warn("aggregation redis error, creating new notification", "error", err)
		return true, "", 0
	}

	// Lua returns []any{notifID string, count int64} on success.
	arr, ok := res.([]any)
	if !ok || len(arr) != 2 {
		return true, "", 0
	}
	notifID, _ := arr[0].(string)
	count64, _ := arr[1].(int64)
	return false, notifID, int(count64)
}

// AggregationState's JSON tags drive the Redis payload that the Lua
// script in tryAggregateScript decodes. Keep `notification_id`,
// `count`, `actors` keys in sync with the struct tags above — the
// Lua script depends on those exact names.

// StartAggregation creates a new aggregation window in Redis.
func StartAggregation(ctx context.Context, rdb *redis.Client, recipientID, eventType, targetID, actorID, notificationID string) {
	template := GetTemplate(eventType)
	if !template.CanAggregate {
		return
	}

	aggKey := fmt.Sprintf("agg:%s:%s:%s", recipientID, eventType, targetID)
	state := AggregationState{
		NotificationID: notificationID,
		Count:          1,
		Actors:         []string{actorID},
		CreatedAt:      time.Now(),
	}
	data, _ := json.Marshal(state)
	rdb.Set(ctx, aggKey, string(data), template.AggregateWindow)
}

// RenderTitle renders a notification title by replacing template variables.
func RenderTitle(template string, vars map[string]string) string {
	result := template
	for k, v := range vars {
		result = strings.Replace(result, "{"+k+"}", v, -1)
	}
	return result
}

// RenderAggregateTitle renders the aggregate version of the title.
func RenderAggregateTitle(aggregateTemplate string, count int, vars map[string]string) string {
	result := aggregateTemplate
	result = strings.Replace(result, "{count}", strconv.Itoa(count), -1)
	for k, v := range vars {
		result = strings.Replace(result, "{"+k+"}", v, -1)
	}
	return result
}
