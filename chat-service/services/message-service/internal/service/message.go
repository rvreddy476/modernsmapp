package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// --- Interfaces ---

type ConversationStore interface {
	CreateDirectConversation(ctx context.Context, userA, userB, createdBy uuid.UUID) (uuid.UUID, error)
	CreateGroupConversation(ctx context.Context, creatorID uuid.UUID, title string, memberIDs []uuid.UUID) (uuid.UUID, error)
	GetConversation(ctx context.Context, id uuid.UUID) (*postgres.Conversation, error)
	TouchConversation(ctx context.Context, id uuid.UUID, ts time.Time) error
	ListConversationsByUser(ctx context.Context, userID uuid.UUID, limit int, cursorUpdatedAt *time.Time, cursorID *uuid.UUID) ([]postgres.Conversation, error)
	CheckMembership(ctx context.Context, conversationID, userID uuid.UUID) (bool, error)
	GetMembers(ctx context.Context, conversationID uuid.UUID) ([]postgres.Member, error)
	GetMemberRole(ctx context.Context, conversationID, userID uuid.UUID) (string, error)
	AddMember(ctx context.Context, conversationID, userID uuid.UUID, role string) error
	RemoveMember(ctx context.Context, conversationID, userID uuid.UUID) (bool, error)
	UpdateTitle(ctx context.Context, conversationID uuid.UUID, title string) error
	InsertOutboxEvent(ctx context.Context, eventType string, payload interface{}) error
	FetchUnpublishedOutboxEvents(ctx context.Context, limit int) ([]postgres.OutboxEvent, error)
	MarkOutboxEventPublished(ctx context.Context, id int64) error
	CheckIdempotencyKey(ctx context.Context, key string) (*postgres.IdempotencyResult, error)
	CreateIdempotencyKey(ctx context.Context, key, requestHash string) (bool, error)
	SaveIdempotencyResponse(ctx context.Context, key, requestHash string, response interface{}) error
	ReleaseIdempotencyKey(ctx context.Context, key, requestHash string) error
	// User profile cache
	UpsertUserProfile(ctx context.Context, userID uuid.UUID, displayName string, avatarMediaID *uuid.UUID) error
	GetUserProfiles(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]postgres.UserProfile, error)
}

type MessageStore interface {
	CreateMessage(ctx context.Context, msg *scylla.Message) error
	GetMessage(ctx context.Context, conversationID uuid.UUID, bucket string, ts time.Time, msgID uuid.UUID) (*scylla.Message, error)
	GetMessages(ctx context.Context, conversationID uuid.UUID, cursor *scylla.MessageCursor, limit int) ([]scylla.Message, *scylla.MessageCursor, error)
	SoftDeleteMessage(ctx context.Context, conversationID uuid.UUID, bucket string, ts time.Time, msgID uuid.UUID) error
	UpsertInbox(ctx context.Context, userID, conversationID, senderID uuid.UUID, text string, ts time.Time) error
	AddReaction(ctx context.Context, convID uuid.UUID, bucket string, msgTs time.Time, msgID uuid.UUID, emoji string, userID uuid.UUID) error
	RemoveReaction(ctx context.Context, convID uuid.UUID, bucket string, msgTs time.Time, msgID uuid.UUID, emoji string, userID uuid.UUID) error
	HasReaction(ctx context.Context, convID uuid.UUID, bucket string, msgTs time.Time, msgID uuid.UUID, emoji string, userID uuid.UUID) (bool, error)
	GetReactionsForMessages(ctx context.Context, convID uuid.UUID, bucket string, keys []scylla.MsgKey) (map[uuid.UUID][]scylla.Reaction, error)
}

type EventProducer interface {
	PublishRaw(ctx context.Context, eventType string, partitionKey string, payloadBytes json.RawMessage) error
	Close() error
}

// --- Response Types ---

