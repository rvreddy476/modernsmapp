// Package service is the business logic for live-service-v2 (LiveKit).
//
// The flow (truthful lifecycle, 1 Oct 2026 — see lifecycle.go):
//
//  1. CreateStream — DB row in 'scheduled', LiveKit room name reserved.
//     Pilot allowlist and platform live bans gate it (fail closed).
//  2. StartStream  — LiveKit room created, status 'starting', publisher
//     token returned. Nothing says 'live' yet.
//  3. LiveKit webhook track_published by the HOST identity -> 'live'
//     (live.stream.started through the outbox; egress starts). Host left /
//     unpublished -> 'reconnecting'; back -> 'live'.
//  4. EndStream / room_finished / admin stop / the sweeper's timeouts ->
//     'ended' (with ended_reason) or 'failed' (no media).
//  5. IssueViewerToken — visibility, blocks and bans, then a
//     subscriber-only LiveKit token while the stream is on air.
//  6. egress_ended webhook — recording_url, live.stream.vod_ready (outbox).
package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

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
	// ErrPaidVisibility refuses creating a paid stream: no viewer passes the
	// paid gate yet (canSee), so a new paid stream would be dead on arrival.
	ErrPaidVisibility = errors.New("invalid: paid visibility is not available yet")
	ErrStreamNotFound = errors.New("live stream not found")

	// Chat moderation (Phase B) sentinels.
	ErrChatMuted       = errors.New("forbidden: you have been muted in this stream")
	ErrChatBlockedWord = errors.New("invalid: message contains a blocked word")
	ErrMessageNotFound = errors.New("chat message not found")
	ErrInvalidWord     = errors.New("invalid: word is required (1-100 chars)")

	// Launch safety (1 Oct 2026).
	ErrLiveNotEnabled       = errors.New("going live is in a closed pilot")
	ErrLiveBanned           = errors.New("you may not go live or chat on live streams")
	ErrBannedFromStream     = errors.New("you are banned from this stream")
	ErrViewerBlocked        = errors.New("live stream not found") // a block hides the stream
	ErrAuthorityUnavailable = errors.New("could not verify access right now")
	ErrStateConflict        = errors.New("the stream is not in a state that allows this")
	ErrStreamNotLive        = errors.New("the stream is not on air")
	ErrNotModerator         = errors.New("only the host or a stream moderator may do this")
	ErrTooManyModerators    = errors.New("a stream may have at most 5 moderators")
	ErrInvalidTarget        = errors.New("invalid: that user cannot be the target of this action")
	ErrInvalidReportReason  = errors.New("invalid: reason must be spam, harassment, hate, nudity, violence, scam or other")
	ErrInvalidNote          = errors.New("invalid: note exceeds 500 characters")
	ErrAlreadyReported      = errors.New("you already reported this")
	ErrReportRateLimited    = errors.New("rate_limited: too many reports; try again later")
	ErrReportNotFound       = errors.New("report not found")
	ErrReportResolved       = errors.New("report already resolved")
	ErrReasonRequired       = errors.New("invalid: reason is required (1-500 characters)")
	ErrInvalidAction        = errors.New("invalid: action is not possible for this report")
)

const (
	visibilityPublic    = "public"
	visibilityFollowers = "followers"
	visibilityPaid      = "paid"

	publisherTokenTTL = 12 * time.Hour
	viewerTokenTTL    = 4 * time.Hour
	// relationshipCacheTTL bounds how long a follow/block answer is reused.
	// The ws-gateway re-asks every 30s; a fresh block must land inside that.
	relationshipCacheTTL = 15 * time.Second

	recordingObjectKeyPrefix = "recordings/"

	// MaxModerators per stream.
	MaxModerators = 5
	// Report limits: one per reporter per target (unique index) and at most
	// reportsPerWindow reports per reporter per reportWindow.
	reportsPerWindow = 20
	reportWindow     = time.Hour
)

