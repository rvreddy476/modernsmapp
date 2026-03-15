package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// NotificationDigest is a weekly or monthly summary of notifications for a user.
type NotificationDigest struct {
	ID          uuid.UUID       `json:"id"`
	UserID      uuid.UUID       `json:"user_id"`
	PeriodType  string          `json:"period_type"`
	PeriodStart time.Time       `json:"period_start"`
	Content     json.RawMessage `json:"content"`
	SentAt      *time.Time      `json:"sent_at,omitempty"`
}

// NotificationBundle groups similar notifications (e.g. multiple likes on one post)
// to reduce noise. Sent once a threshold or TTL is reached.
type NotificationBundle struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	BundleType    string     `json:"bundle_type"`
	Count         int        `json:"count"`
	ActorIDs      []uuid.UUID `json:"actor_ids"`
	RefID         *uuid.UUID `json:"ref_id,omitempty"`
	LastUpdatedAt time.Time  `json:"last_updated_at"`
	SentAt        *time.Time `json:"sent_at,omitempty"`
}

// UpsertBundle creates or updates a notification bundle for the given user.
// Because ref_id can be NULL (not usable in a standard ON CONFLICT clause without a
// partial unique index), this uses a SELECT-then-INSERT/UPDATE approach.
func (s *Store) UpsertBundle(ctx context.Context, userID uuid.UUID, bundleType string, refID *uuid.UUID, actorID uuid.UUID) (*NotificationBundle, error) {
	// Try to find an existing unsent bundle.
	var existing NotificationBundle
	var actorIDsRaw []byte

	var row pgx.Row
	if refID != nil {
		row = s.db.QueryRow(ctx, `
			SELECT id, user_id, bundle_type, count, actor_ids, ref_id, last_updated_at, sent_at
			FROM notification_bundles
			WHERE user_id = $1 AND bundle_type = $2 AND ref_id = $3 AND sent_at IS NULL
			LIMIT 1
		`, userID, bundleType, refID)
	} else {
		row = s.db.QueryRow(ctx, `
			SELECT id, user_id, bundle_type, count, actor_ids, ref_id, last_updated_at, sent_at
			FROM notification_bundles
			WHERE user_id = $1 AND bundle_type = $2 AND ref_id IS NULL AND sent_at IS NULL
			LIMIT 1
		`, userID, bundleType)
	}

	err := row.Scan(
		&existing.ID, &existing.UserID, &existing.BundleType, &existing.Count,
		&actorIDsRaw, &existing.RefID, &existing.LastUpdatedAt, &existing.SentAt,
	)

	if err != nil && err != pgx.ErrNoRows {
		return nil, err
	}

	if err == pgx.ErrNoRows {
		// Insert new bundle.
		existing = NotificationBundle{
			ID:            uuid.New(),
			UserID:        userID,
			BundleType:    bundleType,
			Count:         1,
			ActorIDs:      []uuid.UUID{actorID},
			RefID:         refID,
			LastUpdatedAt: time.Now().UTC(),
		}
		actorArr, _ := json.Marshal(existing.ActorIDs)
		_, err = s.db.Exec(ctx, `
			INSERT INTO notification_bundles (id, user_id, bundle_type, count, actor_ids, ref_id, last_updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, existing.ID, existing.UserID, existing.BundleType, existing.Count,
			actorArr, existing.RefID, existing.LastUpdatedAt)
		if err != nil {
			return nil, err
		}
		return &existing, nil
	}

	// Unmarshal existing actor_ids and append new actorID.
	var actors []uuid.UUID
	if len(actorIDsRaw) > 0 {
		_ = json.Unmarshal(actorIDsRaw, &actors)
	}
	// Append only if not already present.
	found := false
	for _, a := range actors {
		if a == actorID {
			found = true
			break
		}
	}
	if !found {
		actors = append(actors, actorID)
	}
	newActorArr, _ := json.Marshal(actors)
	now := time.Now().UTC()

	_, err = s.db.Exec(ctx, `
		UPDATE notification_bundles
		SET count = count + 1, actor_ids = $1, last_updated_at = $2
		WHERE id = $3
	`, newActorArr, now, existing.ID)
	if err != nil {
		return nil, err
	}

	existing.Count++
	existing.ActorIDs = actors
	existing.LastUpdatedAt = now
	return &existing, nil
}

// GetPendingBundles returns unsent bundles that are either older than 30 minutes
// or have reached the given count threshold.
func (s *Store) GetPendingBundles(ctx context.Context, threshold int) ([]NotificationBundle, error) {
	cutoff := time.Now().UTC().Add(-30 * time.Minute)
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, bundle_type, count, actor_ids, ref_id, last_updated_at, sent_at
		FROM notification_bundles
		WHERE sent_at IS NULL
		  AND (last_updated_at <= $1 OR count >= $2)
		ORDER BY last_updated_at ASC
	`, cutoff, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bundles []NotificationBundle
	for rows.Next() {
		var b NotificationBundle
		var actorIDsRaw []byte
		if err := rows.Scan(
			&b.ID, &b.UserID, &b.BundleType, &b.Count,
			&actorIDsRaw, &b.RefID, &b.LastUpdatedAt, &b.SentAt,
		); err != nil {
			return nil, err
		}
		if len(actorIDsRaw) > 0 {
			_ = json.Unmarshal(actorIDsRaw, &b.ActorIDs)
		}
		bundles = append(bundles, b)
	}
	return bundles, rows.Err()
}

// MarkBundleSent sets sent_at = NOW() on the given bundle.
func (s *Store) MarkBundleSent(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE notification_bundles SET sent_at = NOW() WHERE id = $1
	`, id)
	return err
}

// CreateDigest inserts a new notification digest record.
func (s *Store) CreateDigest(ctx context.Context, d *NotificationDigest) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO notification_digests (id, user_id, period_type, period_start, content, sent_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, period_type, period_start) DO UPDATE
			SET content = $5, sent_at = $6
	`, d.ID, d.UserID, d.PeriodType, d.PeriodStart, d.Content, d.SentAt)
	return err
}

// GetDigests returns recent digests for a user, newest first.
func (s *Store) GetDigests(ctx context.Context, userID uuid.UUID, limit int) ([]NotificationDigest, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, period_type, period_start, content, sent_at
		FROM notification_digests
		WHERE user_id = $1
		ORDER BY period_start DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var digests []NotificationDigest
	for rows.Next() {
		var d NotificationDigest
		if err := rows.Scan(&d.ID, &d.UserID, &d.PeriodType, &d.PeriodStart, &d.Content, &d.SentAt); err != nil {
			return nil, err
		}
		digests = append(digests, d)
	}
	return digests, rows.Err()
}
