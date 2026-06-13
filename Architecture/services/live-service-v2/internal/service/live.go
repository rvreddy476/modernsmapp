// Package service is the business logic for live-service-v2 (LiveKit).
//
// The flow:
//
//	1. CreateStream — DB row in 'scheduled', LiveKit room name reserved.
//	2. StartStream  — LiveKit room created, Egress→S3 started, status
//	                  flipped to 'live', live.stream.started published,
//	                  publisher token returned to the broadcaster.
//	3. IssueViewerToken — visibility gate (public / followers / paid),
//	                  then a subscriber-only LiveKit token.
//	4. EndStream    — Egress stopped, status flipped to 'ended',
//	                  viewer_peak materialised, live.stream.ended fired.
//	5. OnEgressFinished — webhook updates recording_url, fires
//	                  live.stream.vod_ready.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/atpost/live-service-v2/internal/events"
	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// Sentinel errors so HTTP handlers can map to the right status code.
var (
	ErrInvalidVisibility = errors.New("invalid visibility")
	ErrInvalidTitle      = errors.New("title is required")
	ErrNotCreator        = errors.New("only the creator may perform this action")
	ErrNotFollower       = errors.New("creator restricts this stream to followers")
	ErrPaidNotSupported  = errors.New("paid streams not yet supported")
	ErrStreamNotFound    = errors.New("live stream not found")

	// Chat moderation (Phase B) sentinels.
	ErrChatMuted       = errors.New("forbidden: you have been muted in this stream")
	ErrChatBlockedWord = errors.New("invalid: message contains a blocked word")
	ErrMessageNotFound = errors.New("chat message not found")
	ErrInvalidWord     = errors.New("invalid: word is required (1-100 chars)")
)

const (
	visibilityPublic    = "public"
	visibilityFollowers = "followers"
	visibilityPaid      = "paid"

	publisherTokenTTL = 12 * time.Hour
	viewerTokenTTL    = 4 * time.Hour
	followCacheTTL    = 60 * time.Second

	recordingObjectKeyPrefix = "recordings/"
)

// Store is the storage surface live-service-v2 needs. The concrete
// implementation is *postgres.Store; the interface exists so unit
// tests can plug in a fake without spinning up Postgres.
type Store interface {
	CreateStream(ctx context.Context, p postgres.CreateStreamParams) (*postgres.LiveStream, error)
	GetByID(ctx context.Context, id uuid.UUID) (*postgres.LiveStream, error)
	MarkLive(ctx context.Context, id uuid.UUID, egressID string) (*postgres.LiveStream, error)
	MarkEnded(ctx context.Context, id uuid.UUID, peakViewers int) (*postgres.LiveStream, error)
	SetRecording(ctx context.Context, id uuid.UUID, url string, durationSec int) (*postgres.LiveStream, error)
	ListLive(ctx context.Context, p postgres.ListLiveParams) ([]*postgres.LiveStream, error)
	RecordViewerEvent(ctx context.Context, streamID, userID uuid.UUID, eventType string) error

	InsertChatMessage(ctx context.Context, streamID, userID uuid.UUID, text string) (*postgres.ChatMessage, error)
	ListRecentChatMessages(ctx context.Context, streamID uuid.UUID, limit int) ([]*postgres.ChatMessage, error)

	// Phase B moderation.
	MuteUser(ctx context.Context, streamID, userID, mutedBy uuid.UUID) error
	UnmuteUser(ctx context.Context, streamID, userID uuid.UUID) error
	IsUserMuted(ctx context.Context, streamID, userID uuid.UUID) (bool, error)
	ListMutedUsers(ctx context.Context, streamID uuid.UUID) ([]uuid.UUID, error)
	AddWordFilter(ctx context.Context, streamID uuid.UUID, word string, addedBy uuid.UUID) error
	RemoveWordFilter(ctx context.Context, streamID uuid.UUID, word string) error
	ListWordFilters(ctx context.Context, streamID uuid.UUID) ([]string, error)
	MatchesWordFilter(ctx context.Context, streamID uuid.UUID, text string) (bool, error)
	PinMessage(ctx context.Context, streamID, messageID uuid.UUID) error
	UnpinMessage(ctx context.Context, streamID, messageID uuid.UUID) error
	GetPinnedMessage(ctx context.Context, streamID uuid.UUID) (*postgres.ChatMessage, error)
}

