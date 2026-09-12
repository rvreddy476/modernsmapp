package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

// UpdatePublishedPayload is the rich payload consumed from the "atpost.channel.updates" topic.
type UpdatePublishedPayload struct {
	UpdateID    string `json:"update_id"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	AuthorID    string `json:"author_id"`
	AuthorName  string `json:"author_name"`
	UpdateType  string `json:"update_type"`
	Title       string `json:"title"`
	BodyPreview string `json:"body_preview"`
	Visibility  string `json:"visibility"`
	Severity    string `json:"severity"`
	ImageURL    string `json:"image_url"`
	DeepLink    string `json:"deep_link"`
	PublishedAt string `json:"published_at"`
}

// UpdatePublishedEvent is the envelope consumed from the fanout topic.
type UpdatePublishedEvent struct {
	EventType string                 `json:"event_type"`
	EventID   string                 `json:"event_id"`
	Timestamp time.Time              `json:"timestamp"`
	Payload   UpdatePublishedPayload `json:"payload"`
}

// subscriberRow represents a single channel subscriber fetched for fanout.
type subscriberRow struct {
	UserID   uuid.UUID
	NotifyOn string
}

// notificationMsg is produced to the channel notifications topic for push/websocket delivery.
type notificationMsg struct {
	EventType   string `json:"event_type"`
	RecipientID string `json:"recipient_id"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	UpdateID    string `json:"update_id"`
	UpdateType  string `json:"update_type"`
	AuthorID    string `json:"author_id"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	ImageURL    string `json:"image_url"`
	DeepLink    string `json:"deep_link"`
	SentAt      string `json:"sent_at"`
}

// feedInjectPayload mirrors feed-service's FeedInjectPayload
// (feed-service/internal/consumers/channel_updates.go) field for field. It is
// redeclared here rather than imported because the two services are separate
// modules and the wire bytes, not a shared Go type, are the contract.
//
// feed-service gates on target_user_id and item_id, uuid.Parses source_id as
// the timeline author, and stores item_type as the content type. It has no
// slot for author_id or update_type, so those ride in preview_json.
type feedInjectPayload struct {
	TargetUserID string          `json:"target_user_id"`
	ItemType     string          `json:"item_type"`
	ItemID       string          `json:"item_id"`
	SourceType   string          `json:"source_type"`
	SourceID     string          `json:"source_id"`
	Score        int64           `json:"score"`
	PublishedAt  string          `json:"published_at"`
	PreviewJSON  json.RawMessage `json:"preview_json,omitempty"`
}

// feedInjectPreview is the extra context feed-service does not model but a
// timeline renderer may want; it is opaque to the consumer.
type feedInjectPreview struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	AuthorID    string `json:"author_id"`
	UpdateType  string `json:"update_type"`
	Title       string `json:"title"`
	ImageURL    string `json:"image_url"`
	DeepLink    string `json:"deep_link"`
}

// feedInjectEvent is the envelope feed-service decodes from the
// atpost.channel.feed-inject topic. A flat object here used to unmarshal
// cleanly into a zero payload on the consumer side, which then logged
// "missing target_user_id or item_id" and committed the offset, so no channel
// update ever reached a home timeline.
type feedInjectEvent struct {
	EventType string            `json:"event_type"`
	Payload   feedInjectPayload `json:"payload"`
}

// The item and source types are the literal values the consumer documents
// next to its struct tags. The consumer does not switch on event_type, so the
// name follows the notification topic's convention for the same event.
const (
	feedInjectEventType  = "channel.update.published"
	feedInjectItemType   = "channel_update"
	feedInjectSourceType = "channel"
)

// FanoutWorker consumes channel update events and fans them out to subscribers
// via notifications, push, and feed injection.
type FanoutWorker struct {
	db       *pgxpool.Pool
	brokers  []string
	producer *kafka.Writer
	dialer   *kafka.Dialer
	logger   *slog.Logger
}

// NewFanoutWorker creates a FanoutWorker with a Kafka producer for writing
// fanout messages to notification and feed topics.
func NewFanoutWorker(db *pgxpool.Pool, brokers []string, logger *slog.Logger) *FanoutWorker {
	return NewFanoutWorkerWithDialer(db, brokers, logger, nil)
}

// NewFanoutWorkerWithDialer creates a FanoutWorker with an explicit Kafka dialer.
func NewFanoutWorkerWithDialer(db *pgxpool.Pool, brokers []string, logger *slog.Logger, dialer *kafka.Dialer) *FanoutWorker {
	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	return &FanoutWorker{
		db:       db,
		brokers:  brokers,
		producer: writer,
		dialer:   dialer,
		logger:   logger,
	}
}

// Start begins consuming from "atpost.channel.updates" and processing each
// update-published event through the fanout pipeline.
func (w *FanoutWorker) Start(ctx context.Context) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  w.brokers,
		GroupID:  "channel-fanout-group",
		Topic:    "atpost.channel.updates",
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   w.dialer,
	})

	w.logger.Info("fanout worker listening on atpost.channel.updates")

	for {
		m, err := reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				w.logger.Info("fanout worker shutting down")
				_ = reader.Close()
				return
			}
			w.logger.Error("fanout worker read error", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}

		var event UpdatePublishedEvent
		if err := json.Unmarshal(m.Value, &event); err != nil {
			w.logger.Warn("fanout worker: failed to unmarshal event", "error", err)
			continue
		}

		w.handleUpdatePublished(ctx, event)
	}
}

// handleUpdatePublished fans a single update out to all non-banned subscribers.
func (w *FanoutWorker) handleUpdatePublished(ctx context.Context, event UpdatePublishedEvent) {
	p := event.Payload
	channelID := p.ChannelID

	const batchSize = 1000
	offset := 0
	totalSubscribers := 0
	totalNotifications := 0

	for {
		subscribers, err := w.fetchSubscriberBatch(ctx, channelID, batchSize, offset)
		if err != nil {
			w.logger.Error("fanout: failed to fetch subscribers",
				"channel_id", channelID, "offset", offset, "error", err)
			return
		}
		if len(subscribers) == 0 {
			break
		}

		var notifMessages []kafka.Message
		var feedMessages []kafka.Message

		for _, sub := range subscribers {
			// The author never needs telling about their own update.
			if sub.UserID.String() == p.AuthorID {
				continue
			}
			totalSubscribers++

			sendNotification := false
			sendFeed := true // always inject into feed

			switch sub.NotifyOn {
			case "all":
				sendNotification = true
			case "highlights":
				// Only push for critical alerts under highlights
				if p.UpdateType == "alert" && p.Severity == "critical" {
					sendNotification = true
				}
			case "none":
				// No notifications unless critical alert override
			}

			// Critical alert override: always notify regardless of preference
			if p.UpdateType == "alert" && p.Severity == "critical" {
				sendNotification = true
			}

			if sendNotification {
				totalNotifications++

				// The inbox row is written by notification-service from the
				// message below (it owns the inbox store; the old direct
				// INSERT targeted a table that does not exist in this DB).
				notifPayload := notificationMsg{
					EventType:   "channel.update.published",
					RecipientID: sub.UserID.String(),
					ChannelID:   p.ChannelID,
					ChannelName: p.ChannelName,
					UpdateID:    p.UpdateID,
					UpdateType:  p.UpdateType,
					AuthorID:    p.AuthorID,
					Title:       notifTitle(p),
					Body:        p.BodyPreview,
					ImageURL:    p.ImageURL,
					DeepLink:    p.DeepLink,
					SentAt:      time.Now().UTC().Format(time.RFC3339),
				}
				b, _ := json.Marshal(notifPayload)
				notifMessages = append(notifMessages, kafka.Message{
					Topic: "atpost.channel.notifications",
					Key:   []byte(sub.UserID.String()),
					Value: b,
				})
			}

			if sendFeed {
				feedMessages = append(feedMessages, newFeedInjectMessage(p, sub.UserID))
			}
		}

		// Batch produce notification messages
		if len(notifMessages) > 0 {
			if err := w.producer.WriteMessages(ctx, notifMessages...); err != nil {
				w.logger.Warn("fanout: failed to produce notification messages",
					"count", len(notifMessages), "error", err)
			}
		}

		// Batch produce feed messages
		if len(feedMessages) > 0 {
			if err := w.producer.WriteMessages(ctx, feedMessages...); err != nil {
				w.logger.Warn("fanout: failed to produce feed-inject messages",
					"count", len(feedMessages), "error", err)
			}
		}

		if len(subscribers) < batchSize {
			break
		}
		offset += batchSize
	}

	w.logger.Info("fanout complete",
		"update_id", p.UpdateID,
		"subscribers", totalSubscribers,
		"notifications", totalNotifications,
	)
}

// newFeedInjectMessage builds the Kafka message that asks feed-service to put
// one channel update on one subscriber's home timeline.
func newFeedInjectMessage(p UpdatePublishedPayload, recipient uuid.UUID) kafka.Message {
	// A marshal failure here is impossible for a struct of strings, and the
	// preview is optional to the consumer anyway, so a nil result is fine.
	preview, _ := json.Marshal(feedInjectPreview{
		ChannelID:   p.ChannelID,
		ChannelName: p.ChannelName,
		AuthorID:    p.AuthorID,
		UpdateType:  p.UpdateType,
		Title:       p.Title,
		ImageURL:    p.ImageURL,
		DeepLink:    p.DeepLink,
	})
	event := feedInjectEvent{
		EventType: feedInjectEventType,
		Payload: feedInjectPayload{
			TargetUserID: recipient.String(),
			ItemType:     feedInjectItemType,
			ItemID:       p.UpdateID,
			// The consumer stores source_id as the timeline author, so the
			// channel, not the human author, is what the feed attributes to.
			SourceType:  feedInjectSourceType,
			SourceID:    p.ChannelID,
			PublishedAt: p.PublishedAt,
			PreviewJSON: preview,
		},
	}
	b, _ := json.Marshal(event)
	return kafka.Message{
		Topic: "atpost.channel.feed-inject",
		Key:   []byte(recipient.String()),
		Value: b,
	}
}

// subscriberBatchQuery is the fan-out roster read. Two gates, both
// load-bearing:
//
//	cm.role != 'banned'   a removed/banned member stops being notified
//	bc.status = 'active'  a SUSPENDED channel fans nothing out
//
// The status join was added with the invite-only pilot (2026-09-12): the
// per-channel emergency disable writes broadcast_channels.status =
// 'suspended', and without this join a suspended community would keep
// pushing notifications and injecting home-timeline rows.
const subscriberBatchQuery = `SELECT cm.user_id, cm.notify_on
		FROM channel_members cm
		JOIN broadcast_channels bc ON bc.id = cm.channel_id
		WHERE cm.channel_id = $1 AND cm.role != 'banned' AND bc.status = 'active'
		ORDER BY cm.subscribed_at
		LIMIT $2 OFFSET $3`

// fetchSubscriberBatch returns a page of non-banned subscribers for a
// channel that is still active.
func (w *FanoutWorker) fetchSubscriberBatch(ctx context.Context, channelID string, limit, offset int) ([]subscriberRow, error) {
	rows, err := w.db.Query(ctx, subscriberBatchQuery, channelID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query subscribers: %w", err)
	}
	defer rows.Close()

	var subs []subscriberRow
	for rows.Next() {
		var s subscriberRow
		if err := rows.Scan(&s.UserID, &s.NotifyOn); err != nil {
			return nil, fmt.Errorf("scan subscriber: %w", err)
		}
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

// notifTitle returns a human-readable notification title.
func notifTitle(p UpdatePublishedPayload) string {
	if p.Title != "" {
		return fmt.Sprintf("%s: %s", p.ChannelName, p.Title)
	}
	return fmt.Sprintf("New update from %s", p.ChannelName)
}

// Close shuts down the Kafka producer.
func (w *FanoutWorker) Close() error {
	return w.producer.Close()
}
