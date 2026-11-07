package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/community-service/internal/store"
	"github.com/atpost/shared/events"
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
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		// Hash, not LeastBytes: LeastBytes ignores the key, so keying the
		// lifecycle events by community id would order nothing.
		Balancer: &kafka.Hash{},
		Dialer:   dialer,
	})
	return &Producer{writer: w}
}

// PublishCommunityCreated announces a new community, with the row as created.
func (p *Producer) PublishCommunityCreated(ctx context.Context, c *store.Community) error {
	payload := NewCommunityCreatedPayload(c, time.Now())
	ownerID := c.OwnerID
	return p.publishKeyed(ctx, EventCommunityCreated, &ownerID, payload.CommunityID, payload)
}

// PublishCommunityUpdated announces a change to a community, including a
// change of status (the GDPR archive; any future suspension). c is the row
// after the change; the payload carries its status, its visibility (derived
// from the type by the one public set, store.PublicCommunityTypes, so a
// consumer need not keep its own copy of that list) and the whole row.
func (p *Producer) PublishCommunityUpdated(ctx context.Context, c *store.Community, actorID uuid.UUID) error {
	payload := NewCommunityUpdatedPayload(c, actorID, time.Now())
	return p.publishKeyed(ctx, EventCommunityUpdated, &actorID, payload.CommunityID, payload)
}

func (p *Producer) PublishCommunityDeleted(ctx context.Context, communityID, actorID uuid.UUID) error {
	payload := CommunityDeletedPayload{
		CommunityID: communityID.String(),
		ActorID:     actorID.String(),
		DeletedAt:   time.Now(),
	}
	return p.publishKeyed(ctx, EventCommunityDeleted, &actorID, payload.CommunityID, payload)
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
		CommunityID:   communityID.String(),
		SpaceID:       spaceID.String(),
		ActorID:       actorID.String(),
		QuarantinedAt: time.Now(),
	}
	return p.publish(ctx, EventCommunitySpaceQuarantined, &actorID, payload)
}

// publish serializes the envelope synchronously (so the caller's
// payload references are safe) and writes to Kafka asynchronously.
//
// Audit HC4: every Publish* method previously blocked the request on
// the Kafka WriteMessages ACK (50-200 ms). At 50+ call sites the cost
// added up. Moving the I/O off the request context means we no longer
// fail user actions because the broker is briefly slow; we also no
// longer tie request cancellation to whether the event lands.
//
// Errors are logged at WARN. The community-events topic is consumed
// downstream by feed/notification/search, which already tolerate
// at-least-once + duplicates via dedup; a dropped event becomes a
// best-effort reconciliation problem rather than a user-facing failure.
func (p *Producer) publish(ctx context.Context, eventType string, actorID *uuid.UUID, payload any) error {
	return p.publishKeyed(ctx, eventType, actorID, "", payload)
}