type MemberWithProfile struct {
	UserID        uuid.UUID  `json:"user_id"`
	Role          string     `json:"role"`
	JoinedAt      time.Time  `json:"joined_at"`
	DisplayName   string     `json:"display_name,omitempty"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
}

type ConversationResponse struct {
	ID        uuid.UUID           `json:"id"`
	Type      string              `json:"type"`
	Title     *string             `json:"title,omitempty"`
	CreatedBy *uuid.UUID          `json:"created_by,omitempty"`
	Members   []MemberWithProfile `json:"members"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

type ReactionSummary struct {
	Emoji   string   `json:"emoji"`
	UserIDs []string `json:"user_ids"`
}

type MessageResponse struct {
	ConversationID    uuid.UUID         `json:"conversation_id"`
	Bucket            string            `json:"bucket"`
	Ts                time.Time         `json:"ts"`
	MsgID             uuid.UUID         `json:"msg_id"`
	SenderID          uuid.UUID         `json:"sender_id"`
	SenderDisplayName string            `json:"sender_display_name,omitempty"`
	Type              string            `json:"type"`
	Text              string            `json:"text,omitempty"`
	MediaID           *uuid.UUID        `json:"media_id,omitempty"`
	Reactions         []ReactionSummary `json:"reactions,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
}

type ToggleReactionResponse struct {
	Added     bool      `json:"added"`
	Emoji     string    `json:"emoji"`
	MessageID uuid.UUID `json:"message_id"`
}

type ConversationCursor struct {
	UpdatedAt time.Time `json:"updated_at"`
	ID        uuid.UUID `json:"id"`
}

// --- Service ---

type Service struct {
	convStore          ConversationStore
	msgStore           MessageStore
	rdb                *redis.Client
	producer           EventProducer
	log                *slog.Logger
	pollInterval       time.Duration
	userServiceURL     string
	internalServiceKey string
	httpClient         *http.Client
}

var (
	ErrIdempotencyKeyRequired = errors.New("idempotency key is required")
	ErrIdempotencyConflict    = errors.New("idempotency key reused with different request")
	ErrIdempotencyInProgress  = errors.New("request with this idempotency key is still processing")
)

func New(convStore ConversationStore, msgStore MessageStore, rdb *redis.Client, producer EventProducer, log *slog.Logger, pollInterval time.Duration) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		convStore:    convStore,
		msgStore:     msgStore,
		rdb:          rdb,
		producer:     producer,
		log:          log,
		pollInterval: pollInterval,
		httpClient:   &http.Client{Timeout: 5 * time.Second},
	}
}

func (s *Service) SetUserDirectory(userServiceURL, internalServiceKey string) {
	s.userServiceURL = strings.TrimRight(userServiceURL, "/")
	s.internalServiceKey = internalServiceKey
}

// --- Conversations ---

func (s *Service) CreateDirectConversation(ctx context.Context, userID, otherID uuid.UUID, idempotencyKey string) (*ConversationResponse, error) {
	if userID == otherID {
		return nil, errors.New("cannot create conversation with self")
	}

	req := struct {
		UserID  uuid.UUID `json:"user_id"`
		OtherID uuid.UUID `json:"other_id"`
	}{UserID: userID, OtherID: otherID}
	return withIdempotency(ctx, s, idempotencyKey, req, func() (*ConversationResponse, error) {
		convID, err := s.convStore.CreateDirectConversation(ctx, userID, otherID, userID)
		if err != nil {
			return nil, err
		}
		return s.getConversationResponse(ctx, convID)
	})
}

func (s *Service) CreateGroupConversation(ctx context.Context, userID uuid.UUID, title string, memberIDs []uuid.UUID, idempotencyKey string) (*ConversationResponse, error) {
	if title == "" {
		return nil, errors.New("group title is required")
	}
	if len(memberIDs) < 1 {
		return nil, errors.New("at least one other member is required")
	}

	req := struct {
		UserID    uuid.UUID   `json:"user_id"`
		Title     string      `json:"title"`
		MemberIDs []uuid.UUID `json:"member_ids"`
	}{UserID: userID, Title: title, MemberIDs: memberIDs}
	return withIdempotency(ctx, s, idempotencyKey, req, func() (*ConversationResponse, error) {
		convID, err := s.convStore.CreateGroupConversation(ctx, userID, title, memberIDs)
		if err != nil {
			return nil, err
		}

		// Outbox event
		allMembers := make([]string, 0, len(memberIDs)+1)
		allMembers = append(allMembers, userID.String())
		for _, m := range memberIDs {
			if m != userID {
				allMembers = append(allMembers, m.String())
			}
		}
		_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.ConversationCreated, sharedEvents.ConversationCreatedPayload{
			ConversationID: convID.String(),
			Type:           "group",
			Title:          title,
			CreatedBy:      userID.String(),
			MemberIDs:      allMembers,
			CreatedAt:      time.Now(),
		})

		return s.getConversationResponse(ctx, convID)
	})
}

func (s *Service) GetConversation(ctx context.Context, userID, conversationID uuid.UUID) (*ConversationResponse, error) {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a conversation member")
	}
	return s.getConversationResponse(ctx, conversationID)
}

func (s *Service) ListConversations(ctx context.Context, userID uuid.UUID, limit int, cursor *ConversationCursor) ([]ConversationResponse, *ConversationCursor, error) {
	var cursorUpdatedAt *time.Time
	var cursorID *uuid.UUID
	if cursor != nil {
		cursorUpdatedAt = &cursor.UpdatedAt
		cursorID = &cursor.ID
	}

	convs, err := s.convStore.ListConversationsByUser(ctx, userID, limit, cursorUpdatedAt, cursorID)
	if err != nil {
		return nil, nil, err
	}

	out := make([]ConversationResponse, 0, len(convs))
	for _, c := range convs {
		members, err := s.convStore.GetMembers(ctx, c.ID)
		if err != nil {
			return nil, nil, err
		}
		enrichedMembers := s.enrichMembers(ctx, members)
		out = append(out, ConversationResponse{
			ID:        c.ID,
			Type:      c.Type,
			Title:     c.Title,
			CreatedBy: c.CreatedBy,
			Members:   enrichedMembers,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		})
	}

	var next *ConversationCursor
	if len(convs) == limit {
		last := convs[len(convs)-1]
		next = &ConversationCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	return out, next, nil
}

// --- Member Management ---

func (s *Service) AddMember(ctx context.Context, userID, conversationID, targetUserID uuid.UUID) error {
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conv == nil {
		return errors.New("conversation not found")
	}
	if conv.Type != "group" {
		return errors.New("can only add members to group conversations")
	}

	role, err := s.convStore.GetMemberRole(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if role != "admin" {
		return errors.New("only admins can add members")
	}
	targetIsMember, err := s.convStore.CheckMembership(ctx, conversationID, targetUserID)
	if err != nil {
		return err
	}
	if targetIsMember {
		return errors.New("user is already a conversation member")
	}

	if err := s.convStore.AddMember(ctx, conversationID, targetUserID, "member"); err != nil {
		return err
	}

	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MemberAdded, sharedEvents.MemberAddedPayload{
		ConversationID: conversationID.String(),
		UserID:         targetUserID.String(),
		AddedBy:        userID.String(),
		Role:           "member",
		AddedAt:        time.Now(),
	})

	return nil
}

func (s *Service) RemoveMember(ctx context.Context, userID, conversationID, targetUserID uuid.UUID) error {
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conv == nil {
		return errors.New("conversation not found")
	}
	if conv.Type != "group" {
		return errors.New("can only remove members from group conversations")
	}
	requesterIsMember, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !requesterIsMember {
		return errors.New("not a conversation member")
	}
	targetIsMember, err := s.convStore.CheckMembership(ctx, conversationID, targetUserID)
	if err != nil {
		return err
	}
	if !targetIsMember {
		return errors.New("target user is not a conversation member")
	}
	targetRole, err := s.convStore.GetMemberRole(ctx, conversationID, targetUserID)
	if err != nil {
		return err
	}

	// Self-removal is always allowed; otherwise must be admin
	if userID != targetUserID {
		role, err := s.convStore.GetMemberRole(ctx, conversationID, userID)
		if err != nil {
			return err
		}
		if role != "admin" {
			return errors.New("only admins can remove other members")
		}
	}
	if targetRole == "admin" {
		members, err := s.convStore.GetMembers(ctx, conversationID)
		if err != nil {
			return err
		}
		adminCount := 0
		for _, member := range members {
			if member.Role == "admin" {
				adminCount++
			}
		}
		if adminCount <= 1 {
			return errors.New("cannot remove the last admin")
		}
	}

	removed, err := s.convStore.RemoveMember(ctx, conversationID, targetUserID)
	if err != nil {
		return err
	}
	if !removed {
		return errors.New("target user is not a conversation member")
	}

	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MemberRemoved, sharedEvents.MemberRemovedPayload{
		ConversationID: conversationID.String(),
		UserID:         targetUserID.String(),
		RemovedBy:      userID.String(),
		RemovedAt:      time.Now(),
	})

	return nil
}

func (s *Service) UpdateTitle(ctx context.Context, userID, conversationID uuid.UUID, title string) error {
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conv == nil {
		return errors.New("conversation not found")
	}
	if conv.Type != "group" {
		return errors.New("can only update title of group conversations")
	}

	role, err := s.convStore.GetMemberRole(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if role != "admin" {
		return errors.New("only admins can update title")
	}

	return s.convStore.UpdateTitle(ctx, conversationID, title)
}

// --- Messages ---

func (s *Service) SendMessage(ctx context.Context, userID, conversationID uuid.UUID, msgType, text string, mediaID *uuid.UUID, idempotencyKey string) (*MessageResponse, error) {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a conversation member")
	}

	req := struct {
		UserID         uuid.UUID  `json:"user_id"`
		ConversationID uuid.UUID  `json:"conversation_id"`
		Type           string     `json:"type"`
		Text           string     `json:"text"`
		MediaID        *uuid.UUID `json:"media_id,omitempty"`
	}{UserID: userID, ConversationID: conversationID, Type: msgType, Text: text, MediaID: mediaID}
	return withIdempotency(ctx, s, idempotencyKey, req, func() (*MessageResponse, error) {
		now := time.Now()
		msgID := uuid.New()
		bucket := now.UTC().Format("200601")

		msg := &scylla.Message{
			ConversationID: conversationID,
			Bucket:         bucket,
			Ts:             now,
			MsgID:          msgID,
			SenderID:       userID,
			Type:           msgType,
			Text:           text,
			MediaID:        mediaID,
			IsDeleted:      false,
			CreatedAt:      now,
		}

		l := s.log.With("conversation_id", conversationID, "sender_id", userID, "msg_id", msgID)

		// 1. Persist to ScyllaDB
		if err := s.msgStore.CreateMessage(ctx, msg); err != nil {
			l.Error("failed to save message to scylladb", "err", err)
			return nil, err
		}

		// 2. Touch conversation timestamp
		_ = s.convStore.TouchConversation(ctx, conversationID, now)

		// 3. Update inbox projection for all members (async)
		members, _ := s.convStore.GetMembers(ctx, conversationID)
		go func() {
			for _, m := range members {
				if err := s.msgStore.UpsertInbox(context.Background(), m.UserID, conversationID, userID, text, now); err != nil {
					l.Warn("failed to upsert inbox", "err", err, "member_id", m.UserID)
				}
			}
		}()

		// 4. Outbox event — critical synchronous handoff.
		//
		// Audit H5: this previously discarded the error with `_ =`. A
		// transient Postgres blip would silently drop the downstream
		// notification path (push, email) — the recipient would see the
		// message only if they were already connected via Redis pub/sub.
		//
		// Now: retry briefly to absorb transient failures, then log
		// CRITICAL if the row never lands. We still return success to
		// the sender because Scylla has durably persisted the message;
		// the alternative (failing the request) would create a duplicate
		// on retry because the idempotency key gets released on error
		// and a fresh msgID is generated.
		outboxPayload := sharedEvents.MessageCreatedPayload{
			MessageID:      msgID.String(),
			ConversationID: conversationID.String(),
			SenderID:       userID.String(),
			Type:           msgType,
			CreatedAt:      now,
		}
		var outboxErr error
		for attempt := 0; attempt < 3; attempt++ {
			outboxErr = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MessageCreated, outboxPayload)
			if outboxErr == nil {
				break
			}
			if attempt < 2 {
				time.Sleep(time.Duration(100*(attempt+1)) * time.Millisecond)
			}
		}
		if outboxErr != nil {
			l.Error("CRITICAL: outbox insert failed for chat message — downstream notifications will be lost",
				"err", outboxErr)
		}

		// 5. Real-time delivery via Redis pub/sub to all members (best-effort).
		// Runs after the durable outbox handoff so a successful publish
		// always corresponds to a queued outbox event.
		go func() {
			pubCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			payload, _ := json.Marshal(map[string]interface{}{
				"type": "message",
				"payload": map[string]interface{}{
					"conversation_id": conversationID,
					"message_id":      msgID,
					"sender_id":       userID,
					"type":            msgType,
					"text":            text,
					"media_id":        mediaID,
					"created_at":      now,
				},
			})
			for _, m := range members {
				if m.UserID == userID {
					continue // Don't notify sender
				}
				channel := fmt.Sprintf("chat:%s", m.UserID)
				if err := s.rdb.Publish(pubCtx, channel, payload).Err(); err != nil {
					l.Warn("failed to publish to redis pubsub", "err", err, "member_id", m.UserID)
				}
			}
		}()

		// 6. Redis cache update (best-effort).
		go func() {
			cacheCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			key := fmt.Sprintf("chat_messages:%s", conversationID)
			data, _ := json.Marshal(msg)
			pipe := s.rdb.Pipeline()
			pipe.LPush(cacheCtx, key, data)
			pipe.LTrim(cacheCtx, key, 0, 99)
			if _, err := pipe.Exec(cacheCtx); err != nil {
				l.Warn("failed to update redis cache", "err", err)
			}
		}()

		return &MessageResponse{
			ConversationID: conversationID,
			Bucket:         bucket,
			Ts:             now,
			MsgID:          msgID,
			SenderID:       userID,
			Type:           msgType,
			Text:           text,
			MediaID:        mediaID,
			CreatedAt:      now,
		}, nil
	})
}

func (s *Service) GetMessages(ctx context.Context, userID, conversationID uuid.UUID, cursor *scylla.MessageCursor, limit int) ([]MessageResponse, *scylla.MessageCursor, error) {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, errors.New("not a conversation member")
	}

	messages, nextCursor, err := s.msgStore.GetMessages(ctx, conversationID, cursor, limit)
	if err != nil {
		return nil, nil, err
	}

	// Group messages by bucket to batch-fetch reactions.
	bucketKeys := make(map[string][]scylla.MsgKey)
	for _, m := range messages {
		bucketKeys[m.Bucket] = append(bucketKeys[m.Bucket], scylla.MsgKey{Ts: m.Ts, MsgID: m.MsgID})
	}

	allReactions := make(map[uuid.UUID][]scylla.Reaction)
	for bucket, keys := range bucketKeys {
		rxns, err := s.msgStore.GetReactionsForMessages(ctx, conversationID, bucket, keys)
		if err != nil {
			s.log.Warn("failed to fetch reactions", "err", err, "bucket", bucket)
			continue
		}
		for id, r := range rxns {
			allReactions[id] = append(allReactions[id], r...)
		}
	}

	// Batch-fetch sender profiles
	senderIDSet := make(map[uuid.UUID]struct{})
	for _, m := range messages {
		senderIDSet[m.SenderID] = struct{}{}
	}
	senderIDs := make([]uuid.UUID, 0, len(senderIDSet))
	for id := range senderIDSet {
		senderIDs = append(senderIDs, id)
	}
	senderProfiles, err := s.convStore.GetUserProfiles(ctx, senderIDs)
	if err != nil {
		s.log.Warn("failed to fetch sender profiles", "err", err)
		senderProfiles = map[uuid.UUID]postgres.UserProfile{}
	}

	out := make([]MessageResponse, 0, len(messages))
	for _, m := range messages {
		resp := MessageResponse{
			ConversationID: m.ConversationID,
			Bucket:         m.Bucket,
			Ts:             m.Ts,
			MsgID:          m.MsgID,
			SenderID:       m.SenderID,
			Type:           m.Type,
			Text:           m.Text,
			MediaID:        m.MediaID,
			CreatedAt:      m.CreatedAt,
		}
		if p, ok := senderProfiles[m.SenderID]; ok {
			resp.SenderDisplayName = p.DisplayName
		}
		if rxns, ok := allReactions[m.MsgID]; ok {
			resp.Reactions = aggregateReactions(rxns)
		}
		out = append(out, resp)
	}

	return out, nextCursor, nil
}

func (s *Service) DeleteMessage(ctx context.Context, userID, conversationID, messageID uuid.UUID, bucket string, ts time.Time) error {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a conversation member")
	}
	msg, err := s.msgStore.GetMessage(ctx, conversationID, bucket, ts, messageID)
	if err != nil {
		return err
	}
	if msg == nil || msg.IsDeleted {
		return errors.New("message not found")
	}
	authorized := msg.SenderID == userID
	if !authorized {
		conv, err := s.convStore.GetConversation(ctx, conversationID)
		if err != nil {
			return err
		}
		if conv == nil {
			return errors.New("conversation not found")
		}
		if conv.Type == "group" {
			role, err := s.convStore.GetMemberRole(ctx, conversationID, userID)
			if err != nil {
				return err
			}
			authorized = role == "admin"
		}
	}
	if !authorized {
		return errors.New("not allowed to delete this message")
	}

	if err := s.msgStore.SoftDeleteMessage(ctx, conversationID, bucket, ts, messageID); err != nil {
		return err
	}

	// Invalidate cache
	key := fmt.Sprintf("chat_messages:%s", conversationID)
	s.rdb.Del(ctx, key)

	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MessageDeleted, sharedEvents.MessageDeletedPayload{
		MessageID:      messageID.String(),
		ConversationID: conversationID.String(),
		DeletedBy:      userID.String(),
		DeletedAt:      time.Now(),
	})

	return nil
}

// --- Reactions ---

func (s *Service) ToggleReaction(ctx context.Context, userID, conversationID, messageID uuid.UUID, bucket string, ts time.Time, emoji string) (*ToggleReactionResponse, error) {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a conversation member")
	}

	// Verify the message exists
	msg, err := s.msgStore.GetMessage(ctx, conversationID, bucket, ts, messageID)
	if err != nil {
		return nil, err
	}
	if msg == nil || msg.IsDeleted {
		return nil, errors.New("message not found")
	}

	// Check if reaction already exists to determine toggle direction
	exists, err := s.msgStore.HasReaction(ctx, conversationID, bucket, ts, messageID, emoji, userID)
	if err != nil {
		return nil, err
	}

	added := !exists
	if exists {
		if err := s.msgStore.RemoveReaction(ctx, conversationID, bucket, ts, messageID, emoji, userID); err != nil {
			return nil, err
		}
	} else {
		if err := s.msgStore.AddReaction(ctx, conversationID, bucket, ts, messageID, emoji, userID); err != nil {
			return nil, err
		}
	}

	// Real-time delivery via Redis pub/sub to all members
	members, _ := s.convStore.GetMembers(ctx, conversationID)
	go func() {
		pubCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		payload, _ := json.Marshal(map[string]interface{}{
			"type": "reaction",
			"payload": map[string]interface{}{
				"conversation_id": conversationID,
				"message_id":      messageID,
				"user_id":         userID,
				"emoji":           emoji,
				"added":           added,
			},
		})
		for _, m := range members {
			if m.UserID == userID {
				continue
			}
			channel := fmt.Sprintf("chat:%s", m.UserID)
			if err := s.rdb.Publish(pubCtx, channel, payload).Err(); err != nil {
				s.log.Warn("failed to publish reaction to redis", "err", err, "member_id", m.UserID)
			}
		}
	}()

	// Outbox event
	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.ReactionToggled, sharedEvents.ReactionToggledPayload{
		MessageID:      messageID.String(),
		ConversationID: conversationID.String(),
		UserID:         userID.String(),
		Emoji:          emoji,
		Added:          added,
		OccurredAt:     time.Now(),
	})

	return &ToggleReactionResponse{
		Added:     added,
		Emoji:     emoji,
		MessageID: messageID,
	}, nil
}

// aggregateReactions groups flat reaction rows into emoji → user_ids summaries.
func aggregateReactions(rxns []scylla.Reaction) []ReactionSummary {
	emojiUsers := make(map[string][]string)
	for _, r := range rxns {
		emojiUsers[r.Emoji] = append(emojiUsers[r.Emoji], r.UserID.String())
	}
	out := make([]ReactionSummary, 0, len(emojiUsers))
	for emoji, userIDs := range emojiUsers {
		out = append(out, ReactionSummary{Emoji: emoji, UserIDs: userIDs})
	}
	return out
}

// --- Outbox Relay ---

func (s *Service) StartOutboxRelay(ctx context.Context) {
	s.log.Info("starting outbox relay", "poll_interval", s.pollInterval)
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Info("outbox relay stopped")
			return
		case <-ticker.C:
			s.processOutbox(ctx)
		}
	}
}

func (s *Service) processOutbox(ctx context.Context) {
	events, err := s.convStore.FetchUnpublishedOutboxEvents(ctx, 50)
	if err != nil {
		s.log.Error("failed to fetch outbox events", "err", err)
		return
	}

	for _, e := range events {
		if err := s.producer.PublishRaw(ctx, e.EventType, "", e.Payload); err != nil {
			s.log.Error("failed to publish outbox event", "err", err, "event_id", e.ID, "event_type", e.EventType)
			continue
		}
		if err := s.convStore.MarkOutboxEventPublished(ctx, e.ID); err != nil {
			s.log.Error("failed to mark outbox event published", "err", err, "event_id", e.ID)
		}
	}
}

// --- Helpers ---

func (s *Service) getConversationResponse(ctx context.Context, convID uuid.UUID) (*ConversationResponse, error) {
	conv, err := s.convStore.GetConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return nil, errors.New("conversation not found")
	}
	members, err := s.convStore.GetMembers(ctx, convID)
	if err != nil {
		return nil, err
	}
	enrichedMembers := s.enrichMembers(ctx, members)
	return &ConversationResponse{
		ID:        conv.ID,
		Type:      conv.Type,
		Title:     conv.Title,
		CreatedBy: conv.CreatedBy,
		Members:   enrichedMembers,
		CreatedAt: conv.CreatedAt,
		UpdatedAt: conv.UpdatedAt,
	}, nil
}

// enrichMembers batch-fetches user profiles and merges them with member data.
func (s *Service) enrichMembers(ctx context.Context, members []postgres.Member) []MemberWithProfile {
	userIDs := make([]uuid.UUID, len(members))
	for i, m := range members {
		userIDs[i] = m.UserID
	}

	profiles, err := s.convStore.GetUserProfiles(ctx, userIDs)
	if err != nil {
		s.log.Warn("failed to fetch user profiles for enrichment", "err", err)
		profiles = map[uuid.UUID]postgres.UserProfile{}
	}

	missingIDs := make([]uuid.UUID, 0, len(members))
	for _, m := range members {
		p, ok := profiles[m.UserID]
		if !ok || strings.TrimSpace(p.DisplayName) == "" {
			missingIDs = append(missingIDs, m.UserID)
		}
	}
	for id, profile := range s.fetchMissingProfiles(ctx, missingIDs) {
		profiles[id] = profile
	}

	out := make([]MemberWithProfile, len(members))
	for i, m := range members {
		mwp := MemberWithProfile{
			UserID:   m.UserID,
			Role:     m.Role,
			JoinedAt: m.JoinedAt,
		}
		if p, ok := profiles[m.UserID]; ok {
			mwp.DisplayName = p.DisplayName
			mwp.AvatarMediaID = p.AvatarMediaID
		}
		out[i] = mwp
	}
	return out
}

func (s *Service) fetchMissingProfiles(ctx context.Context, userIDs []uuid.UUID) map[uuid.UUID]postgres.UserProfile {
	if len(userIDs) == 0 || s.userServiceURL == "" {
		return map[uuid.UUID]postgres.UserProfile{}
	}

	fetched := make(map[uuid.UUID]postgres.UserProfile, len(userIDs))
	seen := make(map[uuid.UUID]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}

		profile, ok := s.fetchProfileFromUserService(ctx, userID)
		if !ok {
			continue
		}
		fetched[userID] = profile
		if err := s.convStore.UpsertUserProfile(ctx, userID, profile.DisplayName, profile.AvatarMediaID); err != nil {
			s.log.Warn("failed to cache user profile after fallback fetch", "user_id", userID, "err", err)
		}
	}
	return fetched
}

func (s *Service) fetchProfileFromUserService(ctx context.Context, userID uuid.UUID) (postgres.UserProfile, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/v1/users/%s", s.userServiceURL, userID), nil)
	if err != nil {
		s.log.Warn("failed to create user-service profile request", "user_id", userID, "err", err)
		return postgres.UserProfile{}, false
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.log.Warn("user-service profile request failed", "user_id", userID, "err", err)
		return postgres.UserProfile{}, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		s.log.Warn("user-service profile lookup returned non-200", "user_id", userID, "status", resp.StatusCode, "body", string(body))
		return postgres.UserProfile{}, false
	}

	var envelope struct {
		Data struct {
			DisplayName   string  `json:"display_name"`
			AvatarMediaID *string `json:"avatar_media_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		s.log.Warn("failed to decode user-service profile response", "user_id", userID, "err", err)
		return postgres.UserProfile{}, false
	}

	displayName := strings.TrimSpace(envelope.Data.DisplayName)
	if displayName == "" {
		return postgres.UserProfile{}, false
	}

	var avatarMediaID *uuid.UUID
	if envelope.Data.AvatarMediaID != nil {
		if parsed, err := uuid.Parse(*envelope.Data.AvatarMediaID); err == nil {
			avatarMediaID = &parsed
		}
	}

	return postgres.UserProfile{
		UserID:        userID,
		DisplayName:   displayName,
		AvatarMediaID: avatarMediaID,
		UpdatedAt:     time.Now(),
	}, true
}

