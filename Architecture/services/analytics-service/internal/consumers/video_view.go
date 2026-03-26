package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/atpost/analytics-service/internal/model"
	"github.com/atpost/analytics-service/internal/scoring"
	"github.com/atpost/analytics-service/internal/store/scylla"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// VideoViewConsumer processes video analytics events from Kafka,
// updating ScyllaDB watch sessions and Redis view counters.
type VideoViewConsumer struct {
	watch *scylla.WatchStore
	rdb   *redis.Client
	base  *BaseConsumer
}

func NewVideoViewConsumer(watch *scylla.WatchStore, rdb *redis.Client) *VideoViewConsumer {
	return &VideoViewConsumer{
		watch: watch,
		rdb:   rdb,
		base:  NewBaseConsumer(rdb, "analytics-video-view"),
	}
}

// Start launches the Kafka consumer loop. Blocks until ctx is cancelled.
func (c *VideoViewConsumer) Start(ctx context.Context, brokers []string, topic string, dialer *kafka.Dialer) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  "analytics-video-view",
		Topic:    topic,
		MinBytes: 1,
		MaxBytes: 10e6, // 10 MB
		Dialer:   dialer,
	})
	defer reader.Close()

	log.Println("[VideoViewConsumer] started")

	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[VideoViewConsumer] fetch error: %v", err)
			continue
		}

		var envelope events.EventEnvelope
		if err := json.Unmarshal(msg.Value, &envelope); err != nil {
			log.Printf("[VideoViewConsumer] unmarshal error: %v", err)
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		// Only process video analytics events
		if !isVideoEvent(envelope.EventType) {
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		// Dedup
		if c.base.IsDuplicate(ctx, envelope.EventID) {
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		if err := c.processEvent(ctx, &envelope); err != nil {
			log.Printf("[VideoViewConsumer] process error for %s: %v", envelope.EventType, err)
		}

		_ = reader.CommitMessages(ctx, msg)
	}
}

func isVideoEvent(eventType string) bool {
	switch eventType {
	case events.VideoImpression,
		events.VideoPlayStart,
		events.VideoHeartbeat,
		events.VideoMilestone,
		events.VideoPlayEnd,
		events.VideoFollowFromContent,
		events.VideoNotInterested,
		events.VideoReport,
		events.VideoBlockCreator:
		return true
	}
	return false
}

func (c *VideoViewConsumer) processEvent(ctx context.Context, env *events.EventEnvelope) error {
	switch env.EventType {
	case events.VideoPlayStart:
		return c.handlePlayStart(ctx, env.Payload)
	case events.VideoHeartbeat:
		return c.handleHeartbeat(ctx, env.Payload)
	case events.VideoMilestone:
		return c.handleMilestone(ctx, env.Payload)
	case events.VideoPlayEnd:
		return c.handlePlayEnd(ctx, env.Payload)
	case events.VideoFollowFromContent,
		events.VideoNotInterested,
		events.VideoReport,
		events.VideoBlockCreator:
		return c.handleEngagement(ctx, env.EventType, env.Payload)
	}
	return nil
}

func (c *VideoViewConsumer) handlePlayStart(ctx context.Context, payload json.RawMessage) error {
	var p events.VideoPlayStartPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}

	contentID, _ := uuid.Parse(p.ContentID)
	viewerID, _ := uuid.Parse(p.ViewerID)

	now := time.Now()
	ws := &scylla.WatchSession{
		ContentID:   contentID,
		SessionID:   p.SessionID,
		ViewerID:    viewerID,
		ContentType: p.ContentType,
		DurationMS:  p.ContentDurationMS,
		WatchedMS:   0,
		Surface:     p.Surface,
		Country:     p.Country,
		DeviceHash:  p.DeviceIDHash,
		IsAutoplay:  p.IsAutoplay,
		TrustFactor: 1.0, // default, updated by TrustFactor worker
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	return c.watch.UpsertWatchSession(ctx, ws)
}

func (c *VideoViewConsumer) handleHeartbeat(ctx context.Context, payload json.RawMessage) error {
	var p events.VideoHeartbeatPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}

	contentID, _ := uuid.Parse(p.ContentID)
	viewerID, _ := uuid.Parse(p.ViewerID)

	// Update ScyllaDB session with latest watch state
	ws, err := c.watch.GetWatchSession(ctx, contentID, p.SessionID)
	if err != nil {
		// Session not found — create a partial one
		ws = &scylla.WatchSession{
			ContentID:   contentID,
			SessionID:   p.SessionID,
			ViewerID:    viewerID,
			TrustFactor: 1.0,
			CreatedAt:   time.Now(),
		}
	}

	ws.WatchedMS = p.WatchedMSTotal
	ws.LoopCount = p.LoopCount
	if ws.DurationMS > 0 {
		ws.PercentViewed = float64(p.WatchedMSTotal) / float64(ws.DurationMS) * 100
	}
	ws.UpdatedAt = time.Now()

	if err := c.watch.UpsertWatchSession(ctx, ws); err != nil {
		return err
	}

	// Track anomaly: excessive loops
	if p.LoopCount > 8 {
		_, _ = IncrementAnomaly(ctx, c.rdb, p.SessionID, "excessive_loops")
	}

	return nil
}

