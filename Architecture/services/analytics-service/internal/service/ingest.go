package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/analytics-service/internal/model"
	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/store/scylla"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

const (
	maxClientEventAge = 24 * time.Hour
	maxClockSkew      = 5 * time.Minute
	maxVideoDuration  = 12 * time.Hour

	// A single heartbeat covers at most this much wall-clock playback.
	// Reels beat every 2s and long video every 5s; ten minutes is a
	// generous ceiling that still rejects a client trying to claim an
	// hour of watch time in one message.
	maxHeartbeatIncrementMS = 10 * 60 * 1000
	// An impression is a viewport-visibility measurement, not playback.
	maxImpressionVisibleMS = 10 * 60 * 1000
)

type EventDTO struct {
	EventID   string          `json:"event_id" binding:"required"`
	Type      string          `json:"type" binding:"required"`
	Payload   json.RawMessage `json:"payload" binding:"required"`
	Timestamp time.Time       `json:"timestamp" binding:"required"`
}

type IngestResult struct {
	Accepted  int `json:"accepted"`
	Duplicate int `json:"duplicate"`
}

// WatchSessionWriter is the slice of the Scylla watch store the ingest
// path needs. An interface so the service stays testable without a
// cluster, and so a deployment without Scylla simply does less rather
// than failing.
type WatchSessionWriter interface {
	UpsertWatchSession(ctx context.Context, ws *scylla.WatchSession) error
}

type IngestService struct {
	store *postgres.Store
	watch WatchSessionWriter
	now   func() time.Time
}

// WithWatchStore attaches the Scylla watch-session store. Optional: the
// PostgreSQL write is the durability contract behind the 202, and this
// is a best-effort projection on top of it. Without it the retention and
// audience-demographics endpoints have nothing to read, which is why
// they returned nothing for HTTP-ingested watch sessions.
func (s *IngestService) WithWatchStore(w WatchSessionWriter) *IngestService {
	if w != nil {
		s.watch = w
	}
	return s
}

// New keeps the historical signature so callers do not need a coordinated
// change. Client analytics is now durably written to PostgreSQL before the
// request succeeds; Kafka/Scylla are optional downstream accelerators and are
// deliberately not part of the acceptance proof.
func New(_ context.Context, store *postgres.Store, _ *kafka.Writer) *IngestService {
	return &IngestService{store: store, now: time.Now}
}

func (s *IngestService) Stop() {}

// clientEvent is the union of every field any of the thirteen video
// analytics events carries. Decoding into one struct keeps the
// per-type validators small; each validator reads only the fields its
// own event defines, and nothing a client sends is persisted unless a
// validator explicitly copies it into the sanitized payload.
type clientEvent struct {
	ContentID string `json:"content_id"`
	SessionID string `json:"session_id"`
	Surface   string `json:"surface"`
	Position  int    `json:"position"`

	// Never trusted — the gateway actor and the PostCreated ownership
	// projection are the only attribution sources. Declared so an
	// explicit test can assert they are dropped.
	ClaimedCreator string `json:"creator_id"`
	ClaimedViewer  string `json:"viewer_id"`

	// impression
	VisibleMS int64 `json:"visible_ms"`

	// play_start
	ContentDurationMS  int64  `json:"content_duration_ms"`
	StartMethod        string `json:"start_method"`
	IsMuted            bool   `json:"is_muted"`
	TimeToFirstFrameMS int64  `json:"time_to_first_frame_ms"`
	InitialBufferMS    int64  `json:"initial_buffer_ms"`
	IsAutoplay         bool   `json:"is_autoplay"`

	// watch_heartbeat
	WatchedMSIncrement   int64   `json:"watched_ms_increment"`
	BufferingMSIncrement int64   `json:"buffering_ms_increment"`
	SeekCountIncrement   int     `json:"seek_count_increment"`
	PlayheadPositionMS   int64   `json:"playhead_position_ms"`
	PlaybackSpeed        float64 `json:"playback_speed"`

	// milestone
	MilestoneType string `json:"milestone_type"`
	WatchedMS     int64  `json:"watched_ms"`

	// watch_heartbeat + play_end
	WatchedMSTotal int64 `json:"watched_ms_total"`

	// play_end
	EndReason            string `json:"end_reason"`
	MaxContinuousWatchMS int64  `json:"max_continuous_watch_ms"`
	LoopCount            int    `json:"loop_count"`

	// negative signals
	Reason string `json:"reason"`
}

