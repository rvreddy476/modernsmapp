package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/user-service/internal/service"
	"github.com/atpost/user-service/internal/store"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// maxProcessAttempts is how many times the consumer retries a failing event
// in place (for transient errors) before parking it in the DLQ.
const maxProcessAttempts = 3

type Consumer struct {
	reader *kafka.Reader
	svc    *service.Service
	store  *store.Store
	topic  string
}

func NewConsumer(brokers []string, topic string, svc *service.Service, st *store.Store) *Consumer {
	return NewConsumerWithDialer(brokers, topic, svc, st, nil)
}

func NewConsumerWithDialer(brokers []string, topic string, svc *service.Service, st *store.Store, dialer *kafka.Dialer) *Consumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: "user-service-group",
		Dialer:  dialer,
	})
	return &Consumer{reader: r, svc: svc, store: st, topic: topic}
}

func (c *Consumer) Start(ctx context.Context) {
	log.Println("Starting Kafka consumer...")
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Println("Kafka consumer shutting down")
				return
			}
			log.Printf("Error reading message: %v\n", err)
			time.Sleep(2 * time.Second)
			continue
		}

		var envelope events.EventEnvelope
		if err := json.Unmarshal(m.Value, &envelope); err != nil {
			// Unparseable event — cannot even read its type. Park the raw
			// bytes in the DLQ rather than dropping them silently.
			c.toDLQ(ctx, "", string(m.Value), fmt.Errorf("envelope unmarshal: %w", err))
			continue
		}

		if err := c.processWithRetry(ctx, envelope); err != nil {
			c.toDLQ(ctx, envelope.EventType, string(envelope.Payload), err)
		}
	}
}

// processWithRetry dispatches an event, retrying a few times so a transient
// failure (e.g. a brief DB blip) does not park an otherwise-good event.
func (c *Consumer) processWithRetry(ctx context.Context, envelope events.EventEnvelope) error {
	var err error
	for attempt := 1; attempt <= maxProcessAttempts; attempt++ {
		if err = c.Dispatch(ctx, envelope); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if attempt < maxProcessAttempts {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}
	return err
}

// Dispatch routes one event to its handler. Unknown event types are a no-op
// (not an error). Exported so the DLQ replay path can reuse it.
func (c *Consumer) Dispatch(ctx context.Context, envelope events.EventEnvelope) error {
	switch envelope.EventType {
	case events.UserRegistered:
		return c.handleUserRegistered(ctx, envelope.Payload)
	case events.UserEndorsed:
		return c.handleUserEndorsed(ctx, envelope.Payload)
	case events.EventUserDeletionRequested:
		return c.handleUserDeletionRequested(ctx, envelope.Payload)
	case events.EventSellerApproved:
		return c.handleSellerApproved(ctx, envelope.Payload)
	default:
		return nil // not for us
	}
}

// toDLQ parks a failed event for inspection/replay. A DLQ-write failure is
// itself only logged — there is nowhere safer left to put it.
func (c *Consumer) toDLQ(ctx context.Context, eventType, payload string, cause error) {
	slog.Error("event processing failed — parking in DLQ",
		"event_type", eventType, "error", cause)
	if c.store == nil {
		return
	}
	if err := c.store.InsertDLQ(ctx, c.topic, eventType, payload, cause.Error()); err != nil {
		slog.Error("failed to write DLQ entry", "error", err, "original_error", cause)
	}
}

// ReplayOne re-dispatches a parked DLQ entry. Handlers are idempotent, so a
// replay of an already-applied event is harmless. On success the entry is
// stamped replayed.
func (c *Consumer) ReplayOne(ctx context.Context, id int64) error {
	entry, err := c.store.GetDLQ(ctx, id)
	if err != nil {
		return err
	}
	if entry == nil {
		return fmt.Errorf("dlq entry %d not found", id)
	}
	envelope := events.EventEnvelope{
		EventType: entry.EventType,
		Payload:   json.RawMessage(entry.Payload),
	}
	if err := c.Dispatch(ctx, envelope); err != nil {
		return err
	}
	return c.store.MarkDLQReplayed(ctx, id)
}

func (c *Consumer) handleUserRegistered(ctx context.Context, payload json.RawMessage) error {
	var p events.UserRegisteredPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("unmarshal UserRegistered: %w", err)
	}
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return fmt.Errorf("invalid user ID %q: %w", p.UserID, err)
	}
	emailStr := ""
	if p.Email != nil {
		emailStr = *p.Email
	}
	if err := c.svc.CreateUser(ctx, userID, p.Phone, emailStr, p.FirstName, p.LastName, p.DOB, p.Gender); err != nil {
		return fmt.Errorf("create user profile for %s: %w", userID, err)
	}
	log.Printf("Created user profile for %s\n", userID)
	return nil
}

func (c *Consumer) handleUserDeletionRequested(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("unmarshal user.deletion_requested: %w", err)
	}
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return fmt.Errorf("invalid user ID %q: %w", p.UserID, err)
	}
	if err := c.svc.SoftDeleteUser(ctx, userID); err != nil {
		return fmt.Errorf("soft-delete user %s: %w", userID, err)
	}
	log.Printf("Soft-deleted user record for %s\n", userID)
	return nil
}

func (c *Consumer) handleUserEndorsed(ctx context.Context, payload json.RawMessage) error {
	var p events.UserEndorsedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("unmarshal UserEndorsed: %w", err)
	}
	toUserID, err := uuid.Parse(p.ToUserID)
	if err != nil {
		return fmt.Errorf("invalid to_user_id %q: %w", p.ToUserID, err)
	}
	rep, err := c.svc.GetReputation(ctx, toUserID)
	if err != nil {
		return fmt.Errorf("get reputation for %s: %w", toUserID, err)
	}
	// Trust score: base 0.50 + 0.05 per endorsement, capped at 1.00.
	newScore := 0.50 + float64(rep.EndorsementCount)*0.05
	if newScore > 1.00 {
		newScore = 1.00
	}
	if newScore != rep.TrustScore {
		log.Printf("Reputation recalc for %s: endorsements=%d, trust_score=%.2f\n", toUserID, rep.EndorsementCount, newScore)
	}
	return nil
}

// handleSellerApproved activates the linked business page + sets seller_id on it.
func (c *Consumer) handleSellerApproved(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		SellerID       string `json:"seller_id"`
		BusinessPageID string `json:"business_page_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("unmarshal seller.approved: %w", err)
	}
	if p.BusinessPageID == "" {
		return nil // seller has no linked page — nothing to activate
	}
	pageID, err := uuid.Parse(p.BusinessPageID)
	if err != nil {
		return fmt.Errorf("invalid business_page_id %q: %w", p.BusinessPageID, err)
	}
	sellerID, err := uuid.Parse(p.SellerID)
	if err != nil {
		return fmt.Errorf("invalid seller_id %q: %w", p.SellerID, err)
	}
	if err := c.svc.SetBusinessPageSellerID(ctx, pageID, sellerID); err != nil {
		return fmt.Errorf("set seller_id on page %s: %w", pageID, err)
	}
	if err := c.svc.ActivateBusinessPage(ctx, pageID); err != nil {
		return fmt.Errorf("activate business page %s: %w", pageID, err)
	}
	slog.Info("activated business page after seller approval", "page_id", pageID, "seller_id", sellerID)
	return nil
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