// Service is the live-service-v2 business layer.
type Service struct {
	store    Store
	livekit  livekit.Client
	graph    GraphClient
	producer *events.Producer
	redis    *redis.Client

	// Public base URL we expose recordings at (e.g. https://media.cdn/live-recordings).
	// If empty we fall back to the S3 endpoint + bucket path.
	recordingPublicBaseURL string
	s3Bucket               string
	s3Endpoint             string
}

type Config struct {
	RecordingPublicBaseURL string
	S3Bucket               string
	S3Endpoint             string
}

func New(store Store, lk livekit.Client, graph GraphClient, producer *events.Producer, rdb *redis.Client, cfg Config) *Service {
	return &Service{
		store:                  store,
		livekit:                lk,
		graph:                  graph,
		producer:               producer,
		redis:                  rdb,
		recordingPublicBaseURL: cfg.RecordingPublicBaseURL,
		s3Bucket:               cfg.S3Bucket,
		s3Endpoint:             cfg.S3Endpoint,
	}
}

// CreateStreamParams is the input to CreateStream.
type CreateStreamParams struct {
	Title        string
	Description  string
	Visibility   string
	CoverMediaID *uuid.UUID
	ScheduledAt  *time.Time
}

// CreateStream inserts a scheduled row and reserves a LiveKit room name.
// The room itself is created lazily in StartStream so we don't allocate
// SFU capacity for a stream that may never go live.
func (s *Service) CreateStream(ctx context.Context, creatorID uuid.UUID, p CreateStreamParams) (*postgres.LiveStream, error) {
	if strings.TrimSpace(p.Title) == "" {
		return nil, ErrInvalidTitle
	}
	vis := normalizeVisibility(p.Visibility)
	if vis == "" {
		return nil, ErrInvalidVisibility
	}
	streamID := uuid.New()
	room := "stream_" + streamID.String()
	return s.store.CreateStream(ctx, postgres.CreateStreamParams{
		CreatorUserID: creatorID,
		LiveKitRoom:   room,
		Title:         strings.TrimSpace(p.Title),
		Description:   strings.TrimSpace(p.Description),
		CoverMediaID:  p.CoverMediaID,
		Visibility:    vis,
		ScheduledAt:   p.ScheduledAt,
	})
}

// StartStreamResult is what we hand back to the broadcaster client. The
// browser uses these to open a LiveKit publisher connection.
type StartStreamResult struct {
	Stream         *postgres.LiveStream `json:"stream"`
	PublisherToken string               `json:"publisher_token"`
	Room           string               `json:"room"`
	ServerURL      string               `json:"server_url"`
}

func (s *Service) StartStream(ctx context.Context, streamID, creatorID uuid.UUID) (*StartStreamResult, error) {
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if st.CreatorUserID != creatorID {
		return nil, ErrNotCreator
	}
	// LiveKit room — idempotent on duplicate name.
	if err := s.livekit.CreateRoom(ctx, st.LiveKitRoom); err != nil {
		return nil, fmt.Errorf("livekit create room: %w", err)
	}
	// Egress to S3. Failure here is logged but does NOT block going
	// live — losing the VOD is better than losing the broadcast.
	objectKey := recordingObjectKeyPrefix + streamID.String() + ".mp4"
	egressID, egErr := s.livekit.StartEgressToS3(ctx, st.LiveKitRoom, objectKey)
	if egErr != nil {
		// We deliberately don't return — the stream goes live without
		// recording. The handler logs the warning.
		egressID = ""
	}
	updated, err := s.store.MarkLive(ctx, streamID, egressID)
	if err != nil {
		return nil, err
	}
	token, err := s.livekit.IssuePublisherToken(ctx, updated.LiveKitRoom, creatorID.String(), publisherTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("livekit publisher token: %w", err)
	}
	startedAt := time.Now()
	if updated.StartedAt != nil {
		startedAt = *updated.StartedAt
	}
	_ = s.producer.PublishStreamStarted(ctx, updated.ID, creatorID, updated.Title, updated.Visibility, startedAt)
	return &StartStreamResult{
		Stream:         updated,
		PublisherToken: token,
		Room:           updated.LiveKitRoom,
		ServerURL:      s.livekit.ServerURL(),
	}, egErr
}

