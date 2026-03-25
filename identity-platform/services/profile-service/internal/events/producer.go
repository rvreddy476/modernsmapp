package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sharedEvents "github.com/atpost/identity-shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
}

func NewProducer(brokers []string, topic string) *Producer {
	return NewProducerWithDialer(brokers, topic, nil)
}

func NewProducerWithDialer(brokers []string, topic string, dialer *kafka.Dialer) *Producer {
	return &Producer{writer: kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})}
}

func (p *Producer) PublishUserProfileUpdated(ctx context.Context, userID uuid.UUID, displayName, bio string, avatarMediaID *uuid.UUID, firstName, lastName string) error {
	var avatarStr *string
	if avatarMediaID != nil {
		s := avatarMediaID.String()
		avatarStr = &s
	}

	payload := sharedEvents.UserProfileUpdatedPayload{
		UserID:        userID.String(),
		DisplayName:   displayName,
		FirstName:     firstName,
		LastName:      lastName,
		Bio:           bio,
		AvatarMediaID: avatarStr,
		UpdatedAt:     time.Now(),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	actorStr := userID.String()
	envelope := sharedEvents.EventEnvelope{
		EventID:     uuid.New().String(),
		EventType:   sharedEvents.UserProfileUpdated,
		OccurredAt:  time.Now(),
		ActorUserID: &actorStr,
		Payload:     payloadBytes,
	}

	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}

	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(userID.String()),
		Value: envelopeBytes,
	})
}

// PublishFollowCreated emits a FollowCreated event.
func (p *Producer) PublishFollowCreated(ctx context.Context, followerID, followingID uuid.UUID) error {
	payload := sharedEvents.FollowPayload{
		ActorID:   followerID.String(),
		TargetID:  followingID.String(),
		Timestamp: time.Now(),
	}
	return p.publishEvent(ctx, sharedEvents.FollowCreated, followerID.String(), &followerID, payload)
}

// PublishFollowDeleted emits a FollowDeleted event.
func (p *Producer) PublishFollowDeleted(ctx context.Context, followerID, followingID uuid.UUID) error {
	payload := sharedEvents.FollowPayload{
		ActorID:   followerID.String(),
		TargetID:  followingID.String(),
		Timestamp: time.Now(),
	}
	return p.publishEvent(ctx, sharedEvents.FollowDeleted, followerID.String(), &followerID, payload)
}

// PublishFriendRequestSent emits a FriendRequestSent event.
func (p *Producer) PublishFriendRequestSent(ctx context.Context, requesterID, addresseeID uuid.UUID) error {
	payload := sharedEvents.FriendRequestPayload{
		RequesterID: requesterID.String(),
		AddresseeID: addresseeID.String(),
		Status:      "pending",
		Timestamp:   time.Now(),
	}
	return p.publishEvent(ctx, sharedEvents.FriendRequestSent, requesterID.String(), &requesterID, payload)
}

// PublishFriendRequestAccepted emits a FriendRequestAccepted event.
func (p *Producer) PublishFriendRequestAccepted(ctx context.Context, requesterID, addresseeID uuid.UUID) error {
	payload := sharedEvents.FriendRequestPayload{
		RequesterID: requesterID.String(),
		AddresseeID: addresseeID.String(),
		Status:      "accepted",
		Timestamp:   time.Now(),
	}
	return p.publishEvent(ctx, sharedEvents.FriendRequestAccepted, addresseeID.String(), &addresseeID, payload)
}

// PublishFriendRequestRejected emits a FriendRequestRejected event.
func (p *Producer) PublishFriendRequestRejected(ctx context.Context, requesterID, addresseeID uuid.UUID) error {
	payload := sharedEvents.FriendRequestPayload{
		RequesterID: requesterID.String(),
		AddresseeID: addresseeID.String(),
		Status:      "rejected",
		Timestamp:   time.Now(),
	}
	return p.publishEvent(ctx, sharedEvents.FriendRequestRejected, addresseeID.String(), &addresseeID, payload)
}

// PublishUserBlocked emits a UserBlocked event.
func (p *Producer) PublishUserBlocked(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	payload := sharedEvents.BlockPayload{
		BlockerID: blockerID.String(),
		BlockedID: blockedID.String(),
		Timestamp: time.Now(),
	}
	return p.publishEvent(ctx, sharedEvents.UserBlocked, blockerID.String(), &blockerID, payload)
}

// PublishUserUnblocked emits a UserUnblocked event.
func (p *Producer) PublishUserUnblocked(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	payload := sharedEvents.BlockPayload{
		BlockerID: blockerID.String(),
		BlockedID: blockedID.String(),
		Timestamp: time.Now(),
	}
	return p.publishEvent(ctx, sharedEvents.UserUnblocked, blockerID.String(), &blockerID, payload)
}

// publishEvent is a shared helper that wraps a payload in a CloudEvents-style envelope and publishes to Kafka.
func (p *Producer) publishEvent(ctx context.Context, eventType string, messageKey string, actorID *uuid.UUID, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	var actorStr *string
	if actorID != nil {
		s := actorID.String()
		actorStr = &s
	}

	envelope := sharedEvents.EventEnvelope{
		EventID:     uuid.New().String(),
		EventType:   eventType,
		OccurredAt:  time.Now(),
		ActorUserID: actorStr,
		Payload:     payloadBytes,
	}

	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}

	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(messageKey),
		Value: envelopeBytes,
	})
}

// PublishModuleProfileUpdated emits a ModuleProfileUpdated event.
func (p *Producer) PublishModuleProfileUpdated(ctx context.Context, userID uuid.UUID, module string) error {
	payload := sharedEvents.ModuleProfileUpdatedPayload{
		UserID:    userID.String(),
		Module:    module,
		UpdatedAt: time.Now(),
	}
	return p.publishEvent(ctx, sharedEvents.ModuleProfileUpdated, userID.String(), &userID, payload)
}

// PublishHandleChanged emits a HandleChanged event.
func (p *Producer) PublishHandleChanged(ctx context.Context, userID uuid.UUID, oldUsername, newUsername string) error {
	payload := sharedEvents.HandleChangedPayload{
		UserID:      userID.String(),
		OldUsername: oldUsername,
		NewUsername: newUsername,
		ChangedAt:   time.Now(),
	}
	return p.publishEvent(ctx, sharedEvents.HandleChanged, userID.String(), &userID, payload)
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
