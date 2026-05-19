package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// CheckpointAppUsers names the reconcile cursor for the app.users projection
// rebuilt from profile.profiles. Shared by the reconcile job and the health
// check (kept here so neither package needs to import the other).
const CheckpointAppUsers = "app_users_from_profiles"

// SyncCheckpoint records how far a projection reconcile job has progressed,
// so an incremental run resumes from where the last one stopped.
type SyncCheckpoint struct {
	Name          string
	LastSyncedAt  time.Time
	LastSuccessAt *time.Time
	LastError     *string
	UpdatedAt     time.Time
}

// GetCheckpoint returns the named checkpoint, or (nil, nil) when none exists
// yet (a first-ever run — the caller treats it as "sync from epoch").
func (s *Store) GetCheckpoint(ctx context.Context, name string) (*SyncCheckpoint, error) {
	var c SyncCheckpoint
	err := s.db.QueryRow(ctx, `
		SELECT projection_name, last_synced_at, last_success_at, last_error, updated_at
		FROM projection_sync_checkpoint WHERE projection_name = $1`, name).
		Scan(&c.Name, &c.LastSyncedAt, &c.LastSuccessAt, &c.LastError, &c.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// SaveCheckpoint upserts a checkpoint after a reconcile run. On success pass a
// non-zero lastSynced and empty errMsg; on failure pass the previous
// lastSynced (so progress is not lost) and a non-empty errMsg.
func (s *Store) SaveCheckpoint(ctx context.Context, name string, lastSynced time.Time, success bool, errMsg string) error {
	var lastSuccess *time.Time
	var lastErr *string
	if success {
		now := time.Now().UTC()
		lastSuccess = &now
	} else if errMsg != "" {
		lastErr = &errMsg
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO projection_sync_checkpoint
			(projection_name, last_synced_at, last_success_at, last_error, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (projection_name) DO UPDATE SET
			last_synced_at  = EXCLUDED.last_synced_at,
			last_success_at = COALESCE(EXCLUDED.last_success_at, projection_sync_checkpoint.last_success_at),
			last_error      = EXCLUDED.last_error,
			updated_at      = now()
	`, name, lastSynced, lastSuccess, lastErr)
	return err
}
