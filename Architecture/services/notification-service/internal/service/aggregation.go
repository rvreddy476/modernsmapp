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

// TryAggregate checks if a notification should be aggregated with an existing one.
// Returns (shouldCreateNew bool, existingNotifID string, newCount int).
func TryAggregate(ctx context.Context, rdb *redis.Client, recipientID, eventType, targetID, actorID string) (bool, string, int) {
	template := GetTemplate(eventType)
	if !template.CanAggregate {
		return true, "", 0 // always create new
	}

	aggKey := fmt.Sprintf("agg:%s:%s:%s", recipientID, eventType, targetID)

	raw, err := rdb.Get(ctx, aggKey).Result()
	if err == redis.Nil {
		// First event in window — create new notification, store aggregation state
		return true, "", 0
	}
	if err != nil {
		slog.Warn("aggregation redis error, creating new notification", "error", err)
		return true, "", 0
	}

	var state AggregationState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return true, "", 0
	}

	// Update existing aggregation
	state.Count++
	// Only track first 5 actor names for display
	if len(state.Actors) < 5 {
		state.Actors = append(state.Actors, actorID)
	}

	data, _ := json.Marshal(state)
	ttl := template.AggregateWindow
	rdb.Set(ctx, aggKey, string(data), ttl)

	return false, state.NotificationID, state.Count
}

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
