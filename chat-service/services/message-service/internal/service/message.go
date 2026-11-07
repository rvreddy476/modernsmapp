// The chat Service: the store and producer interfaces it depends on, the
// Service struct with its constructor and optional dependency setters, the
// shared sentinel errors, per-user rate limits, the idempotency wrapper and
// the outbox relay hook. Conversations, sending, reading, editing,
// reactions and presence live in their own files in this package.

package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/ratelimit"
	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/atpost/chat-shared/presence"
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

// --- Interfaces ---

type ConversationStore interface {
	CreateDirectConversation(ctx context.Context, userA, userB, createdBy uuid.UUID) (uuid.UUID, bool, error)
	SeverDirectConversation(ctx context.Context, blockerID, blockedID uuid.UUID) (uuid.UUID, []postgres.SeveredMembership, error)
	MarkConversationAsRequest(ctx context.Context, conversationID uuid.UUID) error
	CreateMessageRequest(ctx context.Context, convID, senderID, receiverID uuid.UUID) error
	GetMessageRequestByConversation(ctx context.Context, convID uuid.UUID) (*postgres.MessageRequest, error)
	SetMessageRequestPreview(ctx context.Context, convID uuid.UUID, preview string) error
	// RedactUnsentMessageRecords is the Postgres half of unsend's content
	// removal (postgres.ConversationStore.RedactUnsentMessageRecords). It
	// was an optional interface, so a store without it unsent silently
	// keeping every copy; it is required now.
	RedactUnsentMessageRecords(ctx context.Context, conversationID, messageID uuid.UUID) error
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
	// P0-3 dating-match support.
	CreateDatingMatchConversation(ctx context.Context, userA, userB, matchID uuid.UUID) (uuid.UUID, bool, error)
	MarkConversationClosedByMatch(ctx context.Context, matchID uuid.UUID) error
	HasOpenDatingMatch(ctx context.Context, userA, userB uuid.UUID) (bool, error)
	GetConversationMeta(ctx context.Context, conversationID uuid.UUID) (*postgres.ConversationMeta, error)
	ReplaceLastMessage(ctx context.Context, conversationID uuid.UUID, deletedTs time.Time, preview string, senderID *uuid.UUID, ts *time.Time) error
}

type MessageStore interface {
	CreateMessage(ctx context.Context, msg *scylla.Message) error
	GetMessage(ctx context.Context, conversationID uuid.UUID, bucket string, ts time.Time, msgID uuid.UUID) (*scylla.Message, error)
	GetMessages(ctx context.Context, conversationID uuid.UUID, cursor *scylla.MessageCursor, limit int, oldestBucket string) ([]scylla.Message, *scylla.MessageCursor, error)
	SoftDeleteMessage(ctx context.Context, conversationID uuid.UUID, bucket string, ts time.Time, msgID uuid.UUID) error
	UpdateMessageText(ctx context.Context, conversationID uuid.UUID, bucket string, ts time.Time, msgID uuid.UUID, text string, editedAt time.Time) error
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

// HiddenUserStore reports which of the given users are hidden by account
// control (chat.user_profiles.hidden).
type HiddenUserStore interface {
	HiddenUsers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error)
}

// SetPurgeAckProducer wires the producer the outbox relay uses for
// user.purge_acked rows (platform.purge-acks.v1).
func (s *Service) SetPurgeAckProducer(p EventProducer) { s.purgeAckProducer = p }

// SetHiddenUserStore wires the account-control hidden lookup.
func (s *Service) SetHiddenUserStore(h HiddenUserStore) { s.hiddenUsers = h }

// isHidden reports whether one user is hidden; a lookup failure fails
// closed (treated as hidden) because silence is always the safe answer for
// presence and typing.
func (s *Service) isHidden(ctx context.Context, userID uuid.UUID) bool {
	if s.hiddenUsers == nil {
		return false
	}
	m, err := s.hiddenUsers.HiddenUsers(ctx, []uuid.UUID{userID})
	if err != nil {
		s.log.Warn("hidden-user lookup failed; treating as hidden", "user_id", userID, "err", err)
		return true
	}
	return m[userID]
}

// --- Service ---

type Service struct {
	convStore   ConversationStore
	msgStore    MessageStore
	rdb         *redis.Client
	pres        presence.Store
	rateLimiter rateLimitAllower
	producer    EventProducer
	log         *slog.Logger
	// purgeAckProducer writes user.purge_acked outbox rows to the
	// platform purge-acks topic (account control). Optional.
	purgeAckProducer EventProducer
	// hiddenUsers answers "is this user deactivated / deletion-scheduled":
	// hidden users are reported offline and never publish typing. Optional.
	hiddenUsers        HiddenUserStore
	pollInterval       time.Duration
	userServiceURL     string
	identityUserURL    string
	graphServiceURL    string
	mediaServiceURL    string
	inviteLinkBaseURL  string
	internalServiceKey string
	entitlementSecret  string
	// datingServiceURL is where the first-message worker reports dating
	// conversations' first messages (Dating lane D4).
	datingServiceURL string
	httpClient       *http.Client
	// datingMatchState answers whether a dating match's members may still
	// chat (dating backend audit H2 send gate, dating_match_gate.go); nil
	// asks dating-service over HTTP. datingStates caches the answers.
	datingMatchState datingMatchStateChecker
	datingStates     datingMatchStateCache

	// Chat parity (lane B1). nowFn pins the clock in tests; searchServiceURL
	// empty means /v1/chat/search returns no people or messages;
	// trustSafetyURL is where message reports are filed.
	nowFn            func() time.Time
	searchServiceURL string
	trustSafetyURL   string

	// hiddenChatsDisabled is HIDDEN_CHATS_ENABLED=false (hidden_chats.go).
	hiddenChatsDisabled bool
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

	// ErrMatchClosed is returned when SendMessage targets a dating-match
	// conversation whose match has ended (unmatched, expired, or one side
	// blocked / deleted / paused). P0-3 in dating/PRODUCTION_GAP_ANALYSIS.md.
	ErrMatchClosed = errors.New("dating match is closed; no further messages allowed")
)

func New(convStore ConversationStore, msgStore MessageStore, rdb *redis.Client, producer EventProducer, log *slog.Logger, pollInterval time.Duration) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		convStore:    convStore,
		msgStore:     msgStore,
		rdb:          rdb,
		pres:         presence.NewRedisStore(rdb, presence.Options{}),
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

// SetIdentityAuthority wires the identity user-service that owns
// usr.user_settings — the authority the chat policy projection refreshes
// from. This is a DIFFERENT deployment from the legacy user directory above:
// pointing the policy fetch at the directory yields empty 200s and every
// unknown-policy caller fails closed out of chat.
func (s *Service) SetIdentityAuthority(identityUserURL string) {
	s.identityUserURL = strings.TrimRight(identityUserURL, "/")
}

// SetGraphService wires the graph-service base URL used for DM permission
// checks (spec §9.8). Without it, DM gating fails closed (see checkMessagePermission).
func (s *Service) SetGraphService(graphServiceURL string) {
	s.graphServiceURL = strings.TrimRight(graphServiceURL, "/")
}

func (s *Service) SetMediaService(mediaServiceURL string) {
	s.mediaServiceURL = strings.TrimRight(mediaServiceURL, "/")
}

// SetInviteLinkBaseURL sets the public prefix group invite codes are appended
// to (chat-app pass). Empty disables the url field.
func (s *Service) SetInviteLinkBaseURL(base string) {
	s.inviteLinkBaseURL = base
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
	s.relayOutbox(ctx)
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
