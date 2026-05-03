// Sparks store — spec §10 dating_sparks. A Spark is a typed, targeted
// interest signal aimed at one item on the recipient's profile (photo,
// prompt, tune-axis, or echo). Targeted-only per DECISIONS.md D2.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Spark is one row in dating_sparks.
type Spark struct {
	ID         uuid.UUID `json:"id"`
	FromUserID uuid.UUID `json:"from_user_id"`
	ToUserID   uuid.UUID `json:"to_user_id"`
	TargetKind string    `json:"target_kind"`
	TargetRef  string    `json:"target_ref"`
	Note       *string   `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// ErrSparkNotFound is returned when a spark id does not exist.
var ErrSparkNotFound = errors.New("not_found: spark not found")

const sparkSelectCols = `id, from_user_id, to_user_id, target_kind, target_ref, note, created_at`

func scanSpark(row pgx.Row) (*Spark, error) {
	s := &Spark{}
	if err := row.Scan(&s.ID, &s.FromUserID, &s.ToUserID, &s.TargetKind, &s.TargetRef, &s.Note, &s.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSparkNotFound
		}
		return nil, fmt.Errorf("scan spark: %w", err)
	}
	return s, nil
}

// CreateSpark inserts a new dating_sparks row. The UNIQUE constraint
// (from_user_id, to_user_id, target_kind, target_ref) guarantees idempotency:
// if a duplicate is attempted we fall through to the existing row.
func (s *Store) CreateSpark(ctx context.Context, fromUserID, toUserID uuid.UUID, targetKind, targetRef, note string) (*Spark, error) {
	if fromUserID == uuid.Nil || toUserID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user ids required")
	}
	if fromUserID == toUserID {
		return nil, fmt.Errorf("invalid: cannot spark yourself")
	}
	if targetKind == "" || targetRef == "" {
		return nil, fmt.Errorf("invalid: target_kind and target_ref required")
	}
	switch targetKind {
	case "photo", "prompt", "tune_axis", "echo":
	default:
		return nil, fmt.Errorf("invalid: unsupported target_kind %q", targetKind)
	}

	var notePtr *string
	if note != "" {
		notePtr = &note
	}

	row := s.db.QueryRow(ctx, `
        INSERT INTO dating_sparks (from_user_id, to_user_id, target_kind, target_ref, note)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (from_user_id, to_user_id, target_kind, target_ref) DO UPDATE
            SET note = COALESCE(EXCLUDED.note, dating_sparks.note)
        RETURNING `+sparkSelectCols, fromUserID, toUserID, targetKind, targetRef, notePtr)
	return scanSpark(row)
}

// GetSpark returns a single spark by id.
func (s *Store) GetSpark(ctx context.Context, id uuid.UUID) (*Spark, error) {
	row := s.db.QueryRow(ctx, `SELECT `+sparkSelectCols+` FROM dating_sparks WHERE id = $1`, id)
	return scanSpark(row)
}

// ListIncomingSparks returns sparks aimed at userID (ordered newest first).
func (s *Store) ListIncomingSparks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Spark, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(ctx, `
        SELECT `+sparkSelectCols+`
        FROM dating_sparks
        WHERE to_user_id = $1
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list incoming sparks: %w", err)
	}
	defer rows.Close()

	out := make([]*Spark, 0, limit)
	for rows.Next() {
		sp, err := scanSpark(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// ListSparksSent returns sparks sent BY userID (newest first). Used by the
// DPDP data exporter.
func (s *Store) ListSparksSent(ctx context.Context, userID uuid.UUID) ([]*Spark, error) {
	rows, err := s.db.Query(ctx, `
        SELECT `+sparkSelectCols+`
        FROM dating_sparks
        WHERE from_user_id = $1
        ORDER BY created_at DESC
        LIMIT 500`, userID)
	if err != nil {
		return nil, fmt.Errorf("list sparks sent: %w", err)
	}
	defer rows.Close()
	out := make([]*Spark, 0, 16)
	for rows.Next() {
		sp, err := scanSpark(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// ListSparksReceived is an alias around ListIncomingSparks with a default
// page size — kept distinct so the exporter's intent is obvious.
func (s *Store) ListSparksReceived(ctx context.Context, userID uuid.UUID) ([]*Spark, error) {
	return s.ListIncomingSparks(ctx, userID, 500, 0)
}

// DeleteSpark removes a spark only when fromUserID matches the owner.
// Returns ErrSparkNotFound when no row matches.
func (s *Store) DeleteSpark(ctx context.Context, id, fromUserID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
        DELETE FROM dating_sparks
        WHERE id = $1 AND from_user_id = $2`, id, fromUserID)
	if err != nil {
		return fmt.Errorf("delete spark: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSparkNotFound
	}
	return nil
}

// HasReverseSparks reports true if user b has previously Sparked user a.
// Used by the spark service to detect mutual interest and form a match.
func (s *Store) HasReverseSparks(ctx context.Context, a, b uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM dating_sparks
            WHERE from_user_id = $1 AND to_user_id = $2
        )`, b, a).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("has reverse sparks: %w", err)
	}
	return exists, nil
}
