package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
}

func NewProducer(brokers []string, topic string) *Producer {
	w := &kafka.Writer{
		Addr:     kafka.TCP(brokers...),
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
	}
	return &Producer{writer: w}
}

func (p *Producer) PublishCommunityCreated(ctx context.Context, communityID, ownerID uuid.UUID, name, communityType string) error {
	payload := CommunityCreatedPayload{
		CommunityID:   communityID.String(),
		OwnerID:       ownerID.String(),
		Name:          name,
		CommunityType: communityType,
		CreatedAt:     time.Now(),
	}
	return p.publish(ctx, EventCommunityCreated, &ownerID, payload)
}

func (p *Producer) PublishCommunityUpdated(ctx context.Context, communityID, actorID uuid.UUID) error {
	payload := CommunityUpdatedPayload{
		CommunityID: communityID.String(),
		ActorID:     actorID.String(),
		UpdatedAt:   time.Now(),
	}
	return p.publish(ctx, EventCommunityUpdated, &actorID, payload)
}

func (p *Producer) PublishCommunityDeleted(ctx context.Context, communityID, actorID uuid.UUID) error {
	payload := CommunityDeletedPayload{
		CommunityID: communityID.String(),
		ActorID:     actorID.String(),
		DeletedAt:   time.Now(),
	}
	return p.publish(ctx, EventCommunityDeleted, &actorID, payload)
}

func (p *Producer) PublishMemberJoined(ctx context.Context, communityID, userID uuid.UUID) error {
	payload := CommunityMemberJoinedPayload{
		CommunityID: communityID.String(),
		UserID:      userID.String(),
		JoinedAt:    time.Now(),
	}
	return p.publish(ctx, EventCommunityMemberJoined, &userID, payload)
}

func (p *Producer) PublishMemberLeft(ctx context.Context, communityID, userID uuid.UUID) error {
	payload := CommunityMemberLeftPayload{
		CommunityID: communityID.String(),
		UserID:      userID.String(),
		LeftAt:      time.Now(),
	}
	return p.publish(ctx, EventCommunityMemberLeft, &userID, payload)
}

func (p *Producer) PublishMemberBanned(ctx context.Context, communityID, userID, bannedBy uuid.UUID) error {
	payload := CommunityMemberBannedPayload{
		CommunityID: communityID.String(),
		UserID:      userID.String(),
		BannedBy:    bannedBy.String(),
		BannedAt:    time.Now(),
	}
	return p.publish(ctx, EventCommunityMemberBanned, &bannedBy, payload)
}

func (p *Producer) PublishMemberRoleChanged(ctx context.Context, communityID, userID, actorID uuid.UUID, newRole string) error {
	payload := CommunityMemberRoleChangedPayload{
		CommunityID: communityID.String(),
		UserID:      userID.String(),
		ActorID:     actorID.String(),
		NewRole:     newRole,
		ChangedAt:   time.Now(),
	}
	return p.publish(ctx, EventCommunityMemberRoleChanged, &actorID, payload)
}

func (p *Producer) PublishSpaceCreated(ctx context.Context, communityID, spaceID, creatorID uuid.UUID, name, spaceType string) error {
	payload := CommunitySpaceCreatedPayload{
		CommunityID: communityID.String(),
		SpaceID:     spaceID.String(),
		CreatorID:   creatorID.String(),
		Name:        name,
		SpaceType:   spaceType,
		CreatedAt:   time.Now(),
	}
	return p.publish(ctx, EventCommunitySpaceCreated, &creatorID, payload)
}

func (p *Producer) PublishSpaceRemoved(ctx context.Context, communityID, spaceID, actorID uuid.UUID) error {
	payload := CommunitySpaceRemovedPayload{
		CommunityID: communityID.String(),
		SpaceID:     spaceID.String(),
		ActorID:     actorID.String(),
		RemovedAt:   time.Now(),
	}
	return p.publish(ctx, EventCommunitySpaceRemoved, &actorID, payload)
}

