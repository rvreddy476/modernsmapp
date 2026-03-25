package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// CallConsumer handles call-related notification events from the call.notifications topic.
type CallConsumer struct {
	reader  *kafka.Reader
	service *service.Service
}

func NewCallConsumer(brokers []string, groupID string, topic string, svc *service.Service) *CallConsumer {
	return NewCallConsumerWithDialer(brokers, groupID, topic, svc, nil)
}

func NewCallConsumerWithDialer(brokers []string, groupID string, topic string, svc *service.Service, dialer *kafka.Dialer) *CallConsumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	return &CallConsumer{
		reader:  reader,
		service: svc,
	}
}

func (c *CallConsumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			log.Printf("Call consumer error: %v\n", err)
			break
		}

		if err := c.processMessage(ctx, m); err != nil {
			log.Printf("Failed to process call message: %v\n", err)
		}
	}
}

func (c *CallConsumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return err
	}

	switch envelope.EventType {
	case events.EventCallInvited:
		var e events.CallInvitedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		inviterID, _ := uuid.Parse(e.InviterUserID)
		inviteeID, _ := uuid.Parse(e.InviteeUserID)
		callID, _ := uuid.Parse(e.CallID)

		notifType := "incoming_call"
		if e.CallType == "video" {
			notifType = "incoming_video_call"
		}
		deepLink := fmt.Sprintf("/call/%s", e.CallID)
		return c.service.CreateNotification(ctx, inviteeID, inviterID, notifType, "call", callID, deepLink, e.CreatedAt)

	case events.EventCallEnded:
		var e events.CallEndedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return err
		}

		if e.EndedReason != "missed" && e.EndedReason != "no_answer" {
			return nil
		}

		initiatorID, _ := uuid.Parse(e.InitiatorUserID)
		callID, _ := uuid.Parse(e.CallID)

		deepLink := fmt.Sprintf("/call/history?callId=%s", e.CallID)
		return c.service.CreateNotification(ctx, initiatorID, uuid.Nil, "missed_call", "call", callID, deepLink, e.EndedAt)

	default:
		return nil
	}
}

func (c *CallConsumer) Close() error {
	return c.reader.Close()
}
