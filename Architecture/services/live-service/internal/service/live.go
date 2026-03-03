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
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Service struct {
	store  *postgres.Store
	writer *kafka.Writer
}

func New(store *postgres.Store, kafkaBrokers string) *Service {
	w := &kafka.Writer{
		Addr:     kafka.TCP(kafkaBrokers),
		Topic:    "social.events.v1",
		Balancer: &kafka.LeastBytes{},
	}
	return &Service{store: store, writer: w}
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
	return st, nil
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
	go s.publishEvent(ctx, "LiveStarted", &hostID, events.LiveStartedPayload{
		StreamID:  streamID.String(),
		HostID:    hostID.String(),
		Title:     st.Title,
		StartedAt: time.Now(),
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

	return nil
}

func (s *Service) GetStream(ctx context.Context, streamID uuid.UUID) (*postgres.Stream, error) {
	return s.store.GetStream(ctx, streamID)
}

func (s *Service) ListLiveStreams(ctx context.Context, limit, offset int) ([]postgres.Stream, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.store.ListLiveStreams(ctx, limit, offset)
}

func (s *Service) ListHostStreams(ctx context.Context, hostID uuid.UUID, limit, offset int) ([]postgres.Stream, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.store.ListHostStreams(ctx, hostID, limit, offset)
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
	return count, nil
}

func (s *Service) LeaveStream(ctx context.Context, streamID, userID uuid.UUID) error {
	return s.store.LeaveStream(ctx, streamID, userID)
}

func (s *Service) LikeStream(ctx context.Context, streamID uuid.UUID) error {
	return s.store.IncrementLikes(ctx, streamID)
}

func (s *Service) GetViewerCount(ctx context.Context, streamID uuid.UUID) (int, error) {
	return s.store.GetActiveViewerCount(ctx, streamID)
}

// --- Chat ---

func (s *Service) SendChatMessage(ctx context.Context, streamID, userID uuid.UUID, message string) (*postgres.ChatMessage, error) {
	if len(message) == 0 || len(message) > 500 {
		return nil, fmt.Errorf("message must be 1-500 characters")
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
	return msg, nil
}

func (s *Service) GetChatMessages(ctx context.Context, streamID uuid.UUID, limit int, before *time.Time) ([]postgres.ChatMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.store.GetChatMessages(ctx, streamID, limit, before)
}

func (s *Service) PinMessage(ctx context.Context, messageID, hostID uuid.UUID) error {
	return s.store.PinMessage(ctx, messageID)
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

func (s *Service) publishEvent(ctx context.Context, eventType string, actorID *uuid.UUID, payload interface{}) {
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

	env := events.NewEnvelope(ctx, eventType, actorStr, data)

	envData, err := json.Marshal(env)
	if err != nil {
		log.Printf("Warning: failed to marshal %s envelope: %v", eventType, err)
		return
	}

	if err := s.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(eventType),
		Value: envData,
	}); err != nil {
		log.Printf("Warning: failed to publish %s event: %v", eventType, err)
	}
}
