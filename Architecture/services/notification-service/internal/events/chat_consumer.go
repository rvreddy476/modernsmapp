package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Chat event-type strings. Declared locally — chat-service lives in a
// separate Go workspace, so we mirror the contract here rather than
// importing across modules (same pattern the commerce/dating/rider
// consumers use for foreign event types).
const (
	chatEventMessageCreated        = "MessageCreated"
	chatEventMessageRequestCreated = "MessageRequestCreated"
)

// chatEnvelope mirrors the JSON envelope chat-service publishes to the
// chat.events.v1 topic: {event_id, event_type, occurred_at,
// actor_user_id, payload}. It is intentionally distinct from
// shared/events.EventEnvelope because chat-service is a separate module
// with its own envelope shape.
type chatEnvelope struct {
	EventID     string          `json:"event_id"`
	EventType   string          `json:"event_type"`
	OccurredAt  time.Time       `json:"occurred_at"`
	ActorUserID string          `json:"actor_user_id"`
	Payload     json.RawMessage `json:"payload"`
}

// messageCreatedPayload is the payload of a MessageCreated chat event.
type messageCreatedPayload struct {
	MessageID      string    `json:"message_id"`
	ConversationID string    `json:"conversation_id"`
	SenderID       string    `json:"sender_id"`
	Type           string    `json:"type"`
	CreatedAt      time.Time `json:"created_at"`
	RecipientIDs   []string  `json:"recipient_ids"`
}

// messageRequestCreatedPayload is the payload of a MessageRequestCreated
// chat event.
type messageRequestCreatedPayload struct {
	ConversationID string    `json:"conversation_id"`
	SenderID       string    `json:"sender_id"`
	ReceiverID     string    `json:"receiver_id"`
	Preview        string    `json:"preview"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// ChatConsumer handles direct-message and message-request notification
// events from the chat.events.v1 topic. Modeled on CallConsumer — a
// separate consumer/group so chat-event lag never blocks social-event
// notification delivery.
type ChatConsumer struct {
	reader  *kafka.Reader
	service *service.Service
}

func NewChatConsumer(brokers []string, groupID string, topic string, svc *service.Service) *ChatConsumer {
	return NewChatConsumerWithDialer(brokers, groupID, topic, svc, nil)
}

func NewChatConsumerWithDialer(brokers []string, groupID string, topic string, svc *service.Service, dialer *kafka.Dialer) *ChatConsumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	return &ChatConsumer{
		reader:  reader,
		service: svc,
	}
}

func (c *ChatConsumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("chat consumer shutting down\n")
			} else {
				log.Printf("Chat consumer error: %v\n", err)
			}
			break
		}

		if err := c.processMessage(ctx, m); err != nil {
			log.Printf("Failed to process chat message: %v\n", err)
		}
	}
}

func (c *ChatConsumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope chatEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return err
	}

	switch envelope.EventType {
	case chatEventMessageCreated:
		var e messageCreatedPayload
		if err := json.Unmarshal(envelope.Payload, &e); err != nil {
			return err
		}
		return c.handleMessageCreated(ctx, e)

	case chatEventMessageRequestCreated:
		var e messageRequestCreatedPayload
		if err := json.Unmarshal(envelope.Payload, &e); err != nil {
			return err
		}
		return c.handleMessageRequestCreated(ctx, e)

	default:
		// Other chat.events.v1 event types are claimed and ignored.
		return nil
	}
}

// handleMessageCreated fans out a DM notification to every recipient of a
// new chat message, skipping the sender. Notifications carry entity type
// "conversation" so the collapse-key logic groups every message in one
// conversation into a single device notification.
func (c *ChatConsumer) handleMessageCreated(ctx context.Context, e messageCreatedPayload) error {
	senderID, err := uuid.Parse(e.SenderID)
	if err != nil {
		return nil // bad payload — don't retry
	}
	conversationID, err := uuid.Parse(e.ConversationID)
	if err != nil {
		return nil
	}

	createdAt := e.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	deepLink := fmt.Sprintf("/messages/%s", e.ConversationID)

	var firstErr error
	for _, ridStr := range e.RecipientIDs {
		recipientID, err := uuid.Parse(ridStr)
		if err != nil {
			continue
		}
		// Skip the sender — they don't get notified of their own message.
		if recipientID == senderID {
			continue
		}
		if err := c.service.CreateNotification(
			ctx, recipientID, senderID, "dm", "conversation", conversationID, deepLink, createdAt,
		); err != nil {
			log.Printf("chat: failed to create dm notification for %s: %v\n", ridStr, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// handleMessageRequestCreated creates a single message-request
// notification for the receiver. All pending requests collapse into one
// quiet notification via the "message_request" collapse-key case.
func (c *ChatConsumer) handleMessageRequestCreated(ctx context.Context, e messageRequestCreatedPayload) error {
	senderID, err := uuid.Parse(e.SenderID)
	if err != nil {
		return nil
	}
	receiverID, err := uuid.Parse(e.ReceiverID)
	if err != nil {
		return nil
	}
	if receiverID == senderID {
		return nil
	}
	conversationID, err := uuid.Parse(e.ConversationID)
	if err != nil {
		return nil
	}

	occurredAt := e.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	// Deep link to the message-requests folder rather than the
	// conversation itself — requests are reviewed in a dedicated inbox.
	deepLink := "/messages/requests"

	return c.service.CreateNotification(
		ctx, receiverID, senderID, "message_request", "conversation", conversationID, deepLink, occurredAt,
	)
}

func (c *ChatConsumer) Close() error {
	return c.reader.Close()
}
