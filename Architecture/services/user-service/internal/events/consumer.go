package events

import (
	"context"
	"encoding/json"
	"log"

	"github.com/facebook-like/shared/events"
	"github.com/facebook-like/user-service/internal/service"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Consumer struct {
	reader *kafka.Reader
	svc    *service.Service
}

func NewConsumer(brokers []string, topic string, svc *service.Service) *Consumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: "user-service-group",
	})
	return &Consumer{reader: r, svc: svc}
}

func (c *Consumer) Start(ctx context.Context) {
	log.Println("Starting Kafka consumer...")
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			log.Printf("Error reading message: %v\n", err)
			break
		}

		var envelope events.EventEnvelope
		if err := json.Unmarshal(m.Value, &envelope); err != nil {
			log.Printf("Error unmarshalling event: %v\n", err)
			continue
		}

		switch envelope.EventType {
		case events.UserRegistered:
			c.handleUserRegistered(ctx, envelope.Payload)
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