// Store is the storage surface live-service-v2 needs. The concrete
// implementation is *postgres.Store; the interface exists so unit
// tests can plug in a fake without spinning up Postgres.
type Store interface {
	CreateStream(ctx context.Context, p postgres.CreateStreamParams) (*postgres.LiveStream, error)
	GetByID(ctx context.Context, id uuid.UUID) (*postgres.LiveStream, error)
	GetByRoom(ctx context.Context, room string) (*postgres.LiveStream, error)
	ListLive(ctx context.Context, p postgres.ListLiveParams) ([]*postgres.LiveStream, error)
	ListScheduled(ctx context.Context, p postgres.ListScheduledParams) ([]*postgres.LiveStream, error)

	// Lifecycle (lifecycle.go).
	ApplyTransition(ctx context.Context, id uuid.UUID, decide postgres.TransitionFunc, events postgres.EventsFunc, audit *postgres.AuditEntry) (*postgres.TransitionResult, error)
	SetEgressID(ctx context.Context, id uuid.UUID, egressID string) error
	SetRecording(ctx context.Context, id uuid.UUID, url string, durationSec int, job postgres.RecordingImport) (*postgres.LiveStream, error)
	ClaimDueImports(ctx context.Context, limit int, lease time.Duration) ([]postgres.RecordingImport, error)
	CompleteImport(ctx context.Context, streamID, mediaID uuid.UUID, events func(*postgres.LiveStream, postgres.RecordingImport) ([]postgres.OutboxEvent, error)) error
	RetryImport(ctx context.Context, streamID uuid.UUID, mediaID *uuid.UUID, processingStatus, msg string, retryIn time.Duration) error
	TerminateImport(ctx context.Context, streamID uuid.UUID, mediaID *uuid.UUID, processingStatus, msg string) error
	ApplyPresence(ctx context.Context, streamID, userID uuid.UUID, present bool) (*postgres.LiveStream, bool, error)
	ListDueForTimeout(ctx context.Context, startTimeout, grace time.Duration, limit int) ([]uuid.UUID, error)
	ListByStatuses(ctx context.Context, statuses []string, limit int) ([]*postgres.AdminStream, error)
	WebhookSeen(ctx context.Context, eventID string) (bool, error)
	MarkWebhook(ctx context.Context, eventID, event string) error
	PruneWebhookEvents(ctx context.Context, keep time.Duration) error

	InsertChatMessage(ctx context.Context, streamID, userID uuid.UUID, text string) (*postgres.ChatMessage, error)
	ListRecentChatMessages(ctx context.Context, streamID uuid.UUID, limit int) ([]*postgres.ChatMessage, error)
	GetChatMessage(ctx context.Context, streamID, messageID uuid.UUID) (*postgres.ChatMessage, error)
	RemoveChatMessage(ctx context.Context, streamID, messageID, by uuid.UUID) (bool, error)

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

	// Moderation v2 (moderation.go).
	IsModerator(ctx context.Context, streamID, userID uuid.UUID) (bool, error)
	ListModerators(ctx context.Context, streamID uuid.UUID) ([]uuid.UUID, error)
	ModeratorsFor(ctx context.Context, streamIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error)
	ReplaceModerators(ctx context.Context, streamID uuid.UUID, userIDs []uuid.UUID, by uuid.UUID) error
	BanFromStream(ctx context.Context, streamID, userID, by uuid.UUID, reason string) error
	UnbanFromStream(ctx context.Context, streamID, userID uuid.UUID) error
	IsBannedFromStream(ctx context.Context, streamID, userID uuid.UUID) (bool, error)
	ListStreamBans(ctx context.Context, streamID uuid.UUID) ([]postgres.StreamBan, error)
	IsPlatformBanned(ctx context.Context, userID uuid.UUID) (bool, error)
	ListPlatformBans(ctx context.Context, limit, offset int) ([]postgres.PlatformBan, error)
	AdminSetPlatformBan(ctx context.Context, userID uuid.UUID, banned bool, reason string, audit postgres.AuditEntry) error
	ActiveStreamsOf(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	AdminRemoveChatMessage(ctx context.Context, streamID, messageID uuid.UUID, audit postgres.AuditEntry) (bool, error)
	CreateReport(ctx context.Context, r postgres.NewReport, maxPerWindow int, window time.Duration) (*postgres.Report, error)
	ListReports(ctx context.Context, status string, limit int) ([]*postgres.Report, error)
	AdminResolveReport(ctx context.Context, reportID uuid.UUID, act postgres.ResolveAction, check func(*postgres.Report) error, audit postgres.AuditEntry) (*postgres.Report, error)
}

// Service is the live-service-v2 business layer.
type Service struct {
	store   Store
	livekit livekit.Client
	graph   GraphClient
	redis   *redis.Client
	// rt publishes room events to live:stream:{id}; nil publishes nothing.
	rt RoomEvents
	// media imports egress recordings into media-service; nil leaves the
	// import jobs pending (vod_ready never goes out without a media id).
	media MediaImporter

	// Public base URL we expose recordings at (e.g. https://media.cdn/live-recordings).
	// If empty we fall back to the S3 endpoint + bucket path.
	recordingPublicBaseURL string
	s3Bucket               string
	s3Endpoint             string

	// pilot is the set of users who may go live. Empty = nobody.
	pilot  map[uuid.UUID]bool
	limits Limits

	// now is the clock the upcoming-streams listing measures "future"
	// against; nil means time.Now. A field so tests can pin it.
	now func() time.Time
}

// Config carries the service's settings.
type Config struct {
	RecordingPublicBaseURL string
	S3Bucket               string
	S3Endpoint             string

	// PilotUserIDs may create and start streams (LIVE_PILOT_USER_IDS).
	// Empty means nobody: going live fails closed.
	PilotUserIDs []uuid.UUID
	// StartTimeout (LIVE_START_TIMEOUT, default 120s) and ReconnectGrace
	// (LIVE_RECONNECT_GRACE, default 60s); zero takes the default.
	StartTimeout   time.Duration
	ReconnectGrace time.Duration

	// Media imports egress recordings (MEDIA_SERVICE_URL); nil = not configured.
	Media MediaImporter
}

// Default timeouts.
const (
	DefaultStartTimeout   = 120 * time.Second
	DefaultReconnectGrace = 60 * time.Second
)

func New(store Store, lk livekit.Client, graph GraphClient, rdb *redis.Client, cfg Config) *Service {
	pilot := make(map[uuid.UUID]bool, len(cfg.PilotUserIDs))
	for _, id := range cfg.PilotUserIDs {
		if id != uuid.Nil {
			pilot[id] = true
		}
	}
	lim := Limits{StartTimeout: cfg.StartTimeout, ReconnectGrace: cfg.ReconnectGrace}
	if lim.StartTimeout <= 0 {
		lim.StartTimeout = DefaultStartTimeout
	}
	if lim.ReconnectGrace <= 0 {
		lim.ReconnectGrace = DefaultReconnectGrace
	}
	var rt RoomEvents
	if rdb != nil {
		rt = NewRedisRoomEvents(rdb)
	}
	return &Service{
		store:                  store,
		livekit:                lk,
		graph:                  graph,
		redis:                  rdb,
		rt:                     rt,
		recordingPublicBaseURL: cfg.RecordingPublicBaseURL,
		s3Bucket:               cfg.S3Bucket,
		s3Endpoint:             cfg.S3Endpoint,
		pilot:                  pilot,
		limits:                 lim,
		media:                  cfg.Media,
	}
}

// ParsePilotUserIDs parses LIVE_PILOT_USER_IDS (comma-separated uuids).
// Invalid entries are returned separately and never admitted.
func ParsePilotUserIDs(raw string) (ids []uuid.UUID, invalid []string) {
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := uuid.Parse(part)
		if err != nil || id == uuid.Nil {
			invalid = append(invalid, part)
			continue
		}
		ids = append(ids, id)
	}
	return ids, invalid
}

