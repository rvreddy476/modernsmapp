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
	SeverDirectConversation(ctx context.Context, blockerID, blockedID uuid.UUID) (uuid.UUID, []postgres.SeveredMembership, error)
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
	// P0-3 dating-match support.
	CreateDatingMatchConversation(ctx context.Context, userA, userB, matchID uuid.UUID) (uuid.UUID, bool, error)
	MarkConversationClosedByMatch(ctx context.Context, matchID uuid.UUID) error
	GetConversationMeta(ctx context.Context, conversationID uuid.UUID) (*postgres.ConversationMeta, error)
	ReplaceLastMessage(ctx context.Context, conversationID uuid.UUID, deletedTs time.Time, preview string, senderID *uuid.UUID, ts *time.Time) error
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

	// Production chat pass: inbox metadata. HasUnread compares the viewer's
	// durable read cursor with the denormalized last-message timestamp;
	// preview text rides along so the inbox renders without a per-row
	// message fetch.
	AvatarMediaID      *uuid.UUID `json:"avatar_media_id,omitempty"`
	LastMessageAt      *time.Time `json:"last_message_at,omitempty"`
	LastMessagePreview string     `json:"last_message_preview,omitempty"`
	LastMessageSender  *uuid.UUID `json:"last_message_sender,omitempty"`
	HasUnread          bool       `json:"has_unread"`

	// Android chat completion pass: the VIEWER's per-conversation settings,
	// joined in one page query — additive, omitted when false, so every
	// earlier client and capture stays byte-compatible.
	IsPinned bool `json:"is_pinned,omitempty"`
	IsMuted  bool `json:"is_muted,omitempty"`
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
	pres               presence.Store
	rateLimiter        *ratelimit.Limiter
	producer           EventProducer
	log                *slog.Logger
	pollInterval       time.Duration
	userServiceURL     string
	identityUserURL    string
	graphServiceURL    string
	mediaServiceURL    string
	internalServiceKey string
	entitlementSecret  string
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

// ConversationPresence describes who's currently viewing or typing in
// a conversation. activeCount is always present; activeUsers is only
// populated for small conversations (≤100 members) per the production
// realtime architecture rules — large groups get count-only to avoid
// leaking a viewer list.
type ConversationPresence struct {
	ActiveCount int64    `json:"active_count"`
	ActiveUsers []string `json:"active_users,omitempty"`
	TypingUsers []string `json:"typing_users,omitempty"`
	IsBigGroup  bool     `json:"is_big_group"`
}

// GetConversationPresence returns who's actively viewing convID right
// now. Caller must already be a member. The user-list-vs-count decision
// is made server-side based on the group size cap so a client can't
// scrape big channels.
func (s *Service) GetConversationPresence(ctx context.Context, userID, convID uuid.UUID) (*ConversationPresence, error) {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a member of this conversation")
	}

	const smallGroupCap = 100
	members, err := s.convStore.GetMembers(ctx, convID)
	if err != nil {
		return nil, err
	}
	isBig := len(members) > smallGroupCap

	count, err := s.pres.ActiveCount(ctx, convID.String())
	if err != nil {
		return nil, err
	}
	out := &ConversationPresence{ActiveCount: count, IsBigGroup: isBig}
	if !isBig {
		users, err := s.pres.ActiveUsers(ctx, convID.String(), smallGroupCap)
		if err != nil {
			return nil, err
		}
		out.ActiveUsers = s.filterPresenceDisclosure(ctx, userID, users)
		typing, err := s.pres.TypingUsers(ctx, convID.String(), smallGroupCap)
		if err != nil {
			return nil, err
		}
		out.TypingUsers = s.filterPresenceDisclosure(ctx, userID, typing)
	}
	return out, nil
}