func withIdempotency[T any](ctx context.Context, s *Service, key string, requestPayload interface{}, exec func() (*T, error)) (*T, error) {
	if key == "" {
		return nil, ErrIdempotencyKeyRequired
	}
	reqHash, err := hashRequestPayload(requestPayload)
	if err != nil {
		return nil, err
	}

	created, err := s.convStore.CreateIdempotencyKey(ctx, key, reqHash)
	if err != nil {
		return nil, err
	}
	if !created {
		existing, err := s.convStore.CheckIdempotencyKey(ctx, key)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, errors.New("idempotency key exists but was not found")
		}
		if existing.RequestHash != reqHash {
			return nil, ErrIdempotencyConflict
		}
		if len(existing.Response) == 0 || string(existing.Response) == "null" {
			return nil, ErrIdempotencyInProgress
		}

		var cached T
		if err := json.Unmarshal(existing.Response, &cached); err != nil {
			return nil, err
		}
		return &cached, nil
	}

	result, err := exec()
	if err != nil {
		_ = s.convStore.ReleaseIdempotencyKey(ctx, key, reqHash)
		return nil, err
	}
	if err := s.convStore.SaveIdempotencyResponse(ctx, key, reqHash, result); err != nil {
		_ = s.convStore.ReleaseIdempotencyKey(ctx, key, reqHash)
		return nil, err
	}
	return result, nil
}