// EndStream stops the SFU egress, materialises the Redis hot viewer
// counter into viewer_peak, and fires live.stream.ended.
func (s *Service) EndStream(ctx context.Context, streamID, creatorID uuid.UUID) error {
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return mapStoreErr(err)
	}
	if st.CreatorUserID != creatorID {
		return ErrNotCreator
	}
	// Pull the peak from Redis (best-effort).
	peak := s.readViewerPeak(ctx, streamID)
	if st.EgressID != nil && *st.EgressID != "" {
		if err := s.livekit.StopEgress(ctx, *st.EgressID); err != nil {
			// Log but don't fail — webhook will reconcile.
			_ = err
		}
	}
	updated, err := s.store.MarkEnded(ctx, streamID, peak)
	if err != nil {
		return err
	}
	endedAt := time.Now()
	if updated.EndedAt != nil {
		endedAt = *updated.EndedAt
	}
	_ = s.producer.PublishStreamEnded(ctx, updated.ID, creatorID, endedAt, updated.ViewerPeak)
	// Clear the hot counter so a future creator can reuse the slot.
	if s.redis != nil {
		s.redis.Del(ctx, viewerCounterKey(streamID))
	}
	return nil
}

// IssueViewerTokenResult is returned to viewers joining a stream.
type IssueViewerTokenResult struct {
	Token     string `json:"token"`
	Room      string `json:"room"`
	ServerURL string `json:"server_url"`
}

// IssueViewerToken runs the visibility gate then mints a subscriber-only
// LiveKit token. Followers checks are cached for 60s in Redis to keep
// token issuance cheap on big streams.
func (s *Service) IssueViewerToken(ctx context.Context, streamID, viewerID uuid.UUID) (*IssueViewerTokenResult, error) {
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if err := s.authorizeViewer(ctx, st, viewerID); err != nil {
		return nil, err
	}
	token, err := s.livekit.IssueViewerToken(ctx, st.LiveKitRoom, viewerID.String(), viewerTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("livekit viewer token: %w", err)
	}
	return &IssueViewerTokenResult{
		Token:     token,
		Room:      st.LiveKitRoom,
		ServerURL: s.livekit.ServerURL(),
	}, nil
}

