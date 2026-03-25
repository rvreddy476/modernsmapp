package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/atpost/live-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	sharedtrace "github.com/atpost/shared/o11y/trace"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

type Service struct {
	store       Storer
	writer      *kafka.Writer
	realtime    *liveRealtimePublisher
	mediaConfig StreamMediaConfig
}

func New(store *postgres.Store, writer *kafka.Writer, rdb *redis.Client, mediaConfig StreamMediaConfig) *Service {
	return &Service{
		store:       store,
		writer:      writer,
		realtime:    newLiveRealtimePublisher(rdb),
		mediaConfig: mediaConfig.withDefaults(),
	}
}

func (s *Service) Close() {
	if s.writer != nil {
		s.writer.Close()
	}
}

// --- Stream Lifecycle ---

type CreateStreamInput struct {
	HostID       uuid.UUID
	Title        string
	Description  string
	ThumbnailURL *string
	Visibility   string
}

func (s *Service) CreateStream(ctx context.Context, input *CreateStreamInput) (*postgres.Stream, error) {
	if input.Title == "" {
		input.Title = "Live Stream"
	}
	visibility := input.Visibility
	if visibility == "" {
		visibility = "public"
	}

	streamKey := generateStreamKey()
	now := time.Now()

	st := &postgres.Stream{
		ID:           uuid.New(),
		HostID:       input.HostID,
		Title:        input.Title,
		Description:  input.Description,
		ThumbnailURL: input.ThumbnailURL,
		StreamKey:    streamKey,
		Status:       "idle",
		Visibility:   visibility,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.store.CreateStream(ctx, st); err != nil {
		return nil, err
	}
	return s.decorateStream(st, true), nil
}

func (s *Service) GoLive(ctx context.Context, streamID, hostID uuid.UUID) error {
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found")
	}
	if st.HostID != hostID {
		return fmt.Errorf("not the stream host")
	}
	if st.Status != "idle" {
		return fmt.Errorf("stream is already %s", st.Status)
	}

	if err := s.store.GoLive(ctx, streamID); err != nil {
		return err
	}

	// Publish LiveStarted event
	startedAt := time.Now()
	liveStream := *st
	liveStream.Status = "live"
	playbackURL := s.mediaConfig.playbackURL(&liveStream)
	playbackProtocol := ""
	if playbackURL != nil {
		playbackProtocol = s.mediaConfig.withDefaults().PlaybackProtocol
	}
	go s.publishEvent(ctx, "LiveStarted", &hostID, events.LiveStartedPayload{
		StreamID:         streamID.String(),
		HostID:           hostID.String(),
		Title:            st.Title,
		PlaybackURL:      derefString(playbackURL),
		PlaybackProtocol: playbackProtocol,
		StartedAt:        startedAt,
	})

	return nil
}

func (s *Service) EndStream(ctx context.Context, streamID, hostID uuid.UUID) error {
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found")
	}
	if st.HostID != hostID {
		return fmt.Errorf("not the stream host")
	}
	if st.Status != "live" {
		return fmt.Errorf("stream is not live")
	}

	if err := s.store.EndStream(ctx, streamID); err != nil {
		return err
	}

	// Publish LiveEnded event
	go s.publishEvent(ctx, "LiveEnded", &hostID, events.LiveEndedPayload{
		StreamID:     streamID.String(),
		HostID:       hostID.String(),
		DurationSecs: int(time.Since(*st.StartedAt).Seconds()),
		PeakViewers:  st.PeakViewers,
		TotalViewers: st.TotalViewers,
		EndedAt:      time.Now(),
	})
	s.realtime.publishStreamEvent(streamID, "live_stream_ended", map[string]any{
		"host_id":       hostID.String(),
		"duration_secs": int(time.Since(*st.StartedAt).Seconds()),
		"peak_viewers":  st.PeakViewers,
		"total_viewers": st.TotalViewers,
		"ended_at":      time.Now(),
	})

	return nil
}

