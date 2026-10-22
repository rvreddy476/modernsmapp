package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FieldKeyRingStore persists the KMS-wrapped master keys of the field
// encryption ring in chat.field_key_ring (migration 016). It implements
// fieldcrypt.WrappedKeyStore. Only wrapped keys are stored; the plaintext key
// exists in process memory only.
type FieldKeyRingStore struct {
	db pgDB
}

var _ fieldcrypt.WrappedKeyStore = (*FieldKeyRingStore)(nil)

// NewFieldKeyRingStore builds the store over the chat pool.
func NewFieldKeyRingStore(db *pgxpool.Pool) *FieldKeyRingStore {
	return &FieldKeyRingStore{db: db}
}

// ListWrappedKeys returns every version, retired or not: a retired key still
// opens everything sealed while it was active.
func (s *FieldKeyRingStore) ListWrappedKeys(ctx context.Context) ([]fieldcrypt.WrappedKey, error) {
	rows, err := s.db.Query(ctx, `
		SELECT version, wrapped_key, kms_key_id, encryption_context
		FROM chat.field_key_ring ORDER BY version
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fieldcrypt.WrappedKey
	for rows.Next() {
		var (
			version int64
			k       fieldcrypt.WrappedKey
			rawCtx  []byte
		)
		if err := rows.Scan(&version, &k.Wrapped, &k.KMSKeyID, &rawCtx); err != nil {
			return nil, err
		}
		if version <= 0 || version > int64(^uint32(0)) {
			return nil, fmt.Errorf("field_key_ring: version %d is out of range", version)
		}
		k.Version = uint32(version)
		if err := json.Unmarshal(rawCtx, &k.Context); err != nil {
			return nil, fmt.Errorf("field_key_ring: version %d: unreadable encryption context", version)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// CreateFirstWrappedKey installs version 1 only when the ring is empty; a
// concurrent first boot that loses gets fieldcrypt.ErrActiveKeyExists and
// adopts the winner's key.
func (s *FieldKeyRingStore) CreateFirstWrappedKey(ctx context.Context, k fieldcrypt.WrappedKey) error {
	rawCtx, err := json.Marshal(k.Context)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO chat.field_key_ring (version, wrapped_key, kms_key_id, encryption_context, active)
		SELECT 1, $1, $2, $3, TRUE
		WHERE NOT EXISTS (SELECT 1 FROM chat.field_key_ring)
		ON CONFLICT DO NOTHING
	`, k.Wrapped, k.KMSKeyID, rawCtx)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("field_key_ring: %w", fieldcrypt.ErrActiveKeyExists)
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("field_key_ring: %w", fieldcrypt.ErrActiveKeyExists)
	}
	return nil
}

// RotateWrappedKey retires the active version and installs the next one as
// active, in one transaction under a table lock so two rotations cannot pick
// the same number.
func (s *FieldKeyRingStore) RotateWrappedKey(ctx context.Context, k fieldcrypt.WrappedKey) (uint32, error) {
	rawCtx, err := json.Marshal(k.Context)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE chat.field_key_ring IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return 0, err
	}
	var next int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM chat.field_key_ring`).Scan(&next); err != nil {
		return 0, err
	}
	if next > int64(^uint32(0)) {
		return 0, errors.New("field_key_ring: version space exhausted")
	}
	if _, err := tx.Exec(ctx, `UPDATE chat.field_key_ring SET active = FALSE, retired_at = NOW() WHERE active`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO chat.field_key_ring (version, wrapped_key, kms_key_id, encryption_context, active)
		VALUES ($1, $2, $3, $4, TRUE)
	`, next, k.Wrapped, k.KMSKeyID, rawCtx); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return uint32(next), nil
}
