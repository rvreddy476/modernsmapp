package purge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// AckStore is what the consumer writes to. *store.Store satisfies it.
type AckStore interface {
	InsertPurgeAck(ctx context.Context, userID uuid.UUID, service string, ackedAt time.Time) error
}

// Ack is the wire message a service publishes after erasing its slice:
//
//	{"user_id":"<uuid>","service":"graph","purged_at":"2026-10-02T12:00:00Z"}
//
// It may also arrive wrapped in the platform EventEnvelope (payload field);
// both shapes are accepted.
type Ack struct {
	UserID   string `json:"user_id"`
	Service  string `json:"service"`
	PurgedAt string `json:"purged_at"`
}

// ParseAck decodes either the bare ack or an envelope carrying one.
func ParseAck(b []byte) (uuid.UUID, string, time.Time, error) {
	var a Ack
	if err := json.Unmarshal(b, &a); err != nil {
		return uuid.Nil, "", time.Time{}, fmt.Errorf("decode ack: %w", err)
	}
	if a.UserID == "" || a.Service == "" {
		var env struct {
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(b, &env); err == nil && len(env.Payload) > 0 {
			_ = json.Unmarshal(env.Payload, &a)
		}
	}
	if a.UserID == "" || a.Service == "" {
		return uuid.Nil, "", time.Time{}, errors.New("ack missing user_id or service")
	}
	uid, err := uuid.Parse(a.UserID)
	if err != nil {
		return uuid.Nil, "", time.Time{}, fmt.Errorf("ack user_id: %w", err)
	}
	svc := strings.ToLower(strings.TrimSpace(a.Service))
	at := time.Now().UTC()
	if a.PurgedAt != "" {
		if t, perr := time.Parse(time.RFC3339, a.PurgedAt); perr == nil {
			at = t
		}
	}
	return uid, svc, at, nil
}

// AcksConsumer reads the purge-acks topic and records each ack.
type AcksConsumer struct {
	reader *kafka.Reader
	store  AckStore
	log    *slog.Logger
	topic  string
}

// NewAcksConsumer builds the Kafka consumer. Dialer may be nil.
func NewAcksConsumer(brokers []string, topic, groupID string, dialer *kafka.Dialer, s AckStore, logger *slog.Logger) *AcksConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	if groupID == "" {
		groupID = "identity-auth.purge-acks"
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		Dialer:   dialer,
		MinBytes: 1,
		MaxBytes: 1 << 20,
		MaxWait:  time.Second,
	})
	return &AcksConsumer{reader: r, store: s, log: logger, topic: topic}
}

// Run consumes until ctx is cancelled. A malformed message is logged and
// committed (it will never become valid); a store failure is logged and NOT
// committed so the ack is redelivered — losing an ack would stall a purge
// forever, while a duplicate is absorbed by ON CONFLICT DO NOTHING.
func (c *AcksConsumer) Run(ctx context.Context) {
	c.log.Info("starting purge acks consumer", "topic", c.topic)
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				c.log.Info("purge acks consumer stopped")
				return
			}
			c.log.Warn("purge acks: fetch failed — retrying", "event", "purge_acks_fetch_failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		uid, svc, at, perr := ParseAck(m.Value)
		if perr != nil {
			c.log.Warn("purge acks: malformed message dropped", "event", "purge_ack_malformed",
				"err", perr, "offset", m.Offset)
			_ = c.reader.CommitMessages(ctx, m)
			continue
		}
		if err := c.store.InsertPurgeAck(ctx, uid, svc, at); err != nil {
			c.log.Error("purge acks: store failed — will redeliver", "event", "purge_ack_store_failed",
				"user_id", uid, "service", svc, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		c.log.Info("purge ack recorded", "event", "purge_ack_recorded", "user_id", uid, "service", svc)
		if err := c.reader.CommitMessages(ctx, m); err != nil && ctx.Err() == nil {
			c.log.Warn("purge acks: commit failed", "err", err)
		}
	}
}

// Close releases the Kafka reader.
func (c *AcksConsumer) Close() error { return c.reader.Close() }
