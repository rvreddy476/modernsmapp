package events

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/user-service/internal/service"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Consumer struct {
	reader *kafka.Reader
	svc    *service.Service
}

func NewConsumer(brokers []string, topic string, svc *service.Service) *Consumer {
	return NewConsumerWithDialer(brokers, topic, svc, nil)
}

func NewConsumerWithDialer(brokers []string, topic string, svc *service.Service, dialer *kafka.Dialer) *Consumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: "user-service-group",
		Dialer:  dialer,
	})
	return &Consumer{reader: r, svc: svc}
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
			log.Printf("Error unmarshalling event: %v\n", err)
			continue
		}

		switch envelope.EventType {
		case events.UserRegistered:
			c.handleUserRegistered(ctx, envelope.Payload)
		case events.UserEndorsed:
			c.handleUserEndorsed(ctx, envelope.Payload)
		case events.EventUserDeletionRequested:
			c.handleUserDeletionRequested(ctx, envelope.Payload)
		case events.EventSellerApproved:
			c.handleSellerApproved(ctx, envelope.Payload)
		default:
			// Ignore other events
		}
	}
}

func (c *Consumer) handleUserRegistered(ctx context.Context, payload json.RawMessage) {
	var p events.UserRegisteredPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		log.Printf("Error unmarshalling UserRegistered payload: %v\n", err)
		return
	}

	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		log.Printf("Invalid user ID in event: %s\n", p.UserID)
		return
	}

	// Create default profile
	emailStr := ""
	if p.Email != nil {
		emailStr = *p.Email
	}
	if err := c.svc.CreateUser(ctx, userID, p.Phone, emailStr, p.FirstName, p.LastName, p.DOB, p.Gender); err != nil {
		log.Printf("Failed to create user profile for %s: %v\n", userID, err)
	} else {
		log.Printf("Created user profile for %s\n", userID)
	}
}

func (c *Consumer) handleUserDeletionRequested(ctx context.Context, payload json.RawMessage) {
	var p struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		log.Printf("Error unmarshalling user.deletion_requested payload: %v\n", err)
		return
	}

	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		log.Printf("Invalid user ID in user.deletion_requested event: %s\n", p.UserID)
		return
	}

	// Mark the app-level user record as deleted
	if err := c.svc.SoftDeleteUser(ctx, userID); err != nil {
		log.Printf("Failed to soft-delete user %s: %v\n", userID, err)
	} else {
		log.Printf("Soft-deleted user record for %s\n", userID)
	}
}

func (c *Consumer) handleUserEndorsed(ctx context.Context, payload json.RawMessage) {
	var p events.UserEndorsedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		log.Printf("Error unmarshalling UserEndorsed payload: %v\n", err)
		return
	}

	toUserID, err := uuid.Parse(p.ToUserID)
	if err != nil {
		log.Printf("Invalid to_user_id in endorsement event: %s\n", p.ToUserID)
		return
	}

	// Recalculate reputation trust score based on endorsement count
	rep, err := c.svc.GetReputation(ctx, toUserID)
	if err != nil {
		log.Printf("Failed to get reputation for %s: %v\n", toUserID, err)
		return
	}

	// Simple trust score formula: base 0.50 + 0.05 per endorsement, capped at 1.00
	newScore := 0.50 + float64(rep.EndorsementCount)*0.05
	if newScore > 1.00 {
		newScore = 1.00
	}

	if newScore != rep.TrustScore {
		log.Printf("Reputation recalc for %s: endorsements=%d, trust_score=%.2f\n", toUserID, rep.EndorsementCount, newScore)
	}
}

// handleSellerApproved activates the linked business page + sets seller_id on it.
func (c *Consumer) handleSellerApproved(ctx context.Context, payload json.RawMessage) {
	var p struct {
		SellerID       string `json:"seller_id"`
		BusinessPageID string `json:"business_page_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		slog.Error("unmarshal seller.approved", "error", err)
		return
	}

	if p.BusinessPageID == "" {
		return // seller has no linked page — nothing to activate
	}

	pageID, err := uuid.Parse(p.BusinessPageID)
	if err != nil {
		slog.Error("invalid business_page_id in seller.approved", "value", p.BusinessPageID)
		return
	}
	sellerID, err := uuid.Parse(p.SellerID)
	if err != nil {
		slog.Error("invalid seller_id in seller.approved", "value", p.SellerID)
		return
	}

	// Link seller → page and activate it
	if err := c.svc.SetBusinessPageSellerID(ctx, pageID, sellerID); err != nil {
		slog.Error("set seller_id on page", "page_id", pageID, "error", err)
	}
	if err := c.svc.ActivateBusinessPage(ctx, pageID); err != nil {
		slog.Error("activate business page", "page_id", pageID, "error", err)
	} else {
		slog.Info("activated business page after seller approval", "page_id", pageID, "seller_id", sellerID)
	}
}
