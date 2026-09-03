package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Account control: hide / purge (acks as "live-v2") ───────────────────────

// SetUserHidden ends every stream the user is currently broadcasting when
// hidden=true (mirrors MarkEnded's columns); hidden=false is a no-op because
// an ended stream is not resumable. Idempotent.
func (s *Store) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, _ string) error {
	if !hidden {
		return nil
	}
	_, err := s.db.Exec(ctx, endLiveSQL, userID)
	return err
}

const endLiveSQL = `
	UPDATE live_streams
	SET status = 'ended', ended_at = COALESCE(ended_at, NOW()), updated_at = NOW()
	WHERE creator_user_id = $1 AND status = 'live'`

// PurgeUser ends the user's live streams and erases every row keyed by the
// user in ONE transaction: their chat messages, mutes and viewer events
// anywhere, and the streams they created (children cascade; deleted
// explicitly anyway so the purge does not depend on FK clauses). Idempotent.
func (s *Store) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const mine = `SELECT id FROM live_streams WHERE creator_user_id = $1`
	steps := []struct{ table, sql string }{
		{"live_streams", endLiveSQL},
		{"live_chat_messages", `DELETE FROM live_chat_messages WHERE user_id = $1 OR stream_id IN (` + mine + `)`},
		{"live_chat_mutes", `DELETE FROM live_chat_mutes WHERE user_id = $1 OR stream_id IN (` + mine + `)`},
		{"live_chat_word_filters", `DELETE FROM live_chat_word_filters WHERE stream_id IN (` + mine + `)`},
		{"live_viewer_events", `DELETE FROM live_viewer_events WHERE user_id = $1 OR stream_id IN (` + mine + `)`},
		{"live_streams", `DELETE FROM live_streams WHERE creator_user_id = $1`},
	}
	present := map[string]bool{}
	for _, st := range steps {
		ok, known := present[st.table]
		if !known {
			ok, err = tableExists(ctx, tx, st.table)
			if err != nil {
				return fmt.Errorf("purge: probe %s: %w", st.table, err)
			}
			present[st.table] = ok
		}
		if !ok {
			continue
		}
		if _, err := tx.Exec(ctx, st.sql, userID); err != nil {
			return fmt.Errorf("purge: %s: %w", st.table, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("purge: commit: %w", err)
	}
	return nil
}

func tableExists(ctx context.Context, tx pgx.Tx, table string) (bool, error) {
	var oid *uint32
	if err := tx.QueryRow(ctx, `SELECT to_regclass($1)::oid`, "public."+table).Scan(&oid); err != nil {
		return false, err
	}
	return oid != nil, nil
}