func (c *VideoViewConsumer) handleMilestone(ctx context.Context, payload json.RawMessage) error {
	var p events.VideoMilestonePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}

	// Increment the corresponding view bucket in Redis
	if bucket, ok := model.MilestoneToViewBucket[p.MilestoneType]; ok {
		if err := IncrementMilestoneBuckets(ctx, c.rdb, p.ContentID, []string{bucket}); err != nil {
			log.Printf("[VideoViewConsumer] milestone bucket error: %v", err)
		}
	}

	// Track anomaly: repeated milestones from same session
	anomalyCount, _ := IncrementAnomaly(ctx, c.rdb, p.SessionID, "repeated_milestones")
	if anomalyCount > 20 {
		log.Printf("[VideoViewConsumer] suspicious: session %s has %d milestone hits", p.SessionID, anomalyCount)
	}

	return nil
}

func (c *VideoViewConsumer) handlePlayEnd(ctx context.Context, payload json.RawMessage) error {
	var p events.VideoPlayEndPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}

	contentID, _ := uuid.Parse(p.ContentID)
	viewerID, _ := uuid.Parse(p.ViewerID)

	// Check display view rules
	isView := model.IsDisplayView(
		p.ContentType,
		p.ContentDurationMS,
		p.WatchedMSTotal,
		p.PercentViewed,
		p.LoopCount,
	)

	// Update ScyllaDB session
	ws, err := c.watch.GetWatchSession(ctx, contentID, p.SessionID)
	if err != nil {
		ws = &scylla.WatchSession{
			ContentID:   contentID,
			SessionID:   p.SessionID,
			ViewerID:    viewerID,
			ContentType: p.ContentType,
			DurationMS:  p.ContentDurationMS,
			TrustFactor: 1.0,
			CreatedAt:   time.Now(),
		}
	}

	ws.WatchedMS = p.WatchedMSTotal
	ws.PercentViewed = p.PercentViewed
	ws.LoopCount = p.LoopCount
	ws.EndReason = p.EndReason
	ws.IsDisplayView = isView
	ws.Surface = p.Surface
	ws.Country = p.Country
	ws.DeviceHash = p.DeviceIDHash
	ws.IsAutoplay = p.IsAutoplay
	ws.UpdatedAt = time.Now()

	// Compute VQS for this session
	trustFactor := scoring.GetTrustFactor(ctx, c.rdb, p.SessionID)
	vqs := scoring.ComputeVQS(
		p.ContentType,
		p.ContentDurationMS,
		p.WatchedMSTotal,
		p.PercentViewed,
		nil, // engagement data is tracked separately via aggregation
		trustFactor,
	)
	ws.VQS = vqs
	ws.TrustFactor = trustFactor

	if err := c.watch.UpsertWatchSession(ctx, ws); err != nil {
		log.Printf("[VideoViewConsumer] upsert session error: %v", err)
	}

	// If it's a display view, atomically increment Redis counter
	if isView {
		if newView, err := IncrementDisplayView(ctx, c.rdb, p.SessionID, p.ContentID); err != nil {
			log.Printf("[VideoViewConsumer] display view increment error: %v", err)
		} else if newView {
			// Track viewer history for unique/repeat counting
			_ = c.watch.IncrementViewerHistory(ctx, viewerID, contentID)
		}

		// Accumulate VQS into Redis for real-time view score tracking
		c.rdb.IncrByFloat(ctx, fmt.Sprintf("post:view_score:%s", p.ContentID), vqs)
		c.rdb.Expire(ctx, fmt.Sprintf("post:view_score:%s", p.ContentID), 7*24*time.Hour)
	}

	return nil
}

func (c *VideoViewConsumer) handleEngagement(ctx context.Context, eventType string, payload json.RawMessage) error {
	var p events.VideoEngagementPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}

	// Engagement events are primarily tracked via the existing engagement system.
	// Here we just log them for the video analytics pipeline to pick up during aggregation.
	log.Printf("[VideoViewConsumer] engagement: type=%s content=%s viewer=%s action=%s",
		eventType, p.ContentID, p.ViewerID, p.Action)

	return nil
}
