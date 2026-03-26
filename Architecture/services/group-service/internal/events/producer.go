package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
	rdb    *redis.Client
}

func NewProducer(brokers []string, topic string, rdb *redis.Client) *Producer {
	return NewProducerWithDialer(brokers, topic, rdb, nil)
}

func NewProducerWithDialer(brokers []string, topic string, rdb *redis.Client, dialer *kafka.Dialer) *Producer {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	return &Producer{writer: w, rdb: rdb}
}

func (p *Producer) PublishGroupCreated(ctx context.Context, groupID, creatorID uuid.UUID, name, visibility string) error {
	payload := events.GroupCreatedPayload{
		GroupID:    groupID.String(),
		CreatorID:  creatorID.String(),
		Name:       name,
		Visibility: visibility,
		CreatedAt:  time.Now(),
	}
	return p.publish(ctx, events.GroupCreated, &creatorID, payload)
}

func (p *Producer) PublishGroupUpdated(ctx context.Context, groupID, actorID uuid.UUID) error {
	payload := events.GroupUpdatedPayload{
		GroupID:   groupID.String(),
		ActorID:   actorID.String(),
		UpdatedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupUpdated, &actorID, payload)
}

func (p *Producer) PublishGroupDeleted(ctx context.Context, groupID, actorID uuid.UUID) error {
	payload := events.GroupDeletedPayload{
		GroupID:   groupID.String(),
		ActorID:   actorID.String(),
		DeletedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupDeleted, &actorID, payload)
}

func (p *Producer) PublishGroupArchived(ctx context.Context, groupID, actorID uuid.UUID) error {
	payload := events.GroupArchivedPayload{
		GroupID:    groupID.String(),
		ActorID:    actorID.String(),
		ArchivedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupArchived, &actorID, payload)
}

func (p *Producer) PublishGroupMemberJoined(ctx context.Context, groupID, userID uuid.UUID, role string) error {
	payload := events.GroupMemberJoinedPayload{
		GroupID:  groupID.String(),
		UserID:   userID.String(),
		Role:     role,
		JoinedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupMemberJoined, &userID, payload)
}

func (p *Producer) PublishGroupMemberLeft(ctx context.Context, groupID, userID uuid.UUID) error {
	payload := events.GroupMemberLeftPayload{
		GroupID: groupID.String(),
		UserID:  userID.String(),
		LeftAt:  time.Now(),
	}
	return p.publish(ctx, events.GroupMemberLeft, &userID, payload)
}

func (p *Producer) PublishGroupMemberRemoved(ctx context.Context, groupID, userID, removedBy uuid.UUID) error {
	payload := events.GroupMemberRemovedPayload{
		GroupID:   groupID.String(),
		UserID:    userID.String(),
		RemovedBy: removedBy.String(),
		RemovedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupMemberRemoved, &removedBy, payload)
}

func (p *Producer) PublishGroupMemberBanned(ctx context.Context, groupID, userID, bannedBy uuid.UUID) error {
	payload := events.GroupMemberBannedPayload{
		GroupID:  groupID.String(),
		UserID:   userID.String(),
		BannedBy: bannedBy.String(),
		BannedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupMemberBanned, &bannedBy, payload)
}

func (p *Producer) PublishGroupMemberRoleChanged(ctx context.Context, groupID, userID, changedBy uuid.UUID, oldRole, newRole string) error {
	payload := events.GroupMemberRoleChangedPayload{
		GroupID:   groupID.String(),
		UserID:    userID.String(),
		OldRole:   oldRole,
		NewRole:   newRole,
		ChangedBy: changedBy.String(),
		ChangedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupMemberRoleChanged, &changedBy, payload)
}

func (p *Producer) PublishGroupPostCreated(ctx context.Context, groupID, postID, authorID uuid.UUID) error {
	payload := events.GroupPostCreatedPayload{
		GroupID:   groupID.String(),
		PostID:    postID.String(),
		AuthorID:  authorID.String(),
		CreatedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupPostCreated, &authorID, payload)
}

func (p *Producer) PublishGroupInviteSent(ctx context.Context, groupID, inviterID, inviteeID, inviteID uuid.UUID) error {
	payload := events.GroupInviteSentPayload{
		GroupID:   groupID.String(),
		InviterID: inviterID.String(),
		InviteeID: inviteeID.String(),
		InviteID:  inviteID.String(),
		SentAt:    time.Now(),
	}
	return p.publish(ctx, events.GroupInviteSent, &inviterID, payload)
}

func (p *Producer) PublishGroupInviteAccepted(ctx context.Context, groupID, inviteID, userID uuid.UUID) error {
	payload := events.GroupInviteAcceptedPayload{
		GroupID:    groupID.String(),
		InviteID:   inviteID.String(),
		UserID:     userID.String(),
		AcceptedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupInviteAccepted, &userID, payload)
}

func (p *Producer) PublishGroupInviteRejected(ctx context.Context, groupID, inviteID, userID uuid.UUID) error {
	payload := events.GroupInviteRejectedPayload{
		GroupID:    groupID.String(),
		InviteID:   inviteID.String(),
		UserID:     userID.String(),
		RejectedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupInviteRejected, &userID, payload)
}

func (p *Producer) PublishGroupJoinRequested(ctx context.Context, groupID, userID, requestID uuid.UUID) error {
	payload := events.GroupJoinRequestedPayload{
		GroupID:     groupID.String(),
		UserID:      userID.String(),
		RequestID:   requestID.String(),
		RequestedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupJoinRequested, &userID, payload)
}