func hashRequestPayload(payload interface{}) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum), nil
}

// SetTyping publishes a typing indicator to all conversation members via Redis PubSub.
func (s *Service) SetTyping(ctx context.Context, userID, conversationID uuid.UUID) error {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a member of this conversation")
	}

	// Set a short-lived key as typing indicator
	typingKey := fmt.Sprintf("typing:%s:%s", conversationID, userID)
	s.rdb.Set(ctx, typingKey, "1", 3*time.Second)

	// Broadcast to members
	members, _ := s.convStore.GetMembers(ctx, conversationID)
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "typing",
		"payload": map[string]interface{}{
			"conversation_id": conversationID.String(),
			"user_id":         userID.String(),
			"is_typing":       true,
		},
	})

	for _, m := range members {
		if m.UserID == userID {
			continue
		}
		channel := fmt.Sprintf("chat:%s", m.UserID)
		s.rdb.Publish(ctx, channel, payload)
	}

	return nil
}

// MarkRead updates the read cursor and broadcasts a read receipt via Redis PubSub.
func (s *Service) MarkRead(ctx context.Context, userID, conversationID uuid.UUID, messageID string) error {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a member of this conversation")
	}

	// Broadcast read receipt to members
	members, _ := s.convStore.GetMembers(ctx, conversationID)
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "read_receipt",
		"payload": map[string]interface{}{
			"conversation_id": conversationID.String(),
			"user_id":         userID.String(),
			"message_id":      messageID,
			"read_at":         time.Now().UTC().Format(time.RFC3339Nano),
		},
	})

	for _, m := range members {
		if m.UserID == userID {
			continue
		}
		channel := fmt.Sprintf("chat:%s", m.UserID)
		s.rdb.Publish(ctx, channel, payload)
	}

	return nil
}

// GetPresence checks which of the given user IDs are currently online
// by looking up their presence keys in Redis.
func (s *Service) GetPresence(ctx context.Context, userIDs []uuid.UUID) (map[string]bool, error) {
	if len(userIDs) == 0 {
		return map[string]bool{}, nil
	}

	keys := make([]string, len(userIDs))
	for i, id := range userIDs {
		keys[i] = "presence:" + id.String()
	}

	results, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		s.log.Warn("failed to check presence", "err", err)
		return map[string]bool{}, nil
	}

	presence := make(map[string]bool, len(userIDs))
	for i, id := range userIDs {
		presence[id.String()] = results[i] != nil
	}
	return presence, nil
}