// filterPresenceDisclosure keeps only roster entries whose owner discloses
// online state to the requester (P0-2 correction): membership alone never
// justified handing out an identity roster, because who_can_see_online_status
// is a per-target setting. The check is the same graph-gated, 30s-cached,
// fail-closed decision the direct-presence endpoint uses; the requester
// always sees themself.
func (s *Service) filterPresenceDisclosure(ctx context.Context, requesterID uuid.UUID, ids []string) []string {
	if len(ids) == 0 {
		return ids
	}
	filtered := make([]string, 0, len(ids))
	for _, raw := range ids {
		id, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		if id == requesterID || s.discloseOnlineTo(ctx, requesterID, id) {
			filtered = append(filtered, raw)
		}
	}
	return filtered
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

	// The ACTOR's own chat pause blocks new conversations they initiate
	// (directive §3.2). The target's pause is enforced by the graph decision
	// below. Unknown own-policy fails closed for CREATION (sensitive action).
	if own := s.GetChatPolicy(ctx, userID); !own.Known || own.ChatPaused {
		return nil, ErrMessagingNotAllowed
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

	// Decline cooldown (directive §3.3): a recently declined/blocked/reported
	// request bars a re-request REGARDLESS of the idempotency key presented.
	// Checked outside the idempotency closure on purpose — a rotated key is
	// exactly the bypass this closes.
	if !allowed && asRequest {
		prev, err := s.groupStore().GetLatestRequestBetween(ctx, userID, otherID)
		if err != nil {
			return nil, err
		}
		if prev != nil && prev.RespondedAt != nil {
			switch prev.Status {
			case "ignored", "blocked", "reported":
				if time.Since(*prev.RespondedAt) < requestDeclineCooldown {
					return nil, ErrRequestCooldown
				}
			}
		}
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

// CreateDatingMatchConversation provisions the 1:1 conversation that
// backs a freshly formed dating match. Bypasses the usual
// checkMessagePermission DM gate — the match itself is the consent
// signal, so requiring mutual-circle membership would lock matched
// pairs out of chat. Idempotent on matchID via a partial unique index;
// safe to retry from the match-formation saga.
//
// Caller is dating-service (internal-key gated at the handler).
// P0-3 in dating/PRODUCTION_GAP_ANALYSIS.md.
func (s *Service) CreateDatingMatchConversation(ctx context.Context, userA, userB, matchID uuid.UUID) (*ConversationResponse, error) {
	if userA == userB {
		return nil, errors.New("dating-match conversation requires two distinct users")
	}
	if matchID == uuid.Nil {
		return nil, errors.New("match_id is required")
	}
	convID, _, err := s.convStore.CreateDatingMatchConversation(ctx, userA, userB, matchID)
	if err != nil {
		return nil, err
	}
	return s.getConversationResponse(ctx, convID)
}

// CloseDatingMatchConversation flips closed_at on the conversation
// keyed by matchID. Idempotent — a re-close preserves the original
// closed_at. Called by the dating-event consumer when
// match.closed / match.expired lands.
func (s *Service) CloseDatingMatchConversation(ctx context.Context, matchID uuid.UUID) error {
	if matchID == uuid.Nil {
		return errors.New("match_id is required")
	}
	return s.convStore.MarkConversationClosedByMatch(ctx, matchID)
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

	// One cursor query for the whole page — never per row.
	convIDs := make([]uuid.UUID, len(convs))
	for i, c := range convs {
		convIDs[i] = c.ID
	}
	cursors, err := s.groupStore().GetReadCursors(ctx, userID, convIDs)
	if err != nil {
		s.log.Warn("read cursor batch failed — unread flags degraded", "err", err)
		cursors = nil
	}
	// Pinned/muted flags for the page, one query (never per row). Failure
	// degrades the flags to defaults rather than failing the inbox.
	settings, err := s.extrasStore().GetSettingsForConversations(ctx, userID, convIDs)
	if err != nil {
		s.log.Warn("conversation settings batch failed — pin/mute flags degraded", "err", err)
		settings = nil
	}

	out := make([]ConversationResponse, 0, len(convs))
	for _, c := range convs {
		members, err := s.convStore.GetMembers(ctx, c.ID)
		if err != nil {
			return nil, nil, err
		}
		enrichedMembers := s.enrichMembers(ctx, members)
		hasUnread := false
		if c.LastMessageAt != nil {
			// A message the viewer sent themselves is never "unread".
			if c.LastMessageSender == nil || *c.LastMessageSender != userID {
				rc, ok := cursors[c.ID]
				hasUnread = !ok || rc.LastReadAt.Before(*c.LastMessageAt)
			}
		}
		out = append(out, ConversationResponse{
			ID:                 c.ID,
			Type:               c.Type,
			Title:              c.Title,
			CreatedBy:          c.CreatedBy,
			IsRequest:          c.IsRequest,
			Members:            enrichedMembers,
			CreatedAt:          c.CreatedAt,
			UpdatedAt:          c.UpdatedAt,
			AvatarMediaID:      c.AvatarMediaID,
			LastMessageAt:      c.LastMessageAt,
			LastMessagePreview: c.LastMessagePreview,
			LastMessageSender:  c.LastMessageSender,
			HasUnread:          hasUnread,
			IsPinned:           settings[c.ID].IsPinned,
			IsMuted:            settings[c.ID].IsMuted,
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

	// Blocker-2 final correction: even this legacy (currently unrouted)
	// method uses the conversation-locked writer, so a future re-wiring can
	// never reintroduce an unserialized membership add.
	if err := s.groupStore().AddMemberCapped(ctx, conversationID, targetUserID, "member", GroupMemberCap); err != nil {
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

// ManagedAddGroupMember is the narrow group-service reconciliation path.
// Authentication is enforced by the exact /internal route; group-service is
// authoritative for social-group membership, so chat must not re-evaluate a
// chat-admin role that can drift from it.
func (s *Service) ManagedAddGroupMember(ctx context.Context, conversationID, userID uuid.UUID) error {
	conversation, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conversation == nil || conversation.Type != "group" {
		return errors.New("managed group conversation not found")
	}
	// Blocker-2 final correction: the legacy AddMember took no conversation
	// lock, so a managed add could race a removal outside the generation
	// serialization every other membership write obeys. AddMemberCapped is
	// the conversation-locked writer (and managed groups obey the same
	// launch cap as every group).
	return s.groupStore().AddMemberCapped(ctx, conversationID, userID, "member", GroupMemberCap)
}

func (s *Service) ManagedRemoveGroupMember(ctx context.Context, conversationID, userID uuid.UUID) error {
	conversation, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conversation == nil || conversation.Type != "group" {
		return errors.New("managed group conversation not found")
	}
	// Final-verification P0-4: this path severed through the legacy store
	// and acknowledged success with no revocation, leaving the removed
	// member's room subscription and token live. Every sever now goes
	// through the one revocation protocol.
	return s.severMemberWithRevocation(ctx, conversationID, userID)
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

	// Blocker-2 final correction: even this legacy (currently unrouted)
	// method severs through the revocation protocol, so a future re-wiring
	// can never reintroduce an unrevoked sever.
	if err := s.severMemberWithRevocation(ctx, conversationID, targetUserID); err != nil {
		return err
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
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, ErrIdempotencyKeyRequired
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not a conversation member")
	}
	// Chat pause (directive §3.2, P0-1 correction): a paused SENDER sends
	// nothing; a paused direct RECIPIENT receives nothing new. Both read the
	// LOCAL policy projection — no HTTP on the hot path when the projection
	// is fresh. An UNKNOWN policy fails closed like a pause: the Postgres
	// projection (bounded by policyStaleGrace) carries availability through a
	// short identity outage, so "unknown" means the pause state genuinely
	// cannot be established, and sending anyway would override a pause we
	// cannot see.
	if own := s.GetChatPolicy(ctx, userID); !own.Known || own.ChatPaused {
		return nil, ErrMessagingNotAllowed
	}
	// P0-3 dating-match send gate: reject sends to a closed dating
	// conversation (match unmatched / expired / one side blocked /
	// paused / suspended). The closed_at flag is flipped by the
	// dating-event consumer when match.closed / match.expired land.
	// Membership already covers the block case via
	// conversation_members.left_at; the closed_at check is the
	// dating-side equivalent.
	meta, err := s.convStore.GetConversationMeta(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if meta != nil && meta.SourceApp == "dating" && meta.ClosedAt != nil {
		return nil, ErrMatchClosed
	}
	// Direct-recipient pause (P0-1 correction): look up the other member's
	// projected policy. Lookup errors propagate — swallowing them turned a
	// database hiccup into a delivery to a possibly-paused recipient — and an
	// unknown recipient policy fails closed for the same reason as above.
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if conv != nil && conv.Type == "direct" {
		members, err := s.convStore.GetMembers(ctx, conversationID)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			if m.UserID == userID {
				continue
			}
			if p := s.GetChatPolicy(ctx, m.UserID); !p.Known || p.ChatPaused {
				return nil, ErrMessagingNotAllowed
			}
		}
	}

	req := struct {
		UserID         uuid.UUID  `json:"user_id"`
		ConversationID uuid.UUID  `json:"conversation_id"`
		Type           string     `json:"type"`
		Text           string     `json:"text"`
		MediaID        *uuid.UUID `json:"media_id,omitempty"`
	}{UserID: userID, ConversationID: conversationID, Type: msgType, Text: text, MediaID: mediaID}
	requestHash, err := hashRequestPayload(req)
	if err != nil {
		return nil, err
	}
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
		members, err := s.convStore.GetMembers(ctx, conversationID)
		if err != nil {
			return nil, err
		}
		var requestReceiverID *uuid.UUID
		if firstRequestMessage {
			for _, member := range members {
				if member.UserID != userID {
					receiver := member.UserID
					requestReceiverID = &receiver
					break
				}
			}
		}
		sourceApp := "chat"
		var matchID *uuid.UUID
		if meta != nil && meta.SourceApp != "" {
			sourceApp = meta.SourceApp
			matchID = meta.MatchID
		}
		if mediaID != nil {
			// The reference is stable across retries but opaque outside this
			// service. media-service atomically validates and pins the asset
			// before any cross-store message mutation starts.
			referenceID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("atpost-chat-attachment:"+userID.String()+":"+idempotencyKey))
			if err := s.reserveChatAttachment(ctx, referenceID, userID, *mediaID); err != nil {
				return nil, err
			}
		}
		intent, err := s.deliveryStore().ReserveMessageDeliveryIntent(ctx, postgres.MessageDeliveryIntent{
			IdempotencyKey:    idempotencyKey,
			RequestHash:       requestHash,
			ConversationID:    conversationID,
			SenderID:          userID,
			MessageID:         msgID,
			Bucket:            bucket,
			MessageTS:         now,
			MessageType:       msgType,
			MessageText:       text,
			MediaID:           mediaID,
			MemberIDs:         activeMemberIDs(members),
			FirstRequest:      firstRequestMessage,
			RequestReceiverID: requestReceiverID,
			SourceApp:         sourceApp,
			MatchID:           matchID,
		})
		if err != nil {
			if errors.Is(err, postgres.ErrDeliveryIntentConflict) {
				return nil, ErrIdempotencyConflict
			}
			return nil, err
		}
		now, msgID, bucket = intent.MessageTS, intent.MessageID, intent.Bucket

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
		if err := s.completeMessageDelivery(ctx, intent); err != nil {
			l.Error("message delivery is pending durable repair", "err", err)
			return nil, err
		}

		// 5. Real-time delivery via Redis pub/sub to all members (best-effort).
		// Runs after the durable outbox handoff so a successful publish
		// always corresponds to a queued outbox event.
		//
		// Phase 1 §1: for dating_match conversations we tag the payload
		// with source_app + match_id so ws-gateway / clients can route to
		// the dating UI rather than the generic chat list.
		var sourceAppMeta string
		var matchIDMeta string
		if meta != nil && meta.SourceApp == "dating" {
			sourceAppMeta = meta.SourceApp
			if meta.MatchID != nil {
				matchIDMeta = meta.MatchID.String()
			}
		}
		go func() {
			pubCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			msgBody := map[string]interface{}{
				"conversation_id": conversationID,
				"message_id":      msgID,
				"sender_id":       userID,
				"type":            msgType,
				"text":            text,
				"media_id":        mediaID,
				"created_at":      now,
			}
			if sourceAppMeta != "" {
				msgBody["source_app"] = sourceAppMeta
			}
			if matchIDMeta != "" {
				msgBody["match_id"] = matchIDMeta
			}
			payload, _ := json.Marshal(map[string]interface{}{
				"type":    "message",
				"payload": msgBody,
			})
			// M2: per-member fan-out collapsed into a single Redis
			// pipeline. The previous loop issued one round-trip per
			// member, which for a 100-member group added 100×RTT to
			// the send (~100ms in the same DC, much worse cross-AZ).
			// Pipeline batches the PUBLISH commands and waits for one
			// flush. Pub/Sub fan-out semantics stay identical from
			// the ws-gateway's point of view — it subscribes per
			// chat:<user_id> channel as before.
			pipe := s.rdb.Pipeline()
			recipients := 0
			for _, m := range members {
				if m.UserID == userID {
					continue // Don't notify sender
				}
				pipe.Publish(pubCtx, fmt.Sprintf("chat:%s", m.UserID), payload)
				recipients++
			}
			// Conversation-room publish (scoped-rooms foundation, directive
			// §5.3): entitled gateway subscriptions receive the frame once
			// per conversation instead of once per member. Personal-channel
			// fan-out stays (it is the public delivery path today); clients
			// de-duplicate by message_id. One extra PUBLISH, harmless when
			// nothing is subscribed.
			pipe.Publish(pubCtx, fmt.Sprintf("convroom:%s", conversationID), payload)
			if recipients > 0 {
				if _, err := pipe.Exec(pubCtx); err != nil {
					l.Warn("failed to pipeline-publish to redis pubsub", "err", err, "recipients", recipients)
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

	// MP-LB-1 WRITE-AHEAD: the durable preview-repair obligation is committed
	// BEFORE the soft delete. The inbox preview is denormalized from the
	// newest message; if that is THIS message, its text keeps showing on
	// every member's inbox until repaired, and the repair can fail after the
	// delete succeeded (Scylla read, PostgreSQL write, crash, restart). With
	// the obligation on disk first, every one of those failures is resumed by
	// the repair worker. If the obligation itself cannot be recorded, the
	// message is NOT deleted — the client retries a delete that did nothing.
	if err := s.previewRepairStore().CreatePreviewRepairObligation(
		ctx, conversationID, messageID, bucket, msg.Ts,
	); err != nil {
		return fmt.Errorf("record preview repair obligation: %w", err)
	}

	if err := s.msgStore.SoftDeleteMessage(ctx, conversationID, bucket, ts, messageID); err != nil {
		// The obligation stands; the worker later finds the message still
		// live and retires it without touching the preview.
		return err
	}

	// Invalidate cache
	key := fmt.Sprintf("chat_messages:%s", conversationID)
	s.rdb.Del(ctx, key)

	// Inline repair attempt. On success the obligation is retired; on any
	// failure it stays durable and the worker finishes the job — the API
	// still reports success because the message IS deleted.
	obligation := postgres.PreviewRepairObligation{
		MessageID:      messageID,
		ConversationID: conversationID,
		Bucket:         bucket,
		DeletedTs:      msg.Ts,
		CreatedAt:      time.Now(),
	}
	if outcome, rerr := s.resolvePreviewRepair(ctx, obligation); outcome == previewRepairResolved {
		if cerr := s.previewRepairStore().CompletePreviewRepairObligation(ctx, messageID); cerr != nil {
			s.log.Warn("preview repair completion failed; worker will retire it",
				"err", cerr, "message_id", messageID)
		}
	} else if rerr != nil {
		s.log.Warn("inline preview repair deferred to worker",
			"err", rerr, "message_id", messageID, "conversation_id", conversationID)
	}

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
		// M2: pipeline the per-member PUBLISH commands (see message-
		// send path above for the rationale).
		pipe := s.rdb.Pipeline()
		recipients := 0
		for _, m := range members {
			if m.UserID == userID {
				continue
			}
			pipe.Publish(pubCtx, fmt.Sprintf("chat:%s", m.UserID), payload)
			recipients++
		}
		if recipients > 0 {
			if _, err := pipe.Exec(pubCtx); err != nil {
				s.log.Warn("failed to pipeline-publish reaction to redis", "err", err, "recipients", recipients)
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
		ID:                 conv.ID,
		Type:               conv.Type,
		Title:              conv.Title,
		CreatedBy:          conv.CreatedBy,
		IsRequest:          conv.IsRequest,
		Members:            enrichedMembers,
		CreatedAt:          conv.CreatedAt,
		UpdatedAt:          conv.UpdatedAt,
		AvatarMediaID:      conv.AvatarMediaID,
		LastMessageAt:      conv.LastMessageAt,
		LastMessagePreview: conv.LastMessagePreview,
		LastMessageSender:  conv.LastMessageSender,
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
	// Typing is an ACTOR-side disclosure (directive §3.2 setting 7): the
	// sender's own toggle gates it, a paused sender publishes nothing, and an
	// UNKNOWN policy fails closed — silence is always safe. Returns success
	// so a denied indicator never surfaces as a client error.
	if p := s.GetChatPolicy(ctx, userID); !p.Known || p.ChatPaused || !p.SendTypingIndicators {
		return nil
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

// MarkRead durably advances the reader's unread watermark, then broadcasts a
// read receipt ONLY when the reader's privacy settings permit disclosure.
//
// The cursor write is unconditional — unread state is the reader's OWN data
// and repairs push/inbox counts (CH-LB-4.6). The receipt broadcast is a
// DISCLOSURE and obeys who_can_see_read_receipts: no_one (or unknown policy)
// discloses nothing; everyone discloses; connections_only is disclosed only
// in DIRECT conversations after a graph see_read_receipts decision. Group
// conversations get no per-user receipt frames at launch — permitted senders
// read aggregate state, never a reader roster (directive §3.5).
func (s *Service) MarkRead(ctx context.Context, userID, conversationID uuid.UUID, messageID string) error {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a member of this conversation")
	}

	now := time.Now().UTC()
	if msgID, err := uuid.Parse(messageID); err == nil {
		if err := s.groupStore().UpsertReadCursor(ctx, conversationID, userID, msgID, now); err != nil {
			s.log.Warn("read cursor upsert failed", "err", err, "conversation_id", conversationID, "user_id", userID)
		}
	}

	policy := s.GetChatPolicy(ctx, userID)
	if !policy.Known || policy.ChatPaused || policy.ReadReceiptsVisibility == "no_one" {
		return nil
	}

	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil || conv == nil {
		return nil
	}
	members, _ := s.convStore.GetMembers(ctx, conversationID)

	if conv.Type != "direct" {
		// Launch groups: aggregate counts only, computed from read cursors —
		// no per-reader receipt frames.
		return nil
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"type": "read_receipt",
		"payload": map[string]interface{}{
			"conversation_id": conversationID.String(),
			"user_id":         userID.String(),
			"message_id":      messageID,
			"read_at":         now.Format(time.RFC3339Nano),
		},
	})
	for _, m := range members {
		if m.UserID == userID {
			continue
		}
		if policy.ReadReceiptsVisibility == "connections_only" &&
			!s.discloseReceiptTo(ctx, m.UserID, userID) {
			continue
		}
		s.rdb.Publish(ctx, fmt.Sprintf("chat:%s", m.UserID), payload)
	}
	return nil
}

// discloseReceiptTo asks graph whether VIEWER may see READER's receipts —
// the connections_only resolution. Cached in Redis for 30 s per pair;
// failure fails closed (no disclosure).
func (s *Service) discloseReceiptTo(ctx context.Context, viewerID, readerID uuid.UUID) bool {
	if s.rdb == nil {
		return s.checkVisibilityPermission(ctx, viewerID, readerID, "see_read_receipts")
	}
	cacheKey := "receiptvis:" + viewerID.String() + ":" + readerID.String()
	if cached, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil {
		return cached == "1"
	}
	allowed := s.checkVisibilityPermission(ctx, viewerID, readerID, "see_read_receipts")
	value := "0"
	if allowed {
		value = "1"
	}
	s.rdb.Set(ctx, cacheKey, value, 30*time.Second)
	return allowed
}

// checkVisibilityPermission resolves one disclosure action via graph.
// ANY failure returns false — disclosure fails closed.
func (s *Service) checkVisibilityPermission(ctx context.Context, actorID, targetID uuid.UUID, action string) bool {
	if s.graphServiceURL == "" {
		return false
	}
	url := fmt.Sprintf("%s/v1/permissions/check?target_user_id=%s&actions=%s", s.graphServiceURL, targetID, action)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-User-Id", actorID.String())
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	var envelope struct {
		Data struct {
			Decisions map[string]struct {
				Allowed bool `json:"allowed"`
			} `json:"decisions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	return envelope.Data.Decisions[action].Allowed
}

// GetPresence checks which of the given user IDs are currently online,
// GATED per target by their who_can_see_online_status (directive §3.5).
//
// The requester is the actor; each target's own privacy decides disclosure.
// A denied or unknown decision reports OFFLINE — indistinguishable from a
// genuinely offline user, which is the point: privacy denial must not leak.
func (s *Service) GetPresence(ctx context.Context, requesterID uuid.UUID, userIDs []uuid.UUID) (map[string]bool, error) {
	if len(userIDs) == 0 {
		return map[string]bool{}, nil
	}
	if len(userIDs) > 50 {
		userIDs = userIDs[:50]
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
		online := results[i] != nil
		if online && id != requesterID {
			online = s.discloseOnlineTo(ctx, requesterID, id)
		}
		presence[id.String()] = online
	}
	return presence, nil
}

// discloseOnlineTo mirrors discloseReceiptTo for online-status visibility.
func (s *Service) discloseOnlineTo(ctx context.Context, viewerID, targetID uuid.UUID) bool {
	if s.rdb == nil {
		return s.checkVisibilityPermission(ctx, viewerID, targetID, "see_online_status")
	}
	cacheKey := "onlinevis:" + viewerID.String() + ":" + targetID.String()
	if cached, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil {
		return cached == "1"
	}
	allowed := s.checkVisibilityPermission(ctx, viewerID, targetID, "see_online_status")
	value := "0"
	if allowed {
		value = "1"
	}
	s.rdb.Set(ctx, cacheKey, value, 30*time.Second)
	return allowed
}
