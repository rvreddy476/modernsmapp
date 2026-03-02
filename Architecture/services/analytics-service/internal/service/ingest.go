package service

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/facebook-like/analytics-service/internal/model"
	"github.com/facebook-like/analytics-service/internal/store/postgres"
	"github.com/facebook-like/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type EventDTO struct {
	Type      string          `json:"type" binding:"required"`
	Payload   json.RawMessage `json:"payload" binding:"required"`
	Timestamp time.Time       `json:"timestamp" binding:"required"`
}

type IngestService struct {
	store         *postgres.Store
	kafkaWriter   *kafka.Writer // publishes video events to Kafka
	eventChan     chan postgres.Event
	batchSize     int
	flushInterval time.Duration
}

func New(store *postgres.Store, kafkaWriter *kafka.Writer) *IngestService {
	svc := &IngestService{
		store:         store,
		kafkaWriter:   kafkaWriter,
		eventChan:     make(chan postgres.Event, 10000), // Buffer for burst
		batchSize:     100,                              // Configurable
		flushInterval: 2 * time.Second,
	}
	go svc.processLoop()
	return svc
}

func (s *IngestService) IngestEvents(ctx context.Context, userID, sessionID string, dtos []EventDTO) error {
	// Parse IDs
	uID, _ := uuid.Parse(userID)
	sID, _ := uuid.Parse(sessionID)

	receivedAt := time.Now()

	for _, dto := range dtos {
		s.eventChan <- postgres.Event{
			ID:         uuid.New(),
			UserID:     uID,
			SessionID:  sID,
			Type:       dto.Type,
			Payload:    []byte(dto.Payload),
			Timestamp:  dto.Timestamp,
			ReceivedAt: receivedAt,
		}
	}
	return nil
}

func (s *IngestService) processLoop() {
	var batch []postgres.Event
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) > 0 {
			if err := s.store.InsertBatch(context.Background(), batch); err != nil {
				log.Printf("Error inserting batch: %v", err)
			}

			// Publish video events to Kafka for real-time processing
			if s.kafkaWriter != nil {
				s.publishVideoEvents(batch)
			}

			batch = nil
		}
	}

	for {
		select {
		case event := <-s.eventChan:
			batch = append(batch, event)
			if len(batch) >= s.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// publishVideoEvents filters the batch for video analytics events and publishes them
// to Kafka as EventEnvelope messages for the VideoViewConsumer.
func (s *IngestService) publishVideoEvents(batch []postgres.Event) {
	var messages []kafka.Message
	for _, e := range batch {
		if !model.VideoEventNames[e.Type] {
			continue
		}

		// Map the analytics event type to the shared event type constant
		eventType := mapToSharedEventType(e.Type)
		if eventType == "" {
			continue
		}

		actorID := e.UserID.String()
		envelope := events.NewEnvelope(context.Background(), eventType, &actorID, e.Payload)
		envelope.EventID = e.ID.String()
		envelope.OccurredAt = e.Timestamp

		data, err := json.Marshal(envelope)
		if err != nil {
			log.Printf("[IngestService] marshal envelope error: %v", err)
			continue
		}

		messages = append(messages, kafka.Message{
			Key:   []byte(envelope.EventID),
			Value: data,
		})
	}

	if len(messages) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.kafkaWriter.WriteMessages(ctx, messages...); err != nil {
		log.Printf("[IngestService] kafka publish error (%d messages): %v", len(messages), err)
	}
}

// mapToSharedEventType converts a client-side event name (e.g. "play_start")
// to the shared events constant (e.g. events.VideoPlayStart).
func mapToSharedEventType(clientType string) string {
	switch clientType {
	case model.EventImpression:
		return events.VideoImpression
	case model.EventPlayStart:
		return events.VideoPlayStart
	case model.EventWatchHeartbeat:
		return events.VideoHeartbeat
	case model.EventMilestone:
		return events.VideoMilestone
	case model.EventPlayEnd:
		return events.VideoPlayEnd
	case model.EventFollowFromContent:
		return events.VideoFollowFromContent
	case model.EventNotInterested:
		return events.VideoNotInterested
	case model.EventReport:
		return events.VideoReport
	case model.EventBlockCreator:
		return events.VideoBlockCreator
	default:
		return ""
	}
}