func (s *Service) GetStream(ctx context.Context, streamID uuid.UUID) (*postgres.Stream, error) {
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return nil, err
	}
	return s.decorateStream(st, true), nil
}

func (s *Service) ListLiveStreams(ctx context.Context, limit, offset int) ([]postgres.Stream, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	streams, err := s.store.ListLiveStreams(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	decorated := s.decorateStreams(streams, false)
	for i := range decorated {
		decorated[i].StreamKey = ""
	}
	return decorated, nil
}

func (s *Service) ListHostStreams(ctx context.Context, hostID uuid.UUID, limit, offset int) ([]postgres.Stream, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	streams, err := s.store.ListHostStreams(ctx, hostID, limit, offset)
	if err != nil {
		return nil, err
	}
	return s.decorateStreams(streams, true), nil
}

// --- Viewer Interaction ---

func (s *Service) JoinStream(ctx context.Context, streamID, userID uuid.UUID) (int, error) {
	if err := s.store.JoinStream(ctx, streamID, userID); err != nil {
		return 0, err
	}
	count, err := s.store.GetActiveViewerCount(ctx, streamID)
	if err != nil {
		return 0, err
	}
	// Update peak/total
	_ = s.store.UpdateViewerCount(ctx, streamID, count)
	s.publishViewerCount(ctx, streamID, userID, count, "join")
	return count, nil
}

func (s *Service) LeaveStream(ctx context.Context, streamID, userID uuid.UUID) error {
	if err := s.store.LeaveStream(ctx, streamID, userID); err != nil {
		return err
	}
	count, err := s.store.GetActiveViewerCount(ctx, streamID)
	if err == nil {
		s.publishViewerCount(ctx, streamID, userID, count, "leave")
	}
	return nil
}

func (s *Service) LikeStream(ctx context.Context, streamID uuid.UUID) error {
	if err := s.store.IncrementLikes(ctx, streamID); err != nil {
		return err
	}
	if st, err := s.store.GetStream(ctx, streamID); err == nil {
		s.realtime.publishStreamEvent(streamID, "live_stream_likes", map[string]any{
			"like_count": st.LikeCount,
			"updated_at": time.Now(),
		})
	}
	return nil
}

func (s *Service) GetViewerCount(ctx context.Context, streamID uuid.UUID) (int, error) {
	return s.store.GetActiveViewerCount(ctx, streamID)
}

// --- Chat ---

func (s *Service) SendChatMessage(ctx context.Context, streamID, userID uuid.UUID, message string) (*postgres.ChatMessage, error) {
	if len(message) == 0 || len(message) > 500 {
		return nil, fmt.Errorf("message must be 1-500 characters")
	}

	// Enforce mute: muted users cannot send chat messages
	muted, err := s.store.IsUserMuted(ctx, streamID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check mute status: %w", err)
	}
	if muted {
		return nil, fmt.Errorf("you have been muted in this stream")
	}

	// Enforce word filters: reject messages containing filtered words
	filtered, err := s.store.MatchesWordFilter(ctx, streamID, message)
	if err != nil {
		return nil, fmt.Errorf("failed to check word filter: %w", err)
	}
	if filtered {
		return nil, fmt.Errorf("message contains a blocked word or phrase")
	}

	msg := &postgres.ChatMessage{
		ID:        uuid.New(),
		StreamID:  streamID,
		UserID:    userID,
		Message:   message,
		CreatedAt: time.Now(),
	}

	if err := s.store.SendChatMessage(ctx, msg); err != nil {
		return nil, err
	}
	s.realtime.publishStreamEvent(streamID, "live_chat_message", map[string]any{
		"id":         msg.ID.String(),
		"user_id":    msg.UserID.String(),
		"message":    msg.Message,
		"is_pinned":  msg.IsPinned,
		"created_at": msg.CreatedAt,
	})
	return msg, nil
}

func (s *Service) GetChatMessages(ctx context.Context, streamID uuid.UUID, limit int, before *time.Time) ([]postgres.ChatMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.store.GetChatMessages(ctx, streamID, limit, before)
}

func (s *Service) PinMessage(ctx context.Context, streamID, messageID, hostID uuid.UUID) error {
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found")
	}
	if st.HostID != hostID {
		return fmt.Errorf("only the stream host can pin messages")
	}
	msg, err := s.store.GetChatMessage(ctx, messageID)
	if err != nil {
		return fmt.Errorf("message not found")
	}
	if msg.StreamID != streamID {
		return fmt.Errorf("message does not belong to this stream")
	}
	if err := s.store.PinMessage(ctx, messageID); err != nil {
		return err
	}
	s.realtime.publishStreamEvent(streamID, "live_message_pinned", map[string]any{
		"message_id": messageID.String(),
		"pinned_by":  hostID.String(),
		"pinned_at":  time.Now(),
	})
	return nil
}