// ListLiveNow returns currently-live streams visible to viewerID.
// Followers-only streams are filtered to creators the viewer follows.
// Paid streams are skipped (not supported in v2).
type ListLiveResult struct {
	Streams    []*postgres.LiveStream `json:"streams"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

func (s *Service) ListLiveNow(ctx context.Context, viewerID uuid.UUID, limit int, cursor string) (*ListLiveResult, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	startedBefore, idBefore, err := parseCursor(cursor)
	if err != nil {
		return nil, err
	}
	// Over-fetch a bit to compensate for filtering below.
	streams, err := s.store.ListLive(ctx, postgres.ListLiveParams{
		Limit:         limit * 2,
		StartedBefore: startedBefore,
		IDBefore:      idBefore,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*postgres.LiveStream, 0, limit)
	for _, st := range streams {
		if len(out) == limit {
			break
		}
		if err := s.authorizeViewer(ctx, st, viewerID); err != nil {
			continue
		}
		out = append(out, st)
	}
	res := &ListLiveResult{Streams: out}
	if len(out) == limit && out[len(out)-1].StartedAt != nil {
		last := out[len(out)-1]
		res.NextCursor = encodeCursor(*last.StartedAt, last.ID)
	}
	return res, nil
}

// GetStream is the single-read variant used for both the live player and
// VOD playback. Same visibility gate as the list endpoint.
func (s *Service) GetStream(ctx context.Context, streamID, viewerID uuid.UUID) (*postgres.LiveStream, error) {
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if err := s.authorizeViewer(ctx, st, viewerID); err != nil {
		return nil, err
	}
	return st, nil
}

// OnEgressFinished is the LiveKit Egress webhook handler. recordingURL
// can be empty — if so we derive it from the object key.
func (s *Service) OnEgressFinished(ctx context.Context, streamID uuid.UUID, recordingURL string, durationSec int) error {
	if recordingURL == "" {
		recordingURL = s.resolveRecordingURL(streamID)
	}
	st, err := s.store.SetRecording(ctx, streamID, recordingURL, durationSec)
	if err != nil {
		return mapStoreErr(err)
	}
	_ = s.producer.PublishVODReady(ctx, st.ID, st.CreatorUserID, recordingURL, durationSec)
	return nil
}

// RecordParticipantEvent updates the Redis hot counter and persists a
// viewer event for analytics. Called from the LiveKit participant
// join/leave webhook.
func (s *Service) RecordParticipantEvent(ctx context.Context, streamID, userID uuid.UUID, eventType string) error {
	if eventType != "join" && eventType != "leave" {
		return fmt.Errorf("invalid event_type: %s", eventType)
	}
	// Best-effort analytics row.
	_ = s.store.RecordViewerEvent(ctx, streamID, userID, eventType)
	if s.redis == nil {
		return nil
	}
	key := viewerCounterKey(streamID)
	switch eventType {
	case "join":
		s.redis.Incr(ctx, key)
		s.redis.Expire(ctx, key, 12*time.Hour)
	case "leave":
		// Decrement but clamp at zero.
		s.redis.Eval(ctx, `
            local v = tonumber(redis.call('GET', KEYS[1]) or '0')
            if v > 0 then
                return redis.call('DECR', KEYS[1])
            end
            return 0`, []string{key})
	}
	return nil
}

// authorizeViewer applies the public/followers/paid gate. viewerID may
// be uuid.Nil for unauthenticated readers (only public passes then).
func (s *Service) authorizeViewer(ctx context.Context, st *postgres.LiveStream, viewerID uuid.UUID) error {
	switch st.Visibility {
	case visibilityPublic:
		return nil
	case visibilityFollowers:
		// Creators always see their own stream.
		if viewerID == st.CreatorUserID {
			return nil
		}
		if viewerID == uuid.Nil {
			return ErrNotFollower
		}
		follows, err := s.checkFollowCached(ctx, viewerID, st.CreatorUserID)
		if err != nil || !follows {
			return ErrNotFollower
		}
		return nil
	case visibilityPaid:
		return ErrPaidNotSupported
	default:
		return ErrInvalidVisibility
	}
}

// checkFollowCached wraps the graph-service call with a Redis 60-second
// cache keyed by (viewer, creator).
func (s *Service) checkFollowCached(ctx context.Context, viewerID, creatorID uuid.UUID) (bool, error) {
	if s.graph == nil {
		return false, nil
	}
	if s.redis != nil {
		cacheKey := fmt.Sprintf("live_follow_check:%s:%s", viewerID, creatorID)
		if v, err := s.redis.Get(ctx, cacheKey).Result(); err == nil {
			return v == "1", nil
		}
		follows, err := s.graph.IsFollowing(ctx, viewerID, creatorID)
		if err == nil {
			val := "0"
			if follows {
				val = "1"
			}
			s.redis.Set(ctx, cacheKey, val, followCacheTTL)
		}
		return follows, err
	}
	return s.graph.IsFollowing(ctx, viewerID, creatorID)
}

func (s *Service) readViewerPeak(ctx context.Context, streamID uuid.UUID) int {
	if s.redis == nil {
		return 0
	}
	v, err := s.redis.Get(ctx, viewerCounterKey(streamID)).Result()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(v)
	if n < 0 {
		return 0
	}
	return n
}

func (s *Service) resolveRecordingURL(streamID uuid.UUID) string {
	key := recordingObjectKeyPrefix + streamID.String() + ".mp4"
	if s.recordingPublicBaseURL != "" {
		return strings.TrimRight(s.recordingPublicBaseURL, "/") + "/" + key
	}
	if s.s3Endpoint != "" && s.s3Bucket != "" {
		return strings.TrimRight(s.s3Endpoint, "/") + "/" + s.s3Bucket + "/" + key
	}
	return key
}

// --- helpers ---

func viewerCounterKey(streamID uuid.UUID) string {
	return "live:viewers:" + streamID.String()
}

func normalizeVisibility(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", visibilityPublic:
		return visibilityPublic
	case visibilityFollowers:
		return visibilityFollowers
	case visibilityPaid:
		return visibilityPaid
	default:
		return ""
	}
}

func mapStoreErr(err error) error {
	if errors.Is(err, postgres.ErrNotFound) {
		return ErrStreamNotFound
	}
	return err
}

// encodeCursor / parseCursor implement the platform's standard keyset
// cursor: "<unix_micros>:<uuid>". Empty/invalid returns no error to
// keep the discover endpoint forgiving for clients.
func encodeCursor(t time.Time, id uuid.UUID) string {
	return strconv.FormatInt(t.UnixMicro(), 10) + ":" + id.String()
}

func parseCursor(cursor string) (*time.Time, *uuid.UUID, error) {
	if cursor == "" {
		return nil, nil, nil
	}
	parts := strings.SplitN(cursor, ":", 2)
	if len(parts) != 2 {
		return nil, nil, nil
	}
	micros, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, nil, nil
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return nil, nil, nil
	}
	t := time.UnixMicro(micros)
	return &t, &id, nil
}

// --- Chat overlay (Phase A) ---
//
// Live chat is a thin REST surface backed by Redis pub/sub for fan-out.
// Clients SUBSCRIBE to the pub/sub channel via the chat-service
// ws-gateway's dynamic subscribe_live_stream message; this service
// persists to live_chat_messages for replay-on-load.

// chatPubSubChannel returns the Redis pub/sub channel the ws-gateway
// fans out on. Matches the ws-gateway subscribe_live_stream handler's
// channel format (`live:stream:%s`).
func chatPubSubChannel(streamID uuid.UUID) string {
	return fmt.Sprintf("live:stream:%s", streamID.String())
}

// chatRateLimitKey returns the per-user-per-stream rate limit Redis
// key. Window is 60s; max 20 messages.
func chatRateLimitKey(streamID, userID uuid.UUID) string {
	return fmt.Sprintf("live_chat_rl:%s:%s", streamID.String(), userID.String())
}

const (
	chatRateLimitMax    = 20
	chatRateLimitWindow = 60 * time.Second
)

// SendChat persists a chat message + fans out via Redis pub/sub. The
// caller must already be a verified viewer (the handler enforces the
// viewer-token / membership check before reaching us).
//
// Returns the stored row so the broadcaster client can echo it
// immediately. The pub/sub message goes to every other connected
// viewer via the ws-gateway's `subscribe_live_stream` channel.
//
// Rate-limited 20/60s/user. Fail-CLOSED on Redis error for the rate
// check — easy to overload chat with a hostile client otherwise.
func (s *Service) SendChat(ctx context.Context, streamID, userID uuid.UUID, text string) (*postgres.ChatMessage, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("invalid: message text is required")
	}
	if utf8.RuneCountInString(text) > 500 {
		return nil, fmt.Errorf("invalid: message exceeds 500 chars")
	}
	// Stream must exist + still be live to accept chat. End-of-stream
	// chat goes to /v1/livestream/streams/:id/chat which falls back
	// to the replay buffer for VOD viewers.
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if st.Status != "live" {
		return nil, fmt.Errorf("invalid: stream is not live")
	}

	// Moderation gates run BEFORE the rate-limit + persist so a muted
	// user / blocked word does not eat into the per-user budget and we
	// never write a row that will be hidden anyway.
	if s.store != nil {
		muted, err := s.store.IsUserMuted(ctx, streamID, userID)
		if err != nil {
			return nil, fmt.Errorf("check mute: %w", err)
		}
		if muted {
			return nil, ErrChatMuted
		}
		blocked, err := s.store.MatchesWordFilter(ctx, streamID, text)
		if err != nil {
			return nil, fmt.Errorf("check word filter: %w", err)
		}
		if blocked {
			return nil, ErrChatBlockedWord
		}
	}

	// Rate limit. Redis sliding-window INCR+EXPIRE pattern; fail-CLOSED.
	if s.redis != nil {
		key := chatRateLimitKey(streamID, userID)
		pipe := s.redis.Pipeline()
		incr := pipe.Incr(ctx, key)
		pipe.Expire(ctx, key, chatRateLimitWindow)
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, fmt.Errorf("rate-limit check unavailable")
		}
		if incr.Val() > chatRateLimitMax {
			return nil, fmt.Errorf("rate_limited: too many chat messages; slow down")
		}
	}

	msg, err := s.store.InsertChatMessage(ctx, streamID, userID, text)
	if err != nil {
		return nil, err
	}
	// Fan out via Redis pub/sub. Payload shape matches the
	// ws-gateway's pass-through format so clients receive the row
	// verbatim under the `live_chat_message` type tag.
	if s.redis != nil {
		payload, _ := json.Marshal(map[string]any{
			"type": "live_chat_message",
			"payload": map[string]any{
				"id":         msg.ID.String(),
				"stream_id":  msg.StreamID.String(),
				"user_id":    msg.UserID.String(),
				"text":       msg.Text,
				"created_at": msg.CreatedAt,
			},
		})
		_ = s.redis.Publish(ctx, chatPubSubChannel(streamID), string(payload)).Err()
	}
	return msg, nil
}

// ListChat returns the most-recent `limit` messages (default 50,
// max 200). Caller must be a verified viewer of the stream;
// enforcement at the handler. Public streams skip the viewer check.
func (s *Service) ListChat(ctx context.Context, streamID uuid.UUID, limit int) ([]*postgres.ChatMessage, error) {
	if _, err := s.store.GetByID(ctx, streamID); err != nil {
		return nil, mapStoreErr(err)
	}
	return s.store.ListRecentChatMessages(ctx, streamID, limit)
}

// --- Chat moderation (Phase B) ---
//
// All host-only operations (mute, unmute, word filter add/remove, pin,
// unpin) share the same shape: load stream, verify hostID is the
// creator, mutate via the store, and publish a Redis pub/sub event so
// connected viewers can react in real time. The pub/sub channel is
// the same `live:stream:{id}` the chat overlay already uses; clients
// switch on the `type` tag.

// publishModerationEvent best-effort publishes a typed event on the
// chat pub/sub channel. Failure is logged at debug level by the
// caller; moderation must still apply if Redis is down.
func (s *Service) publishModerationEvent(ctx context.Context, streamID uuid.UUID, evtType string, payload map[string]any) {
	if s.redis == nil {
		return
	}
	body, err := json.Marshal(map[string]any{
		"type":    evtType,
		"payload": payload,
	})
	if err != nil {
		return
	}
	_ = s.redis.Publish(ctx, chatPubSubChannel(streamID), string(body)).Err()
}

// requireCreator loads the stream and verifies hostID owns it.
func (s *Service) requireCreator(ctx context.Context, streamID, hostID uuid.UUID) (*postgres.LiveStream, error) {
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if st.CreatorUserID != hostID {
		return nil, ErrNotCreator
	}
	return st, nil
}

// Mute records a per-stream mute for targetUserID. Emits
// `live:chat:mute` so clients can grey-out the muted user's prior
// messages immediately. Idempotent (UPSERT in the store).
func (s *Service) Mute(ctx context.Context, streamID, hostID, targetUserID uuid.UUID) error {
	if _, err := s.requireCreator(ctx, streamID, hostID); err != nil {
		return err
	}
	if err := s.store.MuteUser(ctx, streamID, targetUserID, hostID); err != nil {
		return err
	}
	s.publishModerationEvent(ctx, streamID, "live:chat:mute", map[string]any{
		"stream_id": streamID.String(),
		"user_id":   targetUserID.String(),
		"muted_by":  hostID.String(),
		"muted_at":  time.Now(),
	})
	return nil
}

// Unmute clears a mute. Emits `live:chat:unmute`.
func (s *Service) Unmute(ctx context.Context, streamID, hostID, targetUserID uuid.UUID) error {
	if _, err := s.requireCreator(ctx, streamID, hostID); err != nil {
		return err
	}
	if err := s.store.UnmuteUser(ctx, streamID, targetUserID); err != nil {
		return err
	}
	s.publishModerationEvent(ctx, streamID, "live:chat:unmute", map[string]any{
		"stream_id":  streamID.String(),
		"user_id":    targetUserID.String(),
		"unmuted_by": hostID.String(),
	})
	return nil
}

// ListMutedUsers returns the user IDs currently muted on the stream.
// Caller is expected to be the creator — we still enforce it here so
// the moderation surface is uniformly creator-gated.
func (s *Service) ListMutedUsers(ctx context.Context, streamID, hostID uuid.UUID) ([]uuid.UUID, error) {
	if _, err := s.requireCreator(ctx, streamID, hostID); err != nil {
		return nil, err
	}
	return s.store.ListMutedUsers(ctx, streamID)
}

// AddWordFilter registers a substring filter word (lowercased,
// trim'd). Emits `live:chat:word_filter_added`.
func (s *Service) AddWordFilter(ctx context.Context, streamID, hostID uuid.UUID, word string) error {
	if _, err := s.requireCreator(ctx, streamID, hostID); err != nil {
		return err
	}
	w := strings.ToLower(strings.TrimSpace(word))
	if w == "" || len(w) > 100 {
		return ErrInvalidWord
	}
	if err := s.store.AddWordFilter(ctx, streamID, w, hostID); err != nil {
		return err
	}
	s.publishModerationEvent(ctx, streamID, "live:chat:word_filter_added", map[string]any{
		"stream_id": streamID.String(),
		"word":      w,
		"added_by":  hostID.String(),
	})
	return nil
}

// RemoveWordFilter deletes a filter word. Emits
// `live:chat:word_filter_removed`.
func (s *Service) RemoveWordFilter(ctx context.Context, streamID, hostID uuid.UUID, word string) error {
	if _, err := s.requireCreator(ctx, streamID, hostID); err != nil {
		return err
	}
	w := strings.ToLower(strings.TrimSpace(word))
	if w == "" {
		return ErrInvalidWord
	}
	if err := s.store.RemoveWordFilter(ctx, streamID, w); err != nil {
		return err
	}
	s.publishModerationEvent(ctx, streamID, "live:chat:word_filter_removed", map[string]any{
		"stream_id": streamID.String(),
		"word":      w,
	})
	return nil
}

// ListWordFilters returns the configured filter words for a stream.
// Creator-only.
func (s *Service) ListWordFilters(ctx context.Context, streamID, hostID uuid.UUID) ([]string, error) {
	if _, err := s.requireCreator(ctx, streamID, hostID); err != nil {
		return nil, err
	}
	return s.store.ListWordFilters(ctx, streamID)
}

// PinMessage replaces any prior pin for the stream with messageID and
// emits `live:chat:pin` carrying the freshly-pinned message payload.
func (s *Service) PinMessage(ctx context.Context, streamID, hostID, messageID uuid.UUID) error {
	if _, err := s.requireCreator(ctx, streamID, hostID); err != nil {
		return err
	}
	if err := s.store.PinMessage(ctx, streamID, messageID); err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return ErrMessageNotFound
		}
		return err
	}
	// Re-fetch the pinned row so the broadcast payload is the full
	// message (text + user) — clients show it as a banner without an
	// extra round-trip.
	pinned, _ := s.store.GetPinnedMessage(ctx, streamID)
	if pinned != nil {
		s.publishModerationEvent(ctx, streamID, "live:chat:pin", map[string]any{
			"stream_id":  pinned.StreamID.String(),
			"message_id": pinned.ID.String(),
			"user_id":    pinned.UserID.String(),
			"text":       pinned.Text,
			"pinned_at":  pinned.PinnedAt,
			"pinned_by":  hostID.String(),
		})
	}
	return nil
}

// UnpinMessage clears the pin on a specific message. Emits
// `live:chat:unpin`.
func (s *Service) UnpinMessage(ctx context.Context, streamID, hostID, messageID uuid.UUID) error {
	if _, err := s.requireCreator(ctx, streamID, hostID); err != nil {
		return err
	}
	if err := s.store.UnpinMessage(ctx, streamID, messageID); err != nil {
		return err
	}
	s.publishModerationEvent(ctx, streamID, "live:chat:unpin", map[string]any{
		"stream_id":  streamID.String(),
		"message_id": messageID.String(),
		"unpinned_by": hostID.String(),
	})
	return nil
}

// GetPinnedMessage returns the current pin (or nil) without a
// creator check — viewers need to see the pinned banner too.
func (s *Service) GetPinnedMessage(ctx context.Context, streamID uuid.UUID) (*postgres.ChatMessage, error) {
	if _, err := s.store.GetByID(ctx, streamID); err != nil {
		return nil, mapStoreErr(err)
	}
	return s.store.GetPinnedMessage(ctx, streamID)
}
