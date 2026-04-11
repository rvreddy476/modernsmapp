package events

import (
	"context"
	"encoding/json"

	"github.com/atpost/qa-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

type communityCreatedPayload struct {
	CommunityID   string `json:"community_id"`
	OwnerID       string `json:"owner_id"`
	Name          string `json:"name"`
	CommunityType string `json:"community_type"`
}

type communityUpdatedPayload struct {
	CommunityID   string `json:"community_id"`
	Name          string `json:"name,omitempty"`
	CommunityType string `json:"community_type,omitempty"`
}

type communityDeletedPayload struct {
	CommunityID string `json:"community_id"`
}

type communityMemberJoinedPayload struct {
	CommunityID string `json:"community_id"`
	UserID      string `json:"user_id"`
}

type communityMemberLeftPayload struct {
	CommunityID string `json:"community_id"`
	UserID      string `json:"user_id"`
}

type communityMemberBannedPayload struct {
	CommunityID string `json:"community_id"`
	UserID      string `json:"user_id"`
}

type communityMemberRoleChangedPayload struct {
	CommunityID string `json:"community_id"`
	UserID      string `json:"user_id"`
	NewRole     string `json:"new_role"`
}

func (c *Consumer) handleCommunityCreated(ctx context.Context, envelope events.EventEnvelope) error {
	var payload communityCreatedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	communityID, err := uuid.Parse(payload.CommunityID)
	if err != nil {
		return err
	}
	ownerID, err := uuid.Parse(payload.OwnerID)
	if err != nil {
		return err
	}

	return c.store.UpsertCommunity(ctx, store.MirroredCommunity{
		ID:            communityID,
		OwnerID:       ownerID,
		Name:          payload.Name,
		CommunityType: payload.CommunityType,
		Status:        "active",
	})
}

func (c *Consumer) handleCommunityUpdated(ctx context.Context, envelope events.EventEnvelope) error {
	var payload communityUpdatedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	communityID, err := uuid.Parse(payload.CommunityID)
	if err != nil {
		return err
	}

	if payload.Name == "" && payload.CommunityType == "" {
		return c.store.TouchCommunity(ctx, communityID)
	}

	current, err := c.store.GetCommunity(ctx, communityID)
	if err != nil {
		return nil
	}
	if payload.Name != "" {
		current.Name = payload.Name
	}
	if payload.CommunityType != "" {
		current.CommunityType = payload.CommunityType
	}
	return c.store.UpsertCommunity(ctx, *current)
}

func (c *Consumer) handleCommunityDeleted(ctx context.Context, envelope events.EventEnvelope) error {
	var payload communityDeletedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	communityID, err := uuid.Parse(payload.CommunityID)
	if err != nil {
		return err
	}
	return c.store.DeleteCommunitySync(ctx, communityID)
}

func (c *Consumer) handleCommunityMemberJoined(ctx context.Context, envelope events.EventEnvelope) error {
	var payload communityMemberJoinedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	communityID, err := uuid.Parse(payload.CommunityID)
	if err != nil {
		return err
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return err
	}
	return c.store.UpsertCommunityMember(ctx, communityID, userID, "member")
}

func (c *Consumer) handleCommunityMemberLeft(ctx context.Context, envelope events.EventEnvelope) error {
	var payload communityMemberLeftPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	communityID, err := uuid.Parse(payload.CommunityID)
	if err != nil {
		return err
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return err
	}
	return c.store.RemoveCommunityMember(ctx, communityID, userID)
}

func (c *Consumer) handleCommunityMemberBanned(ctx context.Context, envelope events.EventEnvelope) error {
	var payload communityMemberBannedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	communityID, err := uuid.Parse(payload.CommunityID)
	if err != nil {
		return err
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return err
	}
	return c.store.UpsertCommunityMember(ctx, communityID, userID, "banned")
}

func (c *Consumer) handleCommunityMemberRoleChanged(ctx context.Context, envelope events.EventEnvelope) error {
	var payload communityMemberRoleChangedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}

	communityID, err := uuid.Parse(payload.CommunityID)
	if err != nil {
		return err
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return err
	}
	return c.store.UpsertCommunityMember(ctx, communityID, userID, payload.NewRole)
}