// publishKeyed is publish with a partition key. The lifecycle events
// (created, updated, deleted) are keyed by community id so they land on one
// partition in the order they were written: search applies "archived" and
// must not then apply a stale "active" edit, which would upsert the
// community back (search-service applyCommunitySnapshot). An empty key falls
// back to the event id, which spreads the member and space events.
func (p *Producer) publishKeyed(ctx context.Context, eventType string, actorID *uuid.UUID, key string, payload any) error {
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

	if key == "" {
		key = envelope.EventID
	}
	// Ordering is per partition and per write: two edits published within
	// the same few milliseconds race in the goroutines below. Rare for a
	// human edit; the communities backfill is the repair.
	msg := kafka.Message{
		Key:   []byte(key),
		Value: envelopeBytes,
	}
	go func() {
		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.writer.WriteMessages(writeCtx, msg); err != nil {
			slog.Warn("community-events: async Kafka publish failed",
				"event_type", eventType, "event_id", envelope.EventID, "err", err)
		}
	}()
	return nil
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

// CommunitySnapshot is the community row as committed, every field current
// (an empty description is empty, not "unchanged"). It rides
// community.created and community.updated (2026-11-08) so a consumer writes
// the whole document instead of guessing from a few fields: search upserts it,
// which brings back a community that was made public again. Contract:
// testdata/contracts/community_updated.json, copied byte for byte into
// search-service.
type CommunitySnapshot struct {
	CommunityID string `json:"community_id"`
	OwnerID     string `json:"owner_id"`
	Handle      string `json:"handle"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// CommunityType is the authority for who may find the community;
	// Visibility (public|private) is the wire field derived from it.
	CommunityType string   `json:"community_type"`
	Visibility    string   `json:"visibility"`
	Category      string   `json:"category"`
	TopicTags     []string `json:"topic_tags"`
	MemberCount   int64    `json:"member_count"`
	IsVerified    bool     `json:"is_verified"`
	// Status: active | suspended | archived | deleted. Only active is
	// servable.
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// visibilityOf is the only place the wire visibility is derived.
func visibilityOf(communityType string) string {
	if store.IsPublicCommunityType(communityType) {
		return "public"
	}
	return "private"
}

// SnapshotOf builds the snapshot from the row.
func SnapshotOf(c *store.Community) CommunitySnapshot {
	tags := c.TopicTags
	if tags == nil {
		tags = []string{}
	}
	return CommunitySnapshot{
		CommunityID:   c.ID.String(),
		OwnerID:       c.OwnerID.String(),
		Handle:        c.Handle,
		Name:          c.Name,
		Description:   c.Description,
		CommunityType: c.CommunityType,
		Visibility:    visibilityOf(c.CommunityType),
		Category:      c.Category,
		TopicTags:     tags,
		MemberCount:   c.MemberCount,
		IsVerified:    c.IsVerified,
		Status:        c.Status,
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
	}
}

type CommunityCreatedPayload struct {
	CommunityID   string    `json:"community_id"`
	OwnerID       string    `json:"owner_id"`
	Name          string    `json:"name"`
	CommunityType string    `json:"community_type"`
	CreatedAt     time.Time `json:"created_at"`
	// Community is the full row (added 2026-11-08; absent on older events,
	// which carried the four fields above only).
	Community *CommunitySnapshot `json:"community,omitempty"`
}

// NewCommunityCreatedPayload keeps the original top-level fields for older
// consumers and adds the snapshot.
func NewCommunityCreatedPayload(c *store.Community, now time.Time) CommunityCreatedPayload {
	snap := SnapshotOf(c)
	created := c.CreatedAt
	if created.IsZero() {
		created = now
	}
	return CommunityCreatedPayload{
		CommunityID:   snap.CommunityID,
		OwnerID:       snap.OwnerID,
		Name:          snap.Name,
		CommunityType: snap.CommunityType,
		CreatedAt:     created,
		Community:     &snap,
	}
}

type CommunityUpdatedPayload struct {
	CommunityID   string `json:"community_id"`
	ActorID       string `json:"actor_id"`
	Name          string `json:"name,omitempty"`
	CommunityType string `json:"community_type,omitempty"`
	// Status is the community's status after the change: active, archived
	// or suspended. A consumer that lists communities drops any that is not
	// active.
	Status string `json:"status,omitempty"`
	// Visibility is "public" when anyone may read the community's content
	// (store.PublicCommunityTypes) and "private" otherwise.
	Visibility string    `json:"visibility,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
	// Community is the full row after the change (added 2026-11-08; absent
	// on older events).
	Community *CommunitySnapshot `json:"community,omitempty"`
}

// NewCommunityUpdatedPayload builds community.updated from the row after the
// change.
func NewCommunityUpdatedPayload(c *store.Community, actorID uuid.UUID, now time.Time) CommunityUpdatedPayload {
	snap := SnapshotOf(c)
	return CommunityUpdatedPayload{
		CommunityID:   snap.CommunityID,
		ActorID:       actorID.String(),
		Name:          snap.Name,
		CommunityType: snap.CommunityType,
		Status:        snap.Status,
		Visibility:    snap.Visibility,
		UpdatedAt:     now,
		Community:     &snap,
	}
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
