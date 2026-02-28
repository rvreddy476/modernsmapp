package service

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/facebook-like/analytics-service/internal/store/postgres"
	"github.com/google/uuid"
)

type EventDTO struct {
	Type      string          `json:"type" binding:"required"`
	Payload   json.RawMessage `json:"payload" binding:"required"`
	Timestamp time.Time       `json:"timestamp" binding:"required"`
}

type IngestService struct {
	store         *postgres.Store
	eventChan     chan postgres.Event
	batchSize     int
	flushInterval time.Duration
}

func New(store *postgres.Store) *IngestService {
	svc := &IngestService{
		store:         store,
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
				// Retry / DLQ logic would go here
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