func (p *Producer) PublishSpaceQuarantined(ctx context.Context, communityID, spaceID, actorID uuid.UUID) error {
	payload := CommunitySpaceQuarantinedPayload{
		CommunityID:  communityID.String(),
		SpaceID:      spaceID.String(),
		ActorID:      actorID.String(),
		QuarantinedAt: time.Now(),
	}
	return p.publish(ctx, EventCommunitySpaceQuarantined, &actorID, payload)
}

func (p *Producer) publish(ctx context.Context, eventType string, actorID *uuid.UUID, payload any) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	var actorStr *string
	if actorID != nil {
		s := actorID.String()
		actorStr = &s
	}

	envelope := events.NewEnvelope(ctx, eventType, actorStr, payloadBytes)

	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}

	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(envelope.EventID),
		Value: envelopeBytes,
	})
}

func (p *Producer) Close() error {
	return p.writer.Close()
}

// --- Event type constants ---

const (
	EventCommunityCreated           = "community.created"
	EventCommunityUpdated           = "community.updated"
	EventCommunityDeleted           = "community.deleted"
	EventCommunityMemberJoined      = "community.member.joined"
	EventCommunityMemberLeft        = "community.member.left"
	EventCommunityMemberBanned      = "community.member.banned"
	EventCommunityMemberRoleChanged = "community.member.role_changed"
	EventCommunitySpaceCreated      = "community.space.created"
	EventCommunitySpaceRemoved      = "community.space.removed"
	EventCommunitySpaceQuarantined  = "community.space.quarantined"
)

// --- Payload types ---

type CommunityCreatedPayload struct {
	CommunityID   string    `json:"community_id"`
	OwnerID       string    `json:"owner_id"`
	Name          string    `json:"name"`
	CommunityType string    `json:"community_type"`
	CreatedAt     time.Time `json:"created_at"`
}

type CommunityUpdatedPayload struct {
	CommunityID string    `json:"community_id"`
	ActorID     string    `json:"actor_id"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CommunityDeletedPayload struct {
	CommunityID string    `json:"community_id"`
	ActorID     string    `json:"actor_id"`
	DeletedAt   time.Time `json:"deleted_at"`
}

type CommunityMemberJoinedPayload struct {
	CommunityID string    `json:"community_id"`
	UserID      string    `json:"user_id"`
	JoinedAt    time.Time `json:"joined_at"`
}

type CommunityMemberLeftPayload struct {
	CommunityID string    `json:"community_id"`
	UserID      string    `json:"user_id"`
	LeftAt      time.Time `json:"left_at"`
}

type CommunityMemberBannedPayload struct {
	CommunityID string    `json:"community_id"`
	UserID      string    `json:"user_id"`
	BannedBy    string    `json:"banned_by"`
	BannedAt    time.Time `json:"banned_at"`
}

type CommunityMemberRoleChangedPayload struct {
	CommunityID string    `json:"community_id"`
	UserID      string    `json:"user_id"`
	ActorID     string    `json:"actor_id"`
	NewRole     string    `json:"new_role"`
	ChangedAt   time.Time `json:"changed_at"`
}

type CommunitySpaceCreatedPayload struct {
	CommunityID string    `json:"community_id"`
	SpaceID     string    `json:"space_id"`
	CreatorID   string    `json:"creator_id"`
	Name        string    `json:"name"`
	SpaceType   string    `json:"space_type"`
	CreatedAt   time.Time `json:"created_at"`
}

type CommunitySpaceRemovedPayload struct {
	CommunityID string    `json:"community_id"`
	SpaceID     string    `json:"space_id"`
	ActorID     string    `json:"actor_id"`
	RemovedAt   time.Time `json:"removed_at"`
}

type CommunitySpaceQuarantinedPayload struct {
	CommunityID   string    `json:"community_id"`
	SpaceID       string    `json:"space_id"`
	ActorID       string    `json:"actor_id"`
	QuarantinedAt time.Time `json:"quarantined_at"`
}