// requireMayGoLive is the gate on create and start: the pilot allowlist
// (empty = nobody) and the platform live ban.
func (s *Service) requireMayGoLive(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil || !s.pilot[userID] {
		return ErrLiveNotEnabled
	}
	banned, err := s.store.IsPlatformBanned(ctx, userID)
	if err != nil {
		return fmt.Errorf("check live ban: %w", err)
	}
	if banned {
		return ErrLiveBanned
	}
	return nil
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
	if err := s.requireMayGoLive(ctx, creatorID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Title) == "" {
		return nil, ErrInvalidTitle
	}
	vis := normalizeVisibility(p.Visibility)
	if vis == "" {
		return nil, ErrInvalidVisibility
	}
	if vis == visibilityPaid {
		return nil, ErrPaidVisibility // existing paid rows stay as they are
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

// StartStream creates the LiveKit room and moves the stream to 'starting'.
// It does NOT say 'live': that waits for the host's first published track.
// Calling it again while starting/live/reconnecting re-issues the publisher
// token (a host rejoining) without touching the status.
func (s *Service) StartStream(ctx context.Context, streamID, creatorID uuid.UUID) (*StartStreamResult, error) {
	if err := s.requireMayGoLive(ctx, creatorID); err != nil {
		return nil, err
	}
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if st.CreatorUserID != creatorID {
		return nil, ErrNotCreator
	}
	if postgres.IsTerminal(st.Status) && st.Status != postgres.StatusFailed {
		return nil, ErrStateConflict
	}
	// LiveKit room — idempotent on duplicate name.
	if err := s.livekit.CreateRoom(ctx, st.LiveKitRoom); err != nil {
		return nil, fmt.Errorf("livekit create room: %w", err)
	}
	res, err := s.transition(ctx, streamID, TrigStart, nil)
	if err != nil {
		return nil, err
	}
	token, err := s.livekit.IssuePublisherToken(ctx, res.Next.LiveKitRoom, creatorID.String(), publisherTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("livekit publisher token: %w", err)
	}
	return &StartStreamResult{
		Stream:         res.Next,
		PublisherToken: token,
		Room:           res.Next.LiveKitRoom,
		ServerURL:      s.livekit.ServerURL(),
	}, nil
}

// EndStream is the host's own end: ended with ended_reason host_ended. The
// room is closed so every viewer is disconnected. Ending an ended stream is
// a no-op that returns the row.
func (s *Service) EndStream(ctx context.Context, streamID, creatorID uuid.UUID) (*postgres.LiveStream, error) {
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if st.CreatorUserID != creatorID {
		return nil, ErrNotCreator
	}
	res, err := s.transition(ctx, streamID, TrigHostEnd, nil)
	if err != nil {
		return nil, err
	}
	return res.Next, nil
}

// IssueViewerTokenResult is returned to viewers joining a stream.
type IssueViewerTokenResult struct {
	Token     string `json:"token"`
	Room      string `json:"room"`
	ServerURL string `json:"server_url"`
}

// IssueViewerToken runs the viewer gate (visibility, blocks, stream ban)
// then mints a subscriber-only LiveKit token while the stream is on air
// (starting, live or reconnecting).
func (s *Service) IssueViewerToken(ctx context.Context, streamID, viewerID uuid.UUID) (*IssueViewerTokenResult, error) {
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if err := s.authorizeViewer(ctx, st, viewerID); err != nil {
		return nil, err
	}
	if !onAir(st.Status) {
		return nil, ErrStreamNotLive
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

// onAir: a room exists for the stream and viewers may join it.
func onAir(status string) bool {
	return status == postgres.StatusStarting || status == postgres.StatusLive || status == postgres.StatusReconnecting
}

// chatOpen: chat is accepted while the host is (or is coming back) on air.
func chatOpen(status string) bool {
	return status == postgres.StatusLive || status == postgres.StatusReconnecting
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
		if err := s.canSee(ctx, st, viewerID); err != nil {
			continue
		}
		out = append(out, st)
	}
	out = s.decorateModerators(ctx, viewerID, out)
	res := &ListLiveResult{Streams: out}
	if len(out) == limit && out[len(out)-1].StartedAt != nil {
		last := out[len(out)-1]
		res.NextCursor = encodeCursor(*last.StartedAt, last.ID)
	}
	return res, nil
}

// Stream listing filters for GET /v1/livestream/streams?status= (MTube,
// 2026-09-27). Absent means live, and the live answer is ListLiveNow's,
// unchanged.
const (
	StreamStatusLive      = "live"
	StreamStatusScheduled = "scheduled"
	StreamStatusAll       = "all"

	// allLiveCursorPrefix / allScheduledCursorPrefix tag which phase an
	// ?status=all cursor resumes: the live list first, then the upcoming
	// one. The part after the prefix is that list's own cursor.
	allLiveCursorPrefix      = "live|"
	allScheduledCursorPrefix = "scheduled|"
)

// ErrInvalidStatusFilter is a ?status= value the listing does not know.
var ErrInvalidStatusFilter = errors.New("invalid: status must be live, scheduled or all")

// ListStreams answers GET /v1/livestream/streams for every ?status=.
func (s *Service) ListStreams(ctx context.Context, viewerID uuid.UUID, status string, limit int, cursor string) (*ListLiveResult, error) {
	switch status {
	case "", StreamStatusLive:
		return s.ListLiveNow(ctx, viewerID, limit, cursor)
	case StreamStatusScheduled:
		return s.ListScheduled(ctx, viewerID, limit, cursor)
	case StreamStatusAll:
		return s.listAllStreams(ctx, viewerID, limit, cursor)
	default:
		return nil, ErrInvalidStatusFilter
	}
}

// ListScheduled returns upcoming streams visible to viewerID, soonest
// first: status 'scheduled' with scheduled_at still in the future. The
// visibility rule is canSee, exactly as for the live list — public for
// everyone not blocked, followers-only for followers and the creator,
// paid never. The cursor is the same "<unix_micros>:<uuid>" keyset, over
// (scheduled_at, id) ascending.
func (s *Service) ListScheduled(ctx context.Context, viewerID uuid.UUID, limit int, cursor string) (*ListLiveResult, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	after, idAfter, err := parseCursor(cursor)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	batch := limit * 2 // over-fetch for the visibility filter, as ListLiveNow does
	streams, err := s.store.ListScheduled(ctx, postgres.ListScheduledParams{
		Limit:          batch,
		Now:            now().UTC(),
		ScheduledAfter: after,
		IDAfter:        idAfter,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*postgres.LiveStream, 0, limit)
	var last *postgres.LiveStream // the last row read, visible or not
	for _, st := range streams {
		if len(out) == limit {
			break
		}
		last = st
		if err := s.canSee(ctx, st, viewerID); err != nil {
			continue
		}
		out = append(out, st)
	}
	out = s.decorateModerators(ctx, viewerID, out)
	res := &ListLiveResult{Streams: out}
	// More may follow when the page filled, or when the store handed back a
	// full batch the filter thinned: resume after the last row READ, so a
	// run of hidden streams cannot end the listing early.
	if (len(out) == limit || len(streams) == batch) && last != nil && last.ScheduledAt != nil {
		res.NextCursor = encodeCursor(*last.ScheduledAt, last.ID)
	}
	return res, nil
}

// listAllStreams is ?status=all: every live stream first (ListLiveNow's
// order), then the upcoming ones (ListScheduled's). One page can straddle
// the two; the cursor records which list it resumes.
func (s *Service) listAllStreams(ctx context.Context, viewerID uuid.UUID, limit int, cursor string) (*ListLiveResult, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if strings.HasPrefix(cursor, allScheduledCursorPrefix) {
		return s.scheduledPhase(ctx, viewerID, limit, strings.TrimPrefix(cursor, allScheduledCursorPrefix), nil)
	}
	live, err := s.ListLiveNow(ctx, viewerID, limit, strings.TrimPrefix(cursor, allLiveCursorPrefix))
	if err != nil {
		return nil, err
	}
	if live.NextCursor != "" {
		return &ListLiveResult{Streams: live.Streams, NextCursor: allLiveCursorPrefix + live.NextCursor}, nil
	}
	remaining := limit - len(live.Streams)
	if remaining <= 0 {
		// The live list ended exactly on a full page: the next page is the
		// head of the upcoming list.
		return &ListLiveResult{Streams: live.Streams, NextCursor: allScheduledCursorPrefix}, nil
	}
	return s.scheduledPhase(ctx, viewerID, remaining, "", live.Streams)
}

// scheduledPhase appends a page of upcoming streams to prefix and tags the
// cursor with the scheduled phase.
func (s *Service) scheduledPhase(ctx context.Context, viewerID uuid.UUID, limit int, cursor string, prefix []*postgres.LiveStream) (*ListLiveResult, error) {
	sched, err := s.ListScheduled(ctx, viewerID, limit, cursor)
	if err != nil {
		return nil, err
	}
	out := make([]*postgres.LiveStream, 0, len(prefix)+len(sched.Streams))
	out = append(out, prefix...)
	out = append(out, sched.Streams...)
	res := &ListLiveResult{Streams: out}
	if sched.NextCursor != "" {
		res.NextCursor = allScheduledCursorPrefix + sched.NextCursor
	}
	return res, nil
}

// GetStream is the single-read variant used for both the live player and
// VOD playback. Visibility and blocks gate it; a stream ban does not hide
// the page (the banned viewer is told why they cannot join).
func (s *Service) GetStream(ctx context.Context, streamID, viewerID uuid.UUID) (*postgres.LiveStream, error) {
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if err := s.canSee(ctx, st, viewerID); err != nil {
		return nil, err
	}
	return s.decorateModerators(ctx, viewerID, []*postgres.LiveStream{st})[0], nil
}

// decorateModerators sets moderator_user_ids on the rows whose host or
// moderator is the viewer (an empty list for a host without moderators);
// everyone else gets no field. Rows are copied, never mutated. Best effort:
// a lookup error leaves the field off.
func (s *Service) decorateModerators(ctx context.Context, viewerID uuid.UUID, rows []*postgres.LiveStream) []*postgres.LiveStream {
	if viewerID == uuid.Nil || len(rows) == 0 {
		return rows
	}
	ids := make([]uuid.UUID, len(rows))
	for i, st := range rows {
		ids[i] = st.ID
	}
	mods, err := s.store.ModeratorsFor(ctx, ids)
	if err != nil {
		return rows
	}
	out := make([]*postgres.LiveStream, len(rows))
	for i, st := range rows {
		out[i] = st
		list := mods[st.ID]
		member := st.CreatorUserID == viewerID
		for _, id := range list {
			if id == viewerID {
				member = true
			}
		}
		if !member {
			continue
		}
		cp := *st
		ml := append([]uuid.UUID{}, list...)
		cp.ModeratorUserIDs = &ml
		out[i] = &cp
	}
	return out
}

// mayWatchBudget keeps the answer inside the ws-gateway's 3s timeout.
const mayWatchBudget = 2 * time.Second

// MayWatch answers the ws-gateway's internal viewer route: the viewer
// token's gate (visibility, blocks, stream ban) plus the platform live ban
// (a live-banned user gets no real-time room). A missing stream is a plain
// no; an undecidable gate is an error (the gateway refuses on it).
func (s *Service) MayWatch(ctx context.Context, streamID, viewerID uuid.UUID) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, mayWatchBudget)
	defer cancel()
	banned, err := s.store.IsPlatformBanned(ctx, viewerID)
	if err != nil {
		return false, ErrAuthorityUnavailable
	}
	if banned {
		return false, nil
	}
	st, err := s.store.GetByID(ctx, streamID)
	if errors.Is(err, postgres.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = s.authorizeViewer(ctx, st, viewerID)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrAuthorityUnavailable):
		return false, err
	default:
		return false, nil
	}
}

// canSee applies visibility (public / followers / paid) and blocks in
// either direction between viewer and host. viewerID may be uuid.Nil for
// unauthenticated readers (only public passes then).
func (s *Service) canSee(ctx context.Context, st *postgres.LiveStream, viewerID uuid.UUID) error {
	switch st.Visibility {
	case visibilityPublic, visibilityFollowers:
	case visibilityPaid:
		return ErrPaidNotSupported // not supported in v2, for anyone
	default:
		return ErrInvalidVisibility
	}
	if viewerID != uuid.Nil && viewerID == st.CreatorUserID {
		return nil // creators always see their own stream
	}
	if viewerID == uuid.Nil {
		if st.Visibility == visibilityPublic {
			return nil
		}
		return ErrNotFollower
	}
	rel, err := s.relationshipCached(ctx, viewerID, st.CreatorUserID)
	if err != nil {
		return ErrAuthorityUnavailable
	}
	if rel.Blocked {
		return ErrViewerBlocked
	}
	if st.Visibility == visibilityFollowers && !rel.Follows {
		return ErrNotFollower
	}
	return nil
}

// authorizeViewer is canSee plus the stream ban: the gate for the viewer
// token, chat send/list and the live room subscription.
func (s *Service) authorizeViewer(ctx context.Context, st *postgres.LiveStream, viewerID uuid.UUID) error {
	if err := s.canSee(ctx, st, viewerID); err != nil {
		return err
	}
	if viewerID == uuid.Nil || viewerID == st.CreatorUserID {
		return nil
	}
	banned, err := s.store.IsBannedFromStream(ctx, st.ID, viewerID)
	if err != nil {
		return ErrAuthorityUnavailable
	}
	if banned {
		return ErrBannedFromStream
	}
	return nil
}

// relationshipCached wraps the graph-service call with a short Redis cache
// keyed by (viewer, creator). An error is never cached.
func (s *Service) relationshipCached(ctx context.Context, viewerID, creatorID uuid.UUID) (Relationship, error) {
	if s.graph == nil {
		return Relationship{}, errors.New("graph client not configured")
	}
	if s.redis == nil {
		return s.graph.Relationship(ctx, viewerID, creatorID)
	}
	cacheKey := fmt.Sprintf("live_rel:%s:%s", viewerID, creatorID)
	if v, err := s.redis.Get(ctx, cacheKey).Result(); err == nil && len(v) == 2 {
		return Relationship{Follows: v[0] == '1', Blocked: v[1] == '1'}, nil
	}
	rel, err := s.graph.Relationship(ctx, viewerID, creatorID)
	if err != nil {
		return Relationship{}, err
	}
	val := []byte("00")
	if rel.Follows {
		val[0] = '1'
	}
	if rel.Blocked {
		val[1] = '1'
	}
	s.redis.Set(ctx, cacheKey, string(val), relationshipCacheTTL)
	return rel, nil
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

// --- Chat overlay ---
//
// Live chat is a thin REST surface backed by Redis pub/sub for fan-out.
// Clients SUBSCRIBE to `live:stream:{id}` through the ws-gateway's
// subscribe_live_stream (which asks MayWatch first); this service persists
// to live_chat_messages for replay-on-load.

// chatRateLimitKey returns the per-user-per-stream rate limit Redis
// key. Window is 60s; max 20 messages.
func chatRateLimitKey(streamID, userID uuid.UUID) string {
	return fmt.Sprintf("live_chat_rl:%s:%s", streamID.String(), userID.String())
}

const (
	chatRateLimitMax    = 20
	chatRateLimitWindow = 60 * time.Second
)

// SendChat persists a chat message and fans it out as chat.message. The
// sender must pass the viewer gate (visibility, blocks, stream ban), must
// not be live-banned platform-wide, and the stream must be live or
// reconnecting.
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
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if err := s.authorizeViewer(ctx, st, userID); err != nil {
		return nil, err
	}
	banned, err := s.store.IsPlatformBanned(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("check live ban: %w", err)
	}
	if banned {
		return nil, ErrLiveBanned
	}
	if !chatOpen(st.Status) {
		return nil, ErrStreamNotLive
	}

	// Moderation gates run BEFORE the rate-limit + persist so a muted
	// user / blocked word does not eat into the per-user budget and we
	// never write a row that will be hidden anyway.
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
	// The same row shape GET /chat returns.
	s.publish(ctx, streamID, EventChatMessage, msg)
	return msg, nil
}

// ListChat returns the most-recent `limit` messages that were not removed
// (default 50, max 200), behind the same viewer gate as sending. viewerID
// may be uuid.Nil for a signed-out reader of a public stream.
func (s *Service) ListChat(ctx context.Context, streamID, viewerID uuid.UUID, limit int) ([]*postgres.ChatMessage, error) {
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if err := s.authorizeViewer(ctx, st, viewerID); err != nil {
		return nil, err
	}
	return s.store.ListRecentChatMessages(ctx, streamID, limit)
}

// --- Chat moderation (Phase B) ---
//
// Mute/unmute are open to the host and stream moderators; word filters and
// pins stay host-only. Every change publishes a moderation.* event on
// live:stream:{id} so connected viewers can react in real time.

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

// Mute records a per-stream mute for targetUserID. Emits moderation.mute.
// Idempotent (UPSERT in the store). The host cannot be muted.
func (s *Service) Mute(ctx context.Context, streamID, actorID, targetUserID uuid.UUID) error {
	st, role, err := s.requireHostOrModerator(ctx, streamID, actorID)
	if err != nil {
		return err
	}
	if targetUserID == st.CreatorUserID {
		return ErrInvalidTarget
	}
	if err := s.store.MuteUser(ctx, streamID, targetUserID, actorID); err != nil {
		return err
	}
	s.publish(ctx, streamID, EventModerationMute, map[string]any{
		"stream_id": streamID.String(),
		"user_id":   targetUserID.String(),
		"muted_by":  actorID.String(),
		"by_role":   role,
		"muted_at":  time.Now().UTC(),
	})
	return nil
}

// Unmute clears a mute. Emits moderation.unmute.
func (s *Service) Unmute(ctx context.Context, streamID, actorID, targetUserID uuid.UUID) error {
	_, role, err := s.requireHostOrModerator(ctx, streamID, actorID)
	if err != nil {
		return err
	}
	if err := s.store.UnmuteUser(ctx, streamID, targetUserID); err != nil {
		return err
	}
	s.publish(ctx, streamID, EventModerationUnmute, map[string]any{
		"stream_id":  streamID.String(),
		"user_id":    targetUserID.String(),
		"unmuted_by": actorID.String(),
		"by_role":    role,
	})
	return nil
}

// ListMutedUsers returns the user IDs currently muted on the stream (host
// and moderators).
func (s *Service) ListMutedUsers(ctx context.Context, streamID, actorID uuid.UUID) ([]uuid.UUID, error) {
	if _, _, err := s.requireHostOrModerator(ctx, streamID, actorID); err != nil {
		return nil, err
	}
	return s.store.ListMutedUsers(ctx, streamID)
}

// AddWordFilter registers a substring filter word (lowercased,
// trim'd). Emits moderation.word_filter_added.
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
	s.publish(ctx, streamID, EventModerationWordFilterAdded, map[string]any{
		"stream_id": streamID.String(),
		"word":      w,
		"added_by":  hostID.String(),
	})
	return nil
}

// RemoveWordFilter deletes a filter word. Emits
// moderation.word_filter_removed.
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
	s.publish(ctx, streamID, EventModerationWordFilterRemoved, map[string]any{
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
// emits moderation.pin carrying the freshly-pinned message payload.
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
		s.publish(ctx, streamID, EventModerationPin, map[string]any{
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
// moderation.unpin.
func (s *Service) UnpinMessage(ctx context.Context, streamID, hostID, messageID uuid.UUID) error {
	if _, err := s.requireCreator(ctx, streamID, hostID); err != nil {
		return err
	}
	if err := s.store.UnpinMessage(ctx, streamID, messageID); err != nil {
		return err
	}
	s.publish(ctx, streamID, EventModerationUnpin, map[string]any{
		"stream_id":   streamID.String(),
		"message_id":  messageID.String(),
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