// normalizedEvent is the validated, attribution-corrected result of one
// client event, ready to be written. Kept separate from postgres.Event
// so the validators are pure and unit-testable without a database.
type normalizedEvent struct {
	Type       string
	ContentID  uuid.UUID
	SessionID  uuid.UUID
	DedupeKey  *string
	Attributes map[string]any
}

// IngestEvents accepts the full video analytics event model — all
// thirteen types in internal/model/video_events.go. Attribution is
// rebuilt from the gateway actor and the canonical PostCreated ownership
// projection; client creator/viewer fields are never persisted.
func (s *IngestService) IngestEvents(ctx context.Context, userID string, dtos []EventDTO) (IngestResult, error) {
	actorID, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil || actorID == uuid.Nil {
		return IngestResult{}, errors.New("invalid authenticated actor")
	}
	if len(dtos) == 0 || len(dtos) > 200 {
		return IngestResult{}, errors.New("event batch must contain 1 to 200 events")
	}

	now := s.now().UTC()
	accepted := make([]postgres.Event, 0, len(dtos))
	// One ownership lookup per distinct content in the batch; a realistic
	// batch is many events about a handful of videos.
	ownerships := make(map[uuid.UUID]postgres.ContentOwnership, 8)
	// Watch sessions to project after the durable write lands, keyed by
	// the client's event_id so only the rows that were actually inserted
	// are fanned out.
	watchByEventID := make(map[string]*scylla.WatchSession, 4)

	for _, dto := range dtos {
		if len(dto.EventID) < 16 || len(dto.EventID) > 128 {
			return IngestResult{}, errors.New("invalid event_id")
		}
		if !model.VideoEventNames[dto.Type] {
			return IngestResult{}, fmt.Errorf("unsupported client analytics event: %s", dto.Type)
		}
		when := dto.Timestamp.UTC()
		if when.IsZero() || when.Before(now.Add(-maxClientEventAge)) || when.After(now.Add(maxClockSkew)) {
			return IngestResult{}, errors.New("event timestamp outside accepted window")
		}

		var raw clientEvent
		if err := json.Unmarshal(dto.Payload, &raw); err != nil {
			return IngestResult{}, fmt.Errorf("invalid %s payload", dto.Type)
		}
		contentID, err := uuid.Parse(strings.TrimSpace(raw.ContentID))
		if err != nil || contentID == uuid.Nil {
			return IngestResult{}, errors.New("invalid content_id")
		}

		ownership, seen := ownerships[contentID]
		if !seen {
			ownership, err = s.store.GetContentOwnership(ctx, contentID)
			if err != nil {
				return IngestResult{}, err
			}
			ownerships[contentID] = ownership
		}

		norm, err := normalizeEvent(dto.Type, &raw, ownership)
		if err != nil {
			return IngestResult{}, err
		}

		if ws := watchSessionFor(norm); ws != nil {
			watchByEventID[dto.EventID] = ws
		}

		sanitized, err := json.Marshal(norm.Attributes)
		if err != nil {
			return IngestResult{}, err
		}
		accepted = append(accepted, postgres.Event{
			ID:            uuid.New(),
			ClientEventID: dto.EventID,
			UserID:        actorID,
			SessionID:     norm.SessionID,
			ContentID:     norm.ContentID,
			Type:          norm.Type,
			DedupeKey:     norm.DedupeKey,
			Payload:       sanitized,
			Timestamp:     when,
			ReceivedAt:    now,
		})
	}

	inserted, err := s.store.InsertAcceptedBatch(ctx, accepted)
	if err != nil {
		return IngestResult{}, err
	}
	s.projectWatchSessions(ctx, actorID, inserted, watchByEventID)
	return IngestResult{Accepted: len(inserted), Duplicate: len(accepted) - len(inserted)}, nil
}