func (p *Producer) PublishGroupJoinApproved(ctx context.Context, groupID, userID, requestID, approvedBy uuid.UUID) error {
	payload := events.GroupJoinApprovedPayload{
		GroupID:    groupID.String(),
		UserID:     userID.String(),
		RequestID:  requestID.String(),
		ApprovedBy: approvedBy.String(),
		ApprovedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupJoinApproved, &approvedBy, payload)
}

func (p *Producer) PublishGroupJoinRejected(ctx context.Context, groupID, userID, requestID, rejectedBy uuid.UUID) error {
	payload := events.GroupJoinRejectedPayload{
		GroupID:    groupID.String(),
		UserID:     userID.String(),
		RequestID:  requestID.String(),
		RejectedBy: rejectedBy.String(),
		RejectedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupJoinRejected, &rejectedBy, payload)
}

func (p *Producer) PublishGroupPostDeleted(ctx context.Context, groupID, postID, deletedBy uuid.UUID) error {
	payload := events.GroupPostDeletedPayload{
		GroupID:   groupID.String(),
		PostID:    postID.String(),
		DeletedBy: deletedBy.String(),
		DeletedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupPostDeleted, &deletedBy, payload)
}

func (p *Producer) PublishGroupPostPinned(ctx context.Context, groupID, postID, pinnedBy uuid.UUID) error {
	payload := events.GroupPostPinnedPayload{
		GroupID:  groupID.String(),
		PostID:   postID.String(),
		PinnedBy: pinnedBy.String(),
		PinnedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupPostPinned, &pinnedBy, payload)
}

func (p *Producer) PublishGroupPostUnpinned(ctx context.Context, groupID, postID, unpinnedBy uuid.UUID) error {
	payload := events.GroupPostUnpinnedPayload{
		GroupID:    groupID.String(),
		PostID:     postID.String(),
		UnpinnedBy: unpinnedBy.String(),
		UnpinnedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupPostUnpinned, &unpinnedBy, payload)
}

func (p *Producer) PublishMemberBanLifted(ctx context.Context, groupID, userID, liftedBy uuid.UUID) error {
	payload := events.MemberBanLiftedPayload{
		GroupID:  groupID.String(),
		UserID:   userID.String(),
		LiftedBy: liftedBy.String(),
		LiftedAt: time.Now(),
	}
	return p.publish(ctx, events.MemberBanLifted, &liftedBy, payload)
}

// PublishGroupPostCommented publishes comment created event to Kafka + Redis realtime.
func (p *Producer) PublishGroupPostCommented(ctx context.Context, groupID, postID, commentID, authorID uuid.UUID, body, parentID string) error {
	payload := events.GroupPostCommentedPayload{
		GroupID:   groupID.String(),
		PostID:    postID.String(),
		CommentID: commentID.String(),
		AuthorID:  authorID.String(),
		Body:      body,
		ParentID:  parentID,
		CreatedAt: time.Now(),
	}

	// Kafka (durable, cross-service)
	if err := p.publish(ctx, events.GroupPostCommented, &authorID, payload); err != nil {
		slog.Warn("kafka publish group.comment.created failed", "error", err)
	}

	// Redis pub/sub (realtime fanout to ws-gateway)
	p.publishRealtime(ctx, postID.String(), "comment_created", map[string]any{
		"event_id":   uuid.New().String(),
		"group_id":   groupID.String(),
		"post_id":    postID.String(),
		"comment_id": commentID.String(),
		"author_id":  authorID.String(),
		"body":       body,
		"parent_id":  parentID,
		"created_at": time.Now().Format(time.RFC3339Nano),
	})
	return nil
}

// PublishGroupPostCommentDeleted publishes comment deleted event to Kafka + Redis realtime.
func (p *Producer) PublishGroupPostCommentDeleted(ctx context.Context, groupID, postID, commentID, actorID uuid.UUID) error {
	// Redis pub/sub (realtime fanout)
	p.publishRealtime(ctx, postID.String(), "comment_deleted", map[string]any{
		"event_id":   uuid.New().String(),
		"group_id":   groupID.String(),
		"post_id":    postID.String(),
		"comment_id": commentID.String(),
		"actor_id":   actorID.String(),
	})
	return nil
}

func (p *Producer) PublishGroupPostSparked(ctx context.Context, groupID, postID, userID uuid.UUID) error {
	payload := events.GroupPostSparkedPayload{
		GroupID:   groupID.String(),
		PostID:    postID.String(),
		UserID:    userID.String(),
		SparkedAt: time.Now(),
	}
	return p.publish(ctx, events.GroupPostSparked, &userID, payload)
}

// publishRealtime sends a compact JSON event to Redis for ws-gateway fanout.
func (p *Producer) publishRealtime(ctx context.Context, postID, updateType string, payload map[string]any) {
	if p.rdb == nil {
		return
	}
	payload["update_type"] = updateType
	msg := map[string]any{
		"type":    "group_comment_update",
		"payload": payload,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Warn("failed to marshal realtime group comment event", "error", err)
		return
	}
	channel := fmt.Sprintf("group_post:%s", postID)
	if err := p.rdb.Publish(ctx, channel, string(data)).Err(); err != nil {
		slog.Warn("redis publish group comment event failed", "error", err, "channel", channel)
	}
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
