package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/analytics-service/internal/model"
	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

const (
	maxClientEventAge = 24 * time.Hour
	maxClockSkew      = 5 * time.Minute
	maxVideoDuration  = 12 * time.Hour
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

type IngestService struct {
	store *postgres.Store
	now   func() time.Time
}

// New keeps the historical signature so callers do not need a coordinated
// change. Client analytics is now durably written to PostgreSQL before the
// request succeeds; Kafka/Scylla are optional downstream accelerators and are
// deliberately not part of the acceptance proof.
func New(_ context.Context, store *postgres.Store, _ *kafka.Writer) *IngestService {
	return &IngestService{store: store, now: time.Now}
}

func (s *IngestService) Stop() {}

type playEndPayload struct {
	ContentID      string  `json:"content_id"`
	SessionID      string  `json:"session_id"`
	WatchedMSTotal int64   `json:"watched_ms_total"`
	DurationMS     int64   `json:"content_duration_ms"`
	LoopCount      int     `json:"loop_count"`
	EndReason      string  `json:"end_reason"`
	Surface        string  `json:"surface"`
	PercentViewed  float64 `json:"percent_viewed"`
	ClaimedCreator string  `json:"creator_id"`
	ClaimedViewer  string  `json:"viewer_id"`
}

// IngestEvents accepts only the launch view-completion event. Attribution is
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
	for _, dto := range dtos {
		if len(dto.EventID) < 16 || len(dto.EventID) > 128 {
			return IngestResult{}, errors.New("invalid event_id")
		}
		if dto.Type != model.EventPlayEnd {
			return IngestResult{}, fmt.Errorf("unsupported client analytics event: %s", dto.Type)
		}
		when := dto.Timestamp.UTC()
		if when.IsZero() || when.Before(now.Add(-maxClientEventAge)) || when.After(now.Add(maxClockSkew)) {
			return IngestResult{}, errors.New("event timestamp outside accepted window")
		}

		var payload playEndPayload
		if err := json.Unmarshal(dto.Payload, &payload); err != nil {
			return IngestResult{}, errors.New("invalid play_end payload")
		}
		contentID, err := uuid.Parse(payload.ContentID)
		if err != nil || contentID == uuid.Nil {
			return IngestResult{}, errors.New("invalid content_id")
		}
		sessionID, err := uuid.Parse(payload.SessionID)
		if err != nil || sessionID == uuid.Nil {
			return IngestResult{}, errors.New("invalid session_id")
		}
		if payload.DurationMS <= 0 || time.Duration(payload.DurationMS)*time.Millisecond > maxVideoDuration {
			return IngestResult{}, errors.New("invalid content duration")
		}
		if payload.WatchedMSTotal < 0 || payload.WatchedMSTotal > payload.DurationMS*10 {
			return IngestResult{}, errors.New("invalid watched duration")
		}
		if payload.LoopCount < 0 || payload.LoopCount > 20 {
			return IngestResult{}, errors.New("invalid loop count")
		}

		ownership, err := s.store.GetContentOwnership(ctx, contentID)
		if err != nil {
			return IngestResult{}, err
		}
		percentViewed := float64(payload.WatchedMSTotal) / float64(payload.DurationMS) * 100
		if percentViewed > 100 {
			percentViewed = 100
		}
		isDisplayView := model.IsDisplayView(
			ownership.ContentType,
			payload.DurationMS,
			payload.WatchedMSTotal,
			percentViewed,
			payload.LoopCount,
		)
		surface := normalizeSurface(payload.Surface)
		sanitized, err := json.Marshal(map[string]any{
			"content_id":          contentID.String(),
			"creator_id":          ownership.CreatorID.String(),
			"content_type":        ownership.ContentType,
			"session_id":          sessionID.String(),
			"watched_ms_total":    payload.WatchedMSTotal,
			"content_duration_ms": payload.DurationMS,
			"percent_viewed":      percentViewed,
			"loop_count":          payload.LoopCount,
			"end_reason":          normalizeEndReason(payload.EndReason),
			"surface":             surface,
			"is_display_view":     isDisplayView,
		})
		if err != nil {
			return IngestResult{}, err
		}
		accepted = append(accepted, postgres.Event{
			ID:            uuid.New(),
			ClientEventID: dto.EventID,
			UserID:        actorID,
			SessionID:     sessionID,
			ContentID:     contentID,
			Type:          model.EventPlayEnd,
			Payload:       sanitized,
			Timestamp:     when,
			ReceivedAt:    now,
		})
	}

	inserted, err := s.store.InsertAcceptedBatch(ctx, accepted)
	if err != nil {
		return IngestResult{}, err
	}
	return IngestResult{Accepted: inserted, Duplicate: len(accepted) - inserted}, nil
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
