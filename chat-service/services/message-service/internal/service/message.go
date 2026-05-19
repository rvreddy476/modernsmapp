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
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/chat-message-service/internal/ratelimit"
	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Per-user chat rate limits (messaging/privacy spec §10.4).
var (
	// dmRateLimit caps direct-message sends at 60 per 60s per user.
	dmRateLimit = ratelimit.Limit{Action: "dm", Max: 60, Window: 60 * time.Second}
	// messageRequestRateLimit caps message-request creation at 20 per 24h
	// per user to blunt mass cold-outreach spam.
	messageRequestRateLimit = ratelimit.Limit{Action: "message_request", Max: 20, Window: 24 * time.Hour}
)

// bareDomainRe matches an unadorned domain (e.g. "example.com") so a message
// request's first message cannot smuggle a link without an http(s):// prefix.
var bareDomainRe = regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9-]*\.(com|net|org|io|co|in|me|app|dev|xyz|info|link|to|ly|gg|sh)\b`)

// containsLink reports whether text contains a URL or bare domain. Message
// requests are link-free (spec §9.5) to blunt the most common spam vector.
func containsLink(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") || strings.Contains(lower, "www.") {
		return true
	}
	return bareDomainRe.MatchString(text)
}

// --- Interfaces ---

type ConversationStore interface {
	CreateDirectConversation(ctx context.Context, userA, userB, createdBy uuid.UUID) (uuid.UUID, bool, error)
	MarkConversationAsRequest(ctx context.Context, conversationID uuid.UUID) error
	CreateMessageRequest(ctx context.Context, convID, senderID, receiverID uuid.UUID) error
	GetMessageRequestByConversation(ctx context.Context, convID uuid.UUID) (*postgres.MessageRequest, error)
	SetMessageRequestPreview(ctx context.Context, convID uuid.UUID, preview string) error
	UpdateMessageRequestStatus(ctx context.Context, convID uuid.UUID, status string) error
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
	IsRequest bool                `json:"is_request"`
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
	rateLimiter        *ratelimit.Limiter
	producer           EventProducer
	log                *slog.Logger
	pollInterval       time.Duration
	userServiceURL     string
	graphServiceURL    string
	internalServiceKey string
	httpClient         *http.Client
}

var (
	ErrIdempotencyKeyRequired = errors.New("idempotency key is required")
	ErrIdempotencyConflict    = errors.New("idempotency key reused with different request")
	ErrIdempotencyInProgress  = errors.New("request with this idempotency key is still processing")

	// ErrMessagingNotAllowed is returned when the actor's privacy/relationship
	// state permits neither a direct DM nor a message request to the target
	// (messaging/privacy spec v2 §4).
	ErrMessagingNotAllowed = errors.New("messaging this user is not permitted")
	// ErrRequestFirstMessageInvalid is returned when a message request's first
	// message violates the text-only / no-link / 500-char constraints (§9.5).
	ErrRequestFirstMessageInvalid = errors.New("message request first message is invalid")
	// ErrAwaitingRequestAcceptance is returned when a sender tries to send a
	// follow-up before the recipient has accepted the request.
	ErrAwaitingRequestAcceptance = errors.New("awaiting message request acceptance")
	// ErrRateLimited is returned when a per-user chat rate limit is exceeded
	// (spec §10.4). Mapped to HTTP 429.
	ErrRateLimited = errors.New("rate limit exceeded")
)

func New(convStore ConversationStore, msgStore MessageStore, rdb *redis.Client, producer EventProducer, log *slog.Logger, pollInterval time.Duration) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		convStore:    convStore,
		msgStore:     msgStore,
		rdb:          rdb,
		rateLimiter:  ratelimit.New(rdb),
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

// SetGraphService wires the graph-service base URL used for DM permission
// checks (spec §9.8). Without it, DM gating fails closed (see checkMessagePermission).
func (s *Service) SetGraphService(graphServiceURL string) {
	s.graphServiceURL = strings.TrimRight(graphServiceURL, "/")
}

// checkRateLimit enforces a per-user chat rate limit (spec §10.4). It returns
// ErrRateLimited when the quota is exceeded. A Redis failure fails open (the
// action is allowed) and is logged rather than surfaced to the caller.
func (s *Service) checkRateLimit(ctx context.Context, limit ratelimit.Limit, userID uuid.UUID) error {
	if s.rateLimiter == nil {
		return nil
	}
	allowed, err := s.rateLimiter.Allow(ctx, limit, userID.String())
	if err != nil {
		s.log.Warn("rate limiter check failed — failing open", "err", err, "action", limit.Action, "user_id", userID)
		return nil
	}
	if !allowed {
		return ErrRateLimited
	}
	return nil
}

// --- Conversations ---

func (s *Service) CreateDirectConversation(ctx context.Context, userID, otherID uuid.UUID, idempotencyKey string) (*ConversationResponse, error) {
	if userID == otherID {
		return nil, errors.New("cannot create conversation with self")
	}

	// DM gating (spec §1, §4): a non-connection cannot silently open a direct
	// DM. Depending on the target's privacy + relationship state the attempt
	// is permitted, downgraded to a Message Request, or rejected outright.
	allowed, asRequest, err := s.checkMessagePermission(ctx, userID, otherID)
	if err != nil {
		return nil, err
	}
	if !allowed && !asRequest {
		return nil, ErrMessagingNotAllowed
	}

	req := struct {
		UserID  uuid.UUID `json:"user_id"`
		OtherID uuid.UUID `json:"other_id"`
	}{UserID: userID, OtherID: otherID}
	return withIdempotency(ctx, s, idempotencyKey, req, func() (*ConversationResponse, error) {
		convID, created, err := s.convStore.CreateDirectConversation(ctx, userID, otherID, userID)
		if err != nil {
			return nil, err
		}
		// Downgrade to a Message Request only for a brand-new conversation —
		// an existing conversation means the pair could already talk.
		if !allowed && asRequest && created {
			// Rate-limit message-request creation (spec §10.4): a single
			// user may open at most messageRequestRateLimit.Max new
			// requests per window. Checked only on the actual creation
			// path so idempotent retries / existing conversations are free.
			if err := s.checkRateLimit(ctx, messageRequestRateLimit, userID); err != nil {
				return nil, err
			}
			if err := s.convStore.MarkConversationAsRequest(ctx, convID); err != nil {
				return nil, err
			}
			if err := s.convStore.CreateMessageRequest(ctx, convID, userID, otherID); err != nil {
				return nil, err
			}
		}
		return s.getConversationResponse(ctx, convID)
	})
}

// checkMessagePermission asks graph-service whether actor may DM target.
// Returns (allowed, asRequest): allowed=true means a direct DM is permitted;
// allowed=false + asRequest=true means it must route through a Message
// Request; both false means messaging is not permitted at all.
func (s *Service) checkMessagePermission(ctx context.Context, actorID, targetID uuid.UUID) (allowed bool, asRequest bool, err error) {
	if s.graphServiceURL == "" {
		// Deploy misconfiguration. Fail closed on direct DMs but keep the
		// request path open so messaging is degraded, not bricked.
		s.log.Warn("GRAPH_SERVICE_URL not configured — DM gating degraded to request-only")
		return false, true, nil
	}
	url := fmt.Sprintf("%s/v1/permissions/check?target_user_id=%s&actions=message", s.graphServiceURL, targetID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, false, err
	}
	req.Header.Set("X-User-Id", actorID.String())
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false, false, fmt.Errorf("permission check request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, false, fmt.Errorf("permission check returned %d: %s", resp.StatusCode, string(body))
	}

	var envelope struct {
		Data struct {
			Decisions struct {
				Message struct {
					Allowed  bool   `json:"allowed"`
					Fallback string `json:"fallback"`
				} `json:"message"`
			} `json:"decisions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false, false, fmt.Errorf("decode permission response: %w", err)
	}
	d := envelope.Data.Decisions.Message
	return d.Allowed, d.Fallback == "message_request", nil
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
			IsRequest: c.IsRequest,
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
		// DM rate limit (spec §10.4): cap message sends per user per window.
		// Inside the idempotency closure so a 429 releases the key and an
		// idempotent retry of a previously-accepted send returns the cache.
		if err := s.checkRateLimit(ctx, dmRateLimit, userID); err != nil {
			return nil, err
		}

		// Message-request gating (spec §3.3, §9.5): in a pending request
		// conversation only the original sender may post, and only a single
		// constrained first message until the recipient accepts. This runs
		// inside the idempotency closure so an idempotent retry returns the
		// cached response instead of being rejected as a follow-up.
		firstRequestMessage := false
		conv, err := s.convStore.GetConversation(ctx, conversationID)
		if err != nil {
			return nil, err
		}
		if conv != nil && conv.IsRequest {
			mr, err := s.convStore.GetMessageRequestByConversation(ctx, conversationID)
			if err != nil {
				return nil, err
			}
			if mr == nil || mr.Status != "pending" {
				return nil, ErrAwaitingRequestAcceptance
			}
			if userID != mr.SenderID {
				// The recipient must accept the request before replying.
				return nil, ErrAwaitingRequestAcceptance
			}
			if mr.Preview != "" {
				// The one allowed first message was already sent.
				return nil, ErrAwaitingRequestAcceptance
			}
			if msgType != "text" || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("%w: first message must be non-empty text", ErrRequestFirstMessageInvalid)
			}
			if containsLink(text) {
				return nil, fmt.Errorf("%w: links are not allowed", ErrRequestFirstMessageInvalid)
			}
			if utf8.RuneCountInString(text) > 500 {
				return nil, fmt.Errorf("%w: exceeds 500 characters", ErrRequestFirstMessageInvalid)
			}
			firstRequestMessage = true
		}

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
		recipientIDs := make([]string, 0, len(members))
		for _, m := range members {
			if m.UserID != userID {
				recipientIDs = append(recipientIDs, m.UserID.String())
			}
		}
		outboxPayload := sharedEvents.MessageCreatedPayload{
			MessageID:      msgID.String(),
			ConversationID: conversationID.String(),
			SenderID:       userID.String(),
			Type:           msgType,
			RecipientIDs:   recipientIDs,
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

		// 4b. Message-request first message: store it as the request preview
		// and emit MessageRequestCreated so notify routes it to the
		// recipient's Requests folder (spec §3.3, §18.2).
		if firstRequestMessage {
			if err := s.convStore.SetMessageRequestPreview(ctx, conversationID, text); err != nil {
				l.Warn("failed to set message request preview", "err", err)
			}
			var receiverID uuid.UUID
			for _, m := range members {
				if m.UserID != userID {
					receiverID = m.UserID
					break
				}
			}
			_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MessageRequestCreated, sharedEvents.MessageRequestPayload{
				ConversationID: conversationID.String(),
				SenderID:       userID.String(),
				ReceiverID:     receiverID.String(),
				Preview:        text,
				OccurredAt:     now,
			})
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
		IsRequest: conv.IsRequest,
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
