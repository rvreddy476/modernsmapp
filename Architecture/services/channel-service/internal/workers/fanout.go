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
	RecipientID string `json:"recipient_id"`
	ChannelID   string `json:"channel_id"`
	UpdateID    string `json:"update_id"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	ImageURL    string `json:"image_url"`
	DeepLink    string `json:"deep_link"`
	SentAt      string `json:"sent_at"`
}

// feedInjectMsg is produced to the channel feed-inject topic so feeds pick up the update.
type feedInjectMsg struct {
	RecipientID string `json:"recipient_id"`
	ChannelID   string `json:"channel_id"`
	UpdateID    string `json:"update_id"`
	UpdateType  string `json:"update_type"`
	AuthorID    string `json:"author_id"`
	PublishedAt string `json:"published_at"`
}

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

				// Insert in-app notification
				if err := w.insertNotification(ctx, sub.UserID, p); err != nil {
					w.logger.Warn("fanout: failed to insert notification",
						"recipient_id", sub.UserID, "update_id", p.UpdateID, "error", err)
				}

				// Produce to push/websocket topic
				notifPayload := notificationMsg{
					RecipientID: sub.UserID.String(),
					ChannelID:   p.ChannelID,
					UpdateID:    p.UpdateID,
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
				feedPayload := feedInjectMsg{
					RecipientID: sub.UserID.String(),
					ChannelID:   p.ChannelID,
					UpdateID:    p.UpdateID,
					UpdateType:  p.UpdateType,
					AuthorID:    p.AuthorID,
					PublishedAt: p.PublishedAt,
				}
				b, _ := json.Marshal(feedPayload)
				feedMessages = append(feedMessages, kafka.Message{
					Topic: "atpost.channel.feed-inject",
					Key:   []byte(sub.UserID.String()),
					Value: b,
				})
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

// fetchSubscriberBatch returns a page of non-banned subscribers for a channel.
func (w *FanoutWorker) fetchSubscriberBatch(ctx context.Context, channelID string, limit, offset int) ([]subscriberRow, error) {
	query := `SELECT user_id, notify_on
		FROM channel_members
		WHERE channel_id = $1 AND role != 'banned'
		ORDER BY subscribed_at
		LIMIT $2 OFFSET $3`

	rows, err := w.db.Query(ctx, query, channelID, limit, offset)
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

// insertNotification writes an in-app notification row for the subscriber.
func (w *FanoutWorker) insertNotification(ctx context.Context, recipientID uuid.UUID, p UpdatePublishedPayload) error {
	data, _ := json.Marshal(map[string]string{
		"channel_id":   p.ChannelID,
		"update_id":    p.UpdateID,
		"update_type":  p.UpdateType,
		"channel_name": p.ChannelName,
		"deep_link":    p.DeepLink,
		"image_url":    p.ImageURL,
	})

	query := `INSERT INTO notifications (id, recipient_id, type, title, body, data, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())`

	_, err := w.db.Exec(ctx, query,
		uuid.New(),
		recipientID,
		"channel_update",
		notifTitle(p),
		p.BodyPreview,
		data,
	)
	return err
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