// projectWatchSessions mirrors the accepted play_end rows into Scylla so
// the retention curve and audience-demographics surfaces have something
// to read. Best-effort by construction: the caller has already been told
// its events are durable, so a Scylla outage degrades those two charts
// rather than failing an ingest that has already succeeded. Only the
// rows PostgreSQL actually inserted are projected, so a replayed batch
// cannot double-count a session.
func (s *IngestService) projectWatchSessions(ctx context.Context, actorID uuid.UUID, inserted []postgres.Event, sessions map[string]*scylla.WatchSession) {
	if s.watch == nil || len(sessions) == 0 {
		return
	}
	for _, event := range inserted {
		ws, ok := sessions[event.ClientEventID]
		if !ok {
			continue
		}
		ws.ViewerID = actorID
		ws.CreatedAt = event.Timestamp
		ws.UpdatedAt = event.ReceivedAt
		if err := s.watch.UpsertWatchSession(ctx, ws); err != nil {
			slog.Warn("watch session projection failed",
				"content_id", ws.ContentID.String(), "session_id", ws.SessionID, "error", err)
		}
	}
}

// watchSessionFor builds the Scylla row for a normalized play_end. Every
// field comes from the already-validated attributes, so the projection
// cannot disagree with what was persisted to PostgreSQL.
func watchSessionFor(norm *normalizedEvent) *scylla.WatchSession {
	if norm.Type != model.EventPlayEnd {
		return nil
	}
	a := norm.Attributes
	ws := &scylla.WatchSession{
		ContentID:   norm.ContentID,
		SessionID:   norm.SessionID.String(),
		ContentType: asString(a["content_type"]),
		Surface:     asString(a["surface"]),
		EndReason:   asString(a["end_reason"]),
		TrustFactor: 1.0,
	}
	if v, ok := a["content_duration_ms"].(int64); ok {
		ws.DurationMS = v
	}
	if v, ok := a["watched_ms_total"].(int64); ok {
		ws.WatchedMS = v
	}
	if v, ok := a["percent_viewed"].(float64); ok {
		ws.PercentViewed = v
		ws.VQS = v / 100
	}
	if v, ok := a["loop_count"].(int); ok {
		ws.LoopCount = v
	}
	if v, ok := a["is_display_view"].(bool); ok {
		ws.IsDisplayView = v
	}
	return ws
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// requiresSession lists the event types that are meaningless outside a
// playback session. The engagement and negative-signal events can be
// emitted from a card the viewer never played, so they carry the nil
// session rather than being rejected.
var requiresSession = map[string]bool{
	model.EventPlayStart:      true,
	model.EventWatchHeartbeat: true,
	model.EventMilestone:      true,
	model.EventPlayEnd:        true,
}

// oncePerSession are the signals a viewer can only give once for a piece
// of content within one session. Persisting a second one would inflate
// the engagement rates that feed the content quality score, so they get
// a dedupe_key and collapse in the receipts table.
var oncePerSession = map[string]bool{
	model.EventLike:              true,
	model.EventShare:             true,
	model.EventSave:              true,
	model.EventFollowFromContent: true,
	model.EventNotInterested:     true,
	model.EventReport:            true,
	model.EventBlockCreator:      true,
}

// normalizeEvent validates one decoded client event against its type's
// rules and returns the sanitized row to persist. Pure: no I/O, so the
// twelve-type acceptance matrix is unit-testable.
func normalizeEvent(eventType string, raw *clientEvent, ownership postgres.ContentOwnership) (*normalizedEvent, error) {
	sessionID := uuid.Nil
	if trimmed := strings.TrimSpace(raw.SessionID); trimmed != "" {
		parsed, err := uuid.Parse(trimmed)
		if err != nil {
			return nil, errors.New("invalid session_id")
		}
		sessionID = parsed
	}
	if requiresSession[eventType] && sessionID == uuid.Nil {
		return nil, fmt.Errorf("%s requires a session_id", eventType)
	}

	attrs := map[string]any{
		"content_id":   ownership.ContentID.String(),
		"creator_id":   ownership.CreatorID.String(),
		"content_type": ownership.ContentType,
		"session_id":   sessionID.String(),
		"surface":      normalizeSurface(raw.Surface),
		"event_name":   eventType,
	}
	if raw.Position > 0 && raw.Position <= 10_000 {
		attrs["position"] = raw.Position
	}

	norm := &normalizedEvent{
		Type:       eventType,
		ContentID:  ownership.ContentID,
		SessionID:  sessionID,
		Attributes: attrs,
	}
	if oncePerSession[eventType] {
		key := "session"
		norm.DedupeKey = &key
	}

	switch eventType {
	case model.EventImpression:
		if raw.VisibleMS < 0 || raw.VisibleMS > maxImpressionVisibleMS {
			return nil, errors.New("invalid visible_ms")
		}
		attrs["visible_ms"] = raw.VisibleMS
		attrs["is_autoplay"] = raw.IsAutoplay

	case model.EventPlayStart:
		if err := validDuration(raw.ContentDurationMS); err != nil {
			return nil, err
		}
		if raw.TimeToFirstFrameMS < 0 || raw.TimeToFirstFrameMS > maxHeartbeatIncrementMS {
			return nil, errors.New("invalid time_to_first_frame_ms")
		}
		if raw.InitialBufferMS < 0 || raw.InitialBufferMS > maxHeartbeatIncrementMS {
			return nil, errors.New("invalid initial_buffer_ms")
		}
		attrs["content_duration_ms"] = raw.ContentDurationMS
		attrs["start_method"] = normalizeStartMethod(raw.StartMethod)
		attrs["is_muted"] = raw.IsMuted
		attrs["is_autoplay"] = raw.IsAutoplay
		attrs["time_to_first_frame_ms"] = raw.TimeToFirstFrameMS
		attrs["initial_buffer_ms"] = raw.InitialBufferMS

	case model.EventWatchHeartbeat:
		if raw.WatchedMSIncrement < 0 || raw.WatchedMSIncrement > maxHeartbeatIncrementMS {
			return nil, errors.New("invalid watched_ms_increment")
		}
		if raw.WatchedMSTotal < 0 || time.Duration(raw.WatchedMSTotal)*time.Millisecond > maxVideoDuration {
			return nil, errors.New("invalid watched_ms_total")
		}
		if raw.WatchedMSIncrement > raw.WatchedMSTotal {
			return nil, errors.New("heartbeat increment exceeds running total")
		}
		if raw.PlayheadPositionMS < 0 || time.Duration(raw.PlayheadPositionMS)*time.Millisecond > maxVideoDuration {
			return nil, errors.New("invalid playhead_position_ms")
		}
		if raw.BufferingMSIncrement < 0 || raw.BufferingMSIncrement > maxHeartbeatIncrementMS {
			return nil, errors.New("invalid buffering_ms_increment")
		}
		if raw.SeekCountIncrement < 0 || raw.SeekCountIncrement > 1000 {
			return nil, errors.New("invalid seek_count_increment")
		}
		speed := raw.PlaybackSpeed
		if speed == 0 {
			speed = 1
		}
		if speed < 0.25 || speed > 4 {
			return nil, errors.New("invalid playback_speed")
		}
		attrs["watched_ms_increment"] = raw.WatchedMSIncrement
		attrs["watched_ms_total"] = raw.WatchedMSTotal
		attrs["playhead_position_ms"] = raw.PlayheadPositionMS
		attrs["buffering_ms_increment"] = raw.BufferingMSIncrement
		attrs["seek_count_increment"] = raw.SeekCountIncrement
		attrs["playback_speed"] = speed

	case model.EventMilestone:
		milestone := strings.ToUpper(strings.TrimSpace(raw.MilestoneType))
		if !validMilestone(milestone) {
			return nil, fmt.Errorf("invalid milestone_type: %q", raw.MilestoneType)
		}
		if raw.WatchedMS < 0 || time.Duration(raw.WatchedMS)*time.Millisecond > maxVideoDuration {
			return nil, errors.New("invalid watched_ms")
		}
		attrs["milestone_type"] = milestone
		attrs["watched_ms"] = raw.WatchedMS
		if bucket, ok := model.MilestoneToViewBucket[milestone]; ok {
			attrs["view_bucket"] = bucket
		}
		// One crossing of a given threshold per session. A replay that
		// re-crosses PCT_50 is a loop, not a second milestone.
		key := milestone
		norm.DedupeKey = &key

	case model.EventPlayEnd:
		if err := validDuration(raw.ContentDurationMS); err != nil {
			return nil, err
		}
		if raw.WatchedMSTotal < 0 || raw.WatchedMSTotal > raw.ContentDurationMS*10 {
			return nil, errors.New("invalid watched duration")
		}
		if raw.LoopCount < 0 || raw.LoopCount > 20 {
			return nil, errors.New("invalid loop count")
		}
		if raw.MaxContinuousWatchMS < 0 || raw.MaxContinuousWatchMS > raw.WatchedMSTotal {
			return nil, errors.New("invalid max_continuous_watch_ms")
		}
		percentViewed := float64(raw.WatchedMSTotal) / float64(raw.ContentDurationMS) * 100
		if percentViewed > 100 {
			percentViewed = 100
		}
		isDisplayView := model.IsDisplayView(
			ownership.ContentType,
			raw.ContentDurationMS,
			raw.WatchedMSTotal,
			percentViewed,
			raw.LoopCount,
		)
		attrs["watched_ms_total"] = raw.WatchedMSTotal
		attrs["max_continuous_watch_ms"] = raw.MaxContinuousWatchMS
		attrs["content_duration_ms"] = raw.ContentDurationMS
		attrs["percent_viewed"] = percentViewed
		attrs["loop_count"] = raw.LoopCount
		attrs["end_reason"] = normalizeEndReason(raw.EndReason)
		attrs["is_display_view"] = isDisplayView

	case model.EventLike,
		model.EventCommentCreate,
		model.EventShare,
		model.EventSave,
		model.EventFollowFromContent:
		// Positive engagement carries no payload beyond the common
		// fields. It is counted, never quoted back.

	case model.EventNotInterested,
		model.EventReport,
		model.EventBlockCreator:
		// Negative signals may carry a reason. Bound it so a client
		// cannot use analytics as free-text storage, and keep it to a
		// closed set so it stays aggregatable.
		attrs["reason"] = normalizeNegativeReason(raw.Reason)

	default:
		return nil, fmt.Errorf("unsupported client analytics event: %s", eventType)
	}

	return norm, nil
}

func validDuration(durationMS int64) error {
	if durationMS <= 0 || time.Duration(durationMS)*time.Millisecond > maxVideoDuration {
		return errors.New("invalid content duration")
	}
	return nil
}

func validMilestone(milestone string) bool {
	if _, ok := model.MilestoneToViewBucket[milestone]; ok {
		return true
	}
	switch milestone {
	case "PCT_25", "PCT_50", "PCT_75", "PCT_95":
		return true
	}
	return false
}

func normalizeSurface(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "feed", "posttube", "profile", "search", "channel":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func normalizeEndReason(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ended", "swipe_next", "paused", "backgrounded", "error":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func normalizeStartMethod(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "autoplay", "tap", "resume":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func normalizeNegativeReason(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "spam", "nudity", "violence", "hate", "misinformation",
		"repetitive", "irrelevant", "dislike_creator":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "unspecified"
	}
}
