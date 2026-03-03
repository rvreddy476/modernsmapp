package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Event struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	SessionID  uuid.UUID
	Type       string
	Payload    []byte // jsonb
	Timestamp  time.Time
	ReceivedAt time.Time
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// DeleteEventsByUser removes all analytics raw events for a given user (GDPR erasure).
func (s *Store) DeleteEventsByUser(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM analytics.events_raw WHERE user_id = $1::uuid`, userID)
	return err
}

func (s *Store) InsertBatch(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	query := `
		INSERT INTO analytics.events_raw (id, user_id, session_id, type, payload, ts, received_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	for _, e := range events {
		batch.Queue(query, e.ID, e.UserID, e.SessionID, e.Type, e.Payload, e.Timestamp, e.ReceivedAt)
	}

	br := s.db.SendBatch(ctx, batch)
	defer br.Close()

	if _, err := br.Exec(); err != nil {
		return err
	}
	return nil
}
