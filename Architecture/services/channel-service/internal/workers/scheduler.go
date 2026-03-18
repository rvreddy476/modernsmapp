package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// scheduledUpdate represents a row from channel_updates that is ready to publish.
type scheduledUpdate struct {
	ID         uuid.UUID
	ChannelID  uuid.UUID
	AuthorID   uuid.UUID
	UpdateType string
	Title      *string
	Body       string
	Severity   string
}

// StartScheduler runs a ticker every 30 seconds that publishes scheduled
// channel updates whose scheduled_at time has passed.
func (w *FanoutWorker) StartScheduler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	w.logger.Info("scheduled update publisher started")

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("scheduled update publisher shutting down")
			return
		case <-ticker.C:
			w.publishScheduledUpdates(ctx)
		}
	}
}

// publishScheduledUpdates finds up to 50 due scheduled updates, marks them
// published, increments the channel update count, and emits an
// UpdatePublishedEvent to the fanout topic.
func (w *FanoutWorker) publishScheduledUpdates(ctx context.Context) {
	// 1. SELECT scheduled updates that are due
	query := `SELECT id, channel_id, author_id, update_type, title, body
		FROM channel_updates
		WHERE status = 'scheduled' AND scheduled_at <= NOW()
		LIMIT 50`

	rows, err := w.db.Query(ctx, query)
	if err != nil {
		w.logger.Warn("scheduler: failed to query scheduled updates", "error", err)
		return
	}
	defer rows.Close()

	var updates []scheduledUpdate
	for rows.Next() {
		var u scheduledUpdate
		if err := rows.Scan(&u.ID, &u.ChannelID, &u.AuthorID, &u.UpdateType, &u.Title, &u.Body); err != nil {
			w.logger.Warn("scheduler: failed to scan scheduled update", "error", err)
			continue
		}
		updates = append(updates, u)
	}
	if err := rows.Err(); err != nil {
		w.logger.Warn("scheduler: rows iteration error", "error", err)
		return
	}

	if len(updates) == 0 {
		return
	}

	published := 0
	for _, u := range updates {
		// 2. Mark as published
		updateQuery := `UPDATE channel_updates
			SET status = 'published', published_at = NOW(), updated_at = NOW()
			WHERE id = $1 AND status = 'scheduled'`
		tag, err := w.db.Exec(ctx, updateQuery, u.ID)
		if err != nil {
			w.logger.Warn("scheduler: failed to publish update",
				"update_id", u.ID, "error", err)
			continue
		}
		if tag.RowsAffected() == 0 {
			// Already published by another instance or the main schedule worker
			continue
		}

		// 3. Increment channel update_count
		countQuery := `UPDATE broadcast_channels SET update_count = update_count + 1, updated_at = NOW() WHERE id = $1`
		if _, err := w.db.Exec(ctx, countQuery, u.ChannelID); err != nil {
			w.logger.Warn("scheduler: failed to increment update_count",
				"channel_id", u.ChannelID, "error", err)
		}

		// 4. Emit UpdatePublishedEvent to fanout topic
		title := ""
		if u.Title != nil {
			title = *u.Title
		}
		bodyPreview := u.Body
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200]
		}

		event := UpdatePublishedEvent{
			EventType: "channel.update.published",
			EventID:   uuid.New().String(),
			Timestamp: time.Now().UTC(),
			Payload: UpdatePublishedPayload{
				UpdateID:    u.ID.String(),
				ChannelID:   u.ChannelID.String(),
				AuthorID:    u.AuthorID.String(),
				UpdateType:  u.UpdateType,
				Title:       title,
				BodyPreview: bodyPreview,
				PublishedAt: time.Now().UTC().Format(time.RFC3339),
				DeepLink:    fmt.Sprintf("/channels/%s/updates/%s", u.ChannelID, u.ID),
			},
		}

		b, err := json.Marshal(event)
		if err != nil {
			w.logger.Warn("scheduler: failed to marshal event",
				"update_id", u.ID, "error", err)
			continue
		}

		if err := w.producer.WriteMessages(ctx, kafka.Message{
			Topic: "atpost.channel.updates",
			Key:   []byte(u.ChannelID.String()),
			Value: b,
		}); err != nil {
			w.logger.Warn("scheduler: failed to emit update event",
				"update_id", u.ID, "error", err)
			continue
		}

		published++
		w.logger.Info("scheduler: published scheduled update",
			"update_id", u.ID, "channel_id", u.ChannelID)
	}

	if published > 0 {
		w.logger.Info("scheduler: batch complete",
			"total_due", len(updates), "published", published)
	}
}
