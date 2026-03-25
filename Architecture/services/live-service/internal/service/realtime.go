package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type liveRealtimePublisher struct {
	rdb *redis.Client
}

func newLiveRealtimePublisher(rdb *redis.Client) *liveRealtimePublisher {
	if rdb == nil {
		return nil
	}
	return &liveRealtimePublisher{rdb: rdb}
}

func (p *liveRealtimePublisher) publishStreamEvent(streamID uuid.UUID, eventType string, payload map[string]any) {
	if p == nil || p.rdb == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["stream_id"] = streamID.String()

	msg := map[string]any{
		"type":    eventType,
		"payload": payload,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		slog.Warn("failed to marshal live realtime payload", "event_type", eventType, "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	channel := fmt.Sprintf("live:stream:%s", streamID.String())
	if err := p.rdb.Publish(ctx, channel, string(data)).Err(); err != nil {
		slog.Warn("failed to publish live realtime payload", "channel", channel, "event_type", eventType, "error", err)
	}
}
