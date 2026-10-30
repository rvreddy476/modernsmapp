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
	// DedupeKey narrows the "one per (actor, session, content, type)"
	// receipt rule for the event types where repetition is an artefact
	// rather than a new signal — the milestone kind for milestones, a
	// constant for once-per-session engagement. nil means the client's
	// event_id is the only dedupe key, which is right for heartbeats,
	// impressions and comments.
	DedupeKey  *string
	Payload    []byte // jsonb
	Timestamp  time.Time
	ReceivedAt time.Time
	// Session is this event's contribution to analytics.playback_sessions,
	// applied in the same transaction as the receipt. nil for the
	// engagement types and for anything the Kafka consumers write.
	Session *SessionUpdate
}

type ContentOwnership struct {
	ContentID   uuid.UUID
	CreatorID   uuid.UUID
	ContentType string
	CreatedAt   time.Time
	// EligibleFrom is the deferred-publication floor (migration 014): set
	// when post-service withheld the PostCreated of a post held for
	// copyright review, so no day before the clear earns. nil: no floor.
	EligibleFrom *time.Time
	// MediaDurationMS is the server-measured length of the content's
	// longest video (migration 015), from PostCreated or the eligibility
	// event post-service emits once transcode has measured it. 0 means
	// unknown. It — never the client's content_duration_ms — is what a
	// playback event is clamped and qualified against (audit M6).
	MediaDurationMS int64
}

var ErrContentNotProjected = errors.New("content ownership is not projected")

// LikeDedupeKey is the receipt key for a like from either write path. A
// like is state, not an event — a viewer has either liked a piece of
// content or not — so it collapses on (actor, nil session, content)
// with this key regardless of how many sessions or clients reported
// it. The HTTP ingest path and the Kafka engagement consumer both use
// it, which is what lets the partial unique index on ingest_receipts
// fold the two copies of one like onto one row (audit M-05).
const LikeDedupeKey = "content"

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// DeleteEventsByUser is the service's account-erasure path (GDPR): it
// removes the user's raw analytics events and, as a creator, their daily
// follower counts (migration 013), in one transaction. Idempotent.
//
// The follower receipts are not touched: they carry no user id, and
// keeping them stops a late redelivery from re-creating erased rows.
func (s *Store) DeleteEventsByUser(ctx context.Context, userID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`DELETE FROM analytics.events_raw WHERE user_id = $1::uuid`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM analytics.creator_follower_daily WHERE creator_id = $1::uuid`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
			(content_id, creator_id, content_type, created_at, projected_at, eligibility_eligible_from, media_duration_ms)
		VALUES ($1, $2, $3, $4, NOW(), $5, GREATEST($6::bigint, 0))
		ON CONFLICT (content_id) DO UPDATE SET
			content_type = EXCLUDED.content_type,
			projected_at = NOW(),
			-- A floor only ever rises (GREATEST ignores NULL).
			eligibility_eligible_from = GREATEST(analytics.content_ownership.eligibility_eligible_from, EXCLUDED.eligibility_eligible_from),
			-- A replayed PostCreated from before transcode carries no
			-- duration; it must not erase one that has since been measured.
			media_duration_ms = CASE WHEN EXCLUDED.media_duration_ms > 0
			                         THEN EXCLUDED.media_duration_ms
			                         ELSE analytics.content_ownership.media_duration_ms END
		WHERE analytics.content_ownership.creator_id = EXCLUDED.creator_id`,
		ownership.ContentID, ownership.CreatorID, ownership.ContentType, ownership.CreatedAt, ownership.EligibleFrom,
		ownership.MediaDurationMS)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("content ownership conflict")
	}
	return nil
}

// UpdateContentType applies a post-service reclassification to an already
// projected row. post-service fires PostContentTypeChanged when the
// transcode pipeline reveals a video's real duration and dimensions and
// shared/postclassify puts it in the other bucket; without this the
// ownership projection kept whatever kind the post had at create time,
// and that stale label is what decides both the display-view bar
// (model.IsDisplayView) and the RPM rate monetization settles at.
//
// Deliberately an UPDATE and not an upsert: it must never invent an
// ownership row. PostCreated and PostContentTypeChanged are produced to
// the same topic keyed by author, so the create always lands first;
// applied=false means the row genuinely is not there and there is
// nothing to correct.
func (s *Store) UpdateContentType(ctx context.Context, contentID, creatorID uuid.UUID, contentType string) (applied bool, err error) {
	command, err := s.db.Exec(ctx, `
		UPDATE analytics.content_ownership
		SET content_type = $3, projected_at = NOW()
		WHERE content_id = $1 AND creator_id = $2`,
		contentID, creatorID, contentType)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() > 0, nil
}

func (s *Store) GetContentOwnership(ctx context.Context, contentID uuid.UUID) (ContentOwnership, error) {
	var ownership ContentOwnership
	err := s.db.QueryRow(ctx, `
		SELECT content_id, creator_id, content_type, created_at, media_duration_ms
		FROM analytics.content_ownership
		WHERE content_id = $1`, contentID).Scan(
		&ownership.ContentID, &ownership.CreatorID, &ownership.ContentType, &ownership.CreatedAt,
		&ownership.MediaDurationMS)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContentOwnership{}, ErrContentNotProjected
	}
	return ownership, err
}

// InsertAcceptedBatch atomically creates each dedupe receipt and its raw
// analytics row. A duplicate receipt is a successful no-op. No 2xx caller can
// therefore depend on an in-memory queue that disappears on restart.
//
// A playback event (one carrying a Session) is also folded into its
// analytics.playback_sessions row in the same transaction, so the
// durability contract behind the 202 covers the session too. Duplicates
// are skipped before that step: the receipt already proved the event
// was applied once, and the session totals are GREATEST-merged anyway.
//
// Returns the events that were actually written (not the duplicates), so
// the caller can fan those — and only those — out to the downstream
// accelerators without double-counting a replay.
func (s *Store) InsertAcceptedBatch(ctx context.Context, events []Event) ([]Event, error) {
	if len(events) == 0 {
		return nil, nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	inserted := make([]Event, 0, len(events))
	for _, event := range events {
		var receiptID string
		err := tx.QueryRow(ctx, `
			INSERT INTO analytics.ingest_receipts
				(event_id, actor_id, session_id, content_id, event_type, dedupe_key)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT DO NOTHING
			RETURNING event_id`,
			event.ClientEventID, event.UserID, event.SessionID, event.ContentID,
			event.Type, event.DedupeKey,
		).Scan(&receiptID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO analytics.events_raw
				(id, user_id, session_id, type, payload, ts, received_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			event.ID, event.UserID, event.SessionID, event.Type, event.Payload,
			event.Timestamp, event.ReceivedAt,
		); err != nil {
			return nil, err
		}
		if event.Session != nil {
			if err := applySessionUpdate(ctx, tx, event); err != nil {
				return nil, err
			}
		}
		inserted = append(inserted, event)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return inserted, nil
}
