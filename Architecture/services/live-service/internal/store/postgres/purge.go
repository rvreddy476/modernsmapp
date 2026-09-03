package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Account control: hide / purge (acks as "live") ──────────────────────────

// SetUserHidden ends every stream and audio room the user hosts that is
// still live when hidden=true; a stream is not resumable, so hidden=false is
// a no-op. Idempotent.
func (s *Store) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, _ string) error {
	if !hidden {
		return nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("hide: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := endHostedTx(ctx, tx, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// endHostedTx ends live streams / audio rooms hosted by the user.
func endHostedTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	stmts := []string{
		`UPDATE live.viewer_sessions SET left_at = NOW()
		   WHERE left_at IS NULL AND stream_id IN (SELECT id FROM live.streams WHERE host_id = $1 AND status = 'live')`,
		`UPDATE live.streams SET status = 'ended', ended_at = NOW(),
		        duration_secs = EXTRACT(EPOCH FROM (NOW() - COALESCE(started_at, NOW())))::int, updated_at = NOW()
		   WHERE host_id = $1 AND status = 'live'`,
	}
	for _, st := range stmts {
		if _, err := tx.Exec(ctx, st, userID); err != nil {
			return fmt.Errorf("end streams: %w", err)
		}
	}
	ok, err := tableExists(ctx, tx, "audio_rooms")
	if err != nil {
		return err
	}
	if ok {
		if _, err := tx.Exec(ctx, `UPDATE audio_rooms SET status = 'ended', ended_at = COALESCE(ended_at, NOW())
			WHERE host_id = $1 AND status <> 'ended'`, userID); err != nil {
			return fmt.Errorf("end audio rooms: %w", err)
		}
	}
	return nil
}

// PurgeUser ends anything the user still hosts and erases every live row
// keyed by the user in ONE transaction: their chat messages, viewer
// sessions, gifts, mutes, guest slots, poll votes and audio-room
// memberships; the streams, scheduled streams and audio rooms they host with
// all dependents. Optional tables are probed with to_regclass. Idempotent.
func (s *Store) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := endHostedTx(ctx, tx, userID); err != nil {
		return err
	}
	const hosted = `SELECT id FROM live.streams WHERE host_id = $1`
	steps := []struct{ table, sql string }{
		{"live.chat_messages", `DELETE FROM live.chat_messages WHERE user_id = $1 OR stream_id IN (` + hosted + `)`},
		{"live.viewer_sessions", `DELETE FROM live.viewer_sessions WHERE user_id = $1 OR stream_id IN (` + hosted + `)`},
		{"live_gifts", `DELETE FROM live_gifts WHERE sender_id = $1 OR stream_id IN (` + hosted + `)`},
		{"live_mutes", `DELETE FROM live_mutes WHERE user_id = $1 OR stream_id IN (` + hosted + `)`},
		{"live_guests", `DELETE FROM live_guests WHERE user_id = $1 OR stream_id IN (` + hosted + `)`},
		{"live_poll_votes", `DELETE FROM live_poll_votes WHERE user_id = $1 OR poll_id IN (SELECT id FROM live_polls WHERE stream_id IN (` + hosted + `))`},
		{"live_polls", `DELETE FROM live_polls WHERE stream_id IN (` + hosted + `)`},
		{"live_word_filters", `DELETE FROM live_word_filters WHERE stream_id IN (` + hosted + `)`},
		{"live_dvr_segments", `DELETE FROM live_dvr_segments WHERE stream_id IN (` + hosted + `)`},
		{"live.scheduled_streams", `DELETE FROM live.scheduled_streams WHERE host_id = $1 OR stream_id IN (` + hosted + `)`},
		{"live.streams", `DELETE FROM live.streams WHERE host_id = $1`},
		{"audio_room_members", `DELETE FROM audio_room_members WHERE user_id = $1 OR room_id IN (SELECT id FROM audio_rooms WHERE host_id = $1)`},
		{"audio_room_recordings", `DELETE FROM audio_room_recordings WHERE room_id IN (SELECT id FROM audio_rooms WHERE host_id = $1)`},
		{"audio_rooms", `DELETE FROM audio_rooms WHERE host_id = $1`},
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
	if err := tx.QueryRow(ctx, `SELECT to_regclass($1)::oid`, table).Scan(&oid); err != nil {
		return false, err
	}
	return oid != nil, nil
}
