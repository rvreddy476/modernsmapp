package consumers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

/*
	Live -> video consumer (MTube, 2026-09-27).

	Listens on the social topic for live.stream.vod_ready (live-service-v2,
	internal/events/producer.go) and asks the service to create the
	streamer's long_video post (service/live_vod.go). Same shape as the
	media transcode consumer: shared Kafka consumer with dedup + retry + DLQ,
	one handler, everything the event cannot ever satisfy dropped with a log
	line rather than retried.

	The shared payload is {stream_id, creator_id, recording_url,
	duration_sec}; media_asset_id and title are read when a producer adds
	them and ignored otherwise (LiveVODEvent is the superset).
*/

// LiveVODEvent is the decoded payload: the shared LiveStreamVODReadyPayload
// plus the two optional extensions.
type LiveVODEvent struct {
	events.LiveStreamVODReadyPayload
	MediaAssetID string `json:"media_asset_id,omitempty"`
	Title        string `json:"title,omitempty"`
}

// vodCreator is the one service call the consumer needs, an interface so
// the handler is testable with a fake.
type vodCreator interface {
	CreateLiveVODPost(ctx context.Context, in service.LiveVODInput) (*service.LiveVODOutcome, error)
}

// LiveVODConsumer creates a long video from a finished live stream.
type LiveVODConsumer struct {
	svc      vodCreator
	consumer *sharedkafka.Consumer
}

// NewLiveVODConsumer builds the consumer on the given topic (the social
// topic live-service-v2 publishes to).
func NewLiveVODConsumer(svc *service.Service, brokers []string, topic string, rdb *redis.Client, m *metrics.KafkaConsumerMetrics) *LiveVODConsumer {
	c := &LiveVODConsumer{svc: svc}
	c.consumer = sharedkafka.NewConsumer(
		sharedkafka.ConsumerConfig{
			Brokers:  brokers,
			GroupID:  "post-service-live-vod",
			Topic:    topic,
			DLQTopic: topic + ".dlq",
		},
		rdb, m, c.handle,
	)
	return c
}

func (c *LiveVODConsumer) Start(ctx context.Context) { c.consumer.Start(ctx) }

func (c *LiveVODConsumer) Close() error { return c.consumer.Close() }

func (c *LiveVODConsumer) handle(ctx context.Context, env *events.EventEnvelope) error {
	if env.EventType != events.LiveStreamVODReady {
		return nil
	}
	in, ok := DecodeLiveVODEvent(env.Payload)
	if !ok {
		slog.Warn("live vod consumer: bad payload, dropped", "event_id", env.EventID)
		return nil
	}
	return handleLiveVOD(ctx, c.svc, in)
}

// DecodeLiveVODEvent turns the payload into the service input. Pure. A
// payload with no parseable stream or creator id is not ok.
func DecodeLiveVODEvent(payload []byte) (service.LiveVODInput, bool) {
	var p LiveVODEvent
	if err := json.Unmarshal(payload, &p); err != nil {
		return service.LiveVODInput{}, false
	}
	streamID, err1 := uuid.Parse(strings.TrimSpace(p.StreamID))
	creatorID, err2 := uuid.Parse(strings.TrimSpace(p.CreatorID))
	if err1 != nil || err2 != nil || streamID == uuid.Nil || creatorID == uuid.Nil {
		return service.LiveVODInput{}, false
	}
	in := service.LiveVODInput{
		StreamID:     streamID,
		CreatorID:    creatorID,
		RecordingURL: strings.TrimSpace(p.RecordingURL),
		DurationSec:  p.DurationSec,
		Title:        strings.TrimSpace(p.Title),
	}
	if id, err := uuid.Parse(strings.TrimSpace(p.MediaAssetID)); err == nil && id != uuid.Nil {
		in.MediaAssetID = &id
	}
	return in, true
}

// handleLiveVOD is the retry decision: a store / service error is returned
// (retried, then DLQ); a skip is logged and swallowed; a success is logged.
func handleLiveVOD(ctx context.Context, svc vodCreator, in service.LiveVODInput) error {
	out, err := svc.CreateLiveVODPost(ctx, in)
	if err != nil {
		return err
	}
	switch {
	case out.Skipped != "":
		slog.Info("live vod: skipped", "stream_id", in.StreamID, "reason", out.Skipped)
	case out.Created:
		slog.Info("live vod: post created", "stream_id", in.StreamID, "post_id", out.Post.ID)
	default:
		slog.Debug("live vod: post already existed", "stream_id", in.StreamID, "post_id", out.Post.ID)
	}
	return nil
}
