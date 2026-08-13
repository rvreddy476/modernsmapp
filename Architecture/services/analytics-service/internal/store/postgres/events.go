package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Event struct {
	ID            uuid.UUID
	ClientEventID string
	UserID        uuid.UUID
	SessionID     uuid.UUID
	ContentID     uuid.UUID
	Type          string
	Payload       []byte // jsonb
	Timestamp     time.Time
	ReceivedAt    time.Time
}

type ContentOwnership struct {
	ContentID   uuid.UUID
	CreatorID   uuid.UUID
	ContentType string
	CreatedAt   time.Time
}

var ErrContentNotProjected = errors.New("content ownership is not projected")

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

// UpsertContentOwnership records canonical PostCreated attribution. Ownership
// is immutable: a conflicting author is rejected instead of silently moving a
// creator's historical analytics to another account.
func (s *Store) UpsertContentOwnership(ctx context.Context, ownership ContentOwnership) error {
	command, err := s.db.Exec(ctx, `
		INSERT INTO analytics.content_ownership
			(content_id, creator_id, content_type, created_at, projected_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (content_id) DO UPDATE SET
			content_type = EXCLUDED.content_type,
			projected_at = NOW()
		WHERE analytics.content_ownership.creator_id = EXCLUDED.creator_id`,
		ownership.ContentID, ownership.CreatorID, ownership.ContentType, ownership.CreatedAt)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("content ownership conflict")
	}
	return nil
}

func (s *Store) GetContentOwnership(ctx context.Context, contentID uuid.UUID) (ContentOwnership, error) {
	var ownership ContentOwnership
	err := s.db.QueryRow(ctx, `
		SELECT content_id, creator_id, content_type, created_at
		FROM analytics.content_ownership
		WHERE content_id = $1`, contentID).Scan(
		&ownership.ContentID, &ownership.CreatorID, &ownership.ContentType, &ownership.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContentOwnership{}, ErrContentNotProjected
	}
	return ownership, err
}

// InsertAcceptedBatch atomically creates each dedupe receipt and its raw
// analytics row. A duplicate receipt is a successful no-op. No 2xx caller can
// therefore depend on an in-memory queue that disappears on restart.
func (s *Store) InsertAcceptedBatch(ctx context.Context, events []Event) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	inserted := 0
	for _, event := range events {
		var receiptID string
		err := tx.QueryRow(ctx, `
			INSERT INTO analytics.ingest_receipts
				(event_id, actor_id, session_id, content_id, event_type)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT DO NOTHING
			RETURNING event_id`,
			event.ClientEventID, event.UserID, event.SessionID, event.ContentID, event.Type,
		).Scan(&receiptID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO analytics.events_raw
				(id, user_id, session_id, type, payload, ts, received_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			event.ID, event.UserID, event.SessionID, event.Type, event.Payload,
			event.Timestamp, event.ReceivedAt,
		); err != nil {
			return 0, err
		}
		inserted++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return inserted, nil
}
