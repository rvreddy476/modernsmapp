package postgres

import (
	"context"

	"github.com/google/uuid"
)

// ClaimEventDedup records id in notify_meta.event_dedup (setup.sql) and
// reports whether THIS call inserted it — true exactly once per id, however
// often the same logical delivery is retried or redelivered.
//
// Callers derive id from a stable identity (a UUIDv5 of event + recipient),
// so the ledger holds no user id in the clear and needs no purge handling.
func (s *Store) ClaimEventDedup(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO notify_meta.event_dedup (event_id, processed_at)
		VALUES ($1, NOW())
		ON CONFLICT (event_id) DO NOTHING`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseEventDedup removes a claim whose delivery did not happen, so a
// redelivery can claim and try again (the copyright email: a send that
// failed after its claim). A missing row is not an error.
func (s *Store) ReleaseEventDedup(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM notify_meta.event_dedup WHERE event_id = $1`, id)
	return err
}