// --- Scheduled Streams ---

func (s *Service) ScheduleStream(ctx context.Context, hostID uuid.UUID, title, description string, scheduledAt time.Time) (*postgres.ScheduledStream, error) {
	if scheduledAt.Before(time.Now()) {
		return nil, fmt.Errorf("scheduled time must be in the future")
	}

	ss := &postgres.ScheduledStream{
		ID:          uuid.New(),
		HostID:      hostID,
		Title:       title,
		Description: description,
		ScheduledAt: scheduledAt,
		CreatedAt:   time.Now(),
	}

	if err := s.store.CreateScheduledStream(ctx, ss); err != nil {
		return nil, err
	}
	return ss, nil
}

func (s *Service) ListUpcomingStreams(ctx context.Context, limit int) ([]postgres.ScheduledStream, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.store.ListUpcomingStreams(ctx, limit)
}

// --- Helpers ---

func generateStreamKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "live_" + hex.EncodeToString(b)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Service) publishEvent(ctx context.Context, eventType string, actorID *uuid.UUID, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Warning: failed to marshal %s payload: %v", eventType, err)
		return
	}

	var actorStr *string
	if actorID != nil {
		str := actorID.String()
		actorStr = &str
	}

	publishCtx, cancel := detachedPublishContext(ctx)
	defer cancel()

	env := events.NewEnvelope(publishCtx, eventType, actorStr, data)

	envData, err := json.Marshal(env)
	if err != nil {
		log.Printf("Warning: failed to marshal %s envelope: %v", eventType, err)
		return
	}

	if err := s.writer.WriteMessages(publishCtx, kafka.Message{
		Key:   []byte(eventType),
		Value: envData,
	}); err != nil {
		log.Printf("Warning: failed to publish %s event: %v", eventType, err)
	}
}

func detachedPublishContext(ctx context.Context) (context.Context, context.CancelFunc) {
	publishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if ctx == nil {
		return publishCtx, cancel
	}

	if requestID := sharedtrace.RequestIDFrom(ctx); requestID != "" {
		publishCtx = sharedtrace.WithRequestID(publishCtx, requestID)
	}
	if traceID := sharedtrace.TraceIDFrom(ctx); traceID != "" {
		publishCtx = sharedtrace.WithTraceID(publishCtx, traceID)
	}
	if userID := sharedtrace.UserIDFrom(ctx); userID != "" {
		publishCtx = sharedtrace.WithUserID(publishCtx, userID)
	}
	return publishCtx, cancel
}

func (s *Service) publishViewerCount(ctx context.Context, streamID, actorID uuid.UUID, viewerCount int, reason string) {
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		s.realtime.publishStreamEvent(streamID, "live_stream_viewers", map[string]any{
			"viewer_count": viewerCount,
			"reason":       reason,
			"actor_id":     actorID.String(),
			"updated_at":   time.Now(),
		})
		return
	}

	s.realtime.publishStreamEvent(streamID, "live_stream_viewers", map[string]any{
		"viewer_count":  viewerCount,
		"peak_viewers":  st.PeakViewers,
		"total_viewers": st.TotalViewers,
		"reason":        reason,
		"actor_id":      actorID.String(),
		"updated_at":    time.Now(),
	})
}
