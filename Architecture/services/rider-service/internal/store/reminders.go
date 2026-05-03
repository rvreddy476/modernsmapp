// Document expiry reminder dedupe. The job that walks
// rider_partner_documents + rider_vehicle_documents at 30/14/7/3/1/expired
// thresholds writes one row in rider_doc_reminders_sent per
// (document_id, bucket) so we never double-send. The UNIQUE constraint
// makes the insert side a hard idempotency boundary.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §15 (background jobs — doc expiry).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrReminderAlreadySent is returned when a reminder for the bucket has
// already been recorded for the document. Callers treat this as "skip,
// not an error".
var ErrReminderAlreadySent = errors.New("doc reminder: already sent for bucket")

// DocReminderSent is one row in rider_doc_reminders_sent.
type DocReminderSent struct {
	ID         uuid.UUID `json:"id"`
	PartnerID  uuid.UUID `json:"partner_id"`
	DocumentID uuid.UUID `json:"document_id"`
	ExpiresAt  time.Time `json:"expires_at"`
	Bucket     string    `json:"bucket"`
	SentAt     time.Time `json:"sent_at"`
}

// ExpiringDocument is a denormalized expiring-document row used by the job.
// OwnerKind is "partner" or "vehicle"; for "vehicle" docs the PartnerID is
// derived via the rider_vehicles join in the underlying SQL.
type ExpiringDocument struct {
	DocumentID   uuid.UUID
	PartnerID    uuid.UUID
	OwnerKind    string
	DocumentKind string
	ExpiresAt    time.Time
}

// ListExpiringDocuments returns every partner_document + vehicle_document
// row whose expires_at is within the lookback window or already past.
// The job then computes the per-row bucket label. Bounded at 5000 rows.
func (s *Store) ListExpiringDocuments(ctx context.Context, within time.Duration) ([]ExpiringDocument, error) {
	if within <= 0 {
		within = 30 * 24 * time.Hour
	}
	const q = `
        SELECT id::uuid, partner_id::uuid, 'partner'::text, document_type::text, expires_at
        FROM rider_partner_documents
        WHERE expires_at IS NOT NULL
          AND expires_at <= NOW() + ($1::int * INTERVAL '1 second')
        UNION ALL
        SELECT vd.id::uuid, v.partner_id::uuid, 'vehicle'::text, vd.document_type::text, vd.expires_at
        FROM rider_vehicle_documents vd
        JOIN rider_vehicles v ON v.id = vd.vehicle_id
        WHERE vd.expires_at IS NOT NULL
          AND vd.expires_at <= NOW() + ($1::int * INTERVAL '1 second')
        ORDER BY 5 ASC
        LIMIT 5000`
	rows, err := s.db.Query(ctx, q, int(within.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("list expiring documents: %w", err)
	}
	defer rows.Close()
	var out []ExpiringDocument
	for rows.Next() {
		var d ExpiringDocument
		if err := rows.Scan(&d.DocumentID, &d.PartnerID, &d.OwnerKind, &d.DocumentKind, &d.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MarkReminderSent inserts a (document_id, bucket) row. ON CONFLICT DO
// NOTHING — the unique key makes a duplicate-bucket call a no-op rather
// than an error. Returns true when the row was newly inserted, false
// when it already existed (caller should skip emission).
func (s *Store) MarkReminderSent(ctx context.Context, partnerID, documentID uuid.UUID, expiresAt time.Time, bucket string) (bool, error) {
	if partnerID == uuid.Nil || documentID == uuid.Nil {
		return false, fmt.Errorf("doc reminder: partner_id + document_id required")
	}
	if bucket == "" {
		return false, fmt.Errorf("doc reminder: bucket required")
	}
	const q = `
        INSERT INTO rider_doc_reminders_sent (partner_id, document_id, expires_at, bucket)
        VALUES ($1, $2, $3::date, $4)
        ON CONFLICT (document_id, bucket) DO NOTHING
        RETURNING id`
	var id uuid.UUID
	row := s.db.QueryRow(ctx, q, partnerID, documentID, expiresAt, bucket)
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("mark reminder sent: %w", err)
	}
	return true, nil
}

// HasReminderSent returns true when a reminder row exists for the
// (document_id, bucket) pair. Useful for tests that need to verify the
// dedupe took effect.
func (s *Store) HasReminderSent(ctx context.Context, documentID uuid.UUID, bucket string) (bool, error) {
	const q = `
        SELECT EXISTS (
            SELECT 1 FROM rider_doc_reminders_sent
            WHERE document_id = $1 AND bucket = $2
        )`
	var exists bool
	if err := s.db.QueryRow(ctx, q, documentID, bucket).Scan(&exists); err != nil {
		return false, fmt.Errorf("has reminder sent: %w", err)
	}
	return exists, nil
}
