package postgres

// The video a recording became (2 Oct 2026; migration 006). The lookup's
// state lives on the stream's import job; see the migration for the columns.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Post lookup states.
const (
	PostLookupPending = "pending"
	PostLookupFound   = "found"
	PostLookupDeleted = "deleted"
	PostLookupGaveUp  = "gave_up"
)

// PostLookup is one stream whose recording post is still unknown.
type PostLookup struct {
	StreamID uuid.UUID
	// Attempts is how many sweeper lookups did not settle it.
	Attempts int
	// Age is how long ago the import finished (database clock).
	Age time.Duration
}

const postLookupColumns = `stream_id, post_lookup_attempts,
		          EXTRACT(EPOCH FROM (NOW() - COALESCE(done_at, created_at)))::float8`

func scanPostLookup(row pgx.Row) (PostLookup, error) {
	var l PostLookup
	var ageSec float64
	err := row.Scan(&l.StreamID, &l.Attempts, &ageSec)
	l.Age = time.Duration(ageSec * float64(time.Second))
	return l, err
}

// ClaimDuePostLookups leases up to limit due lookups for lease (pushing
// post_lookup_next_at forward), so concurrent sweepers do not ask for the
// same stream at once. Only imports that reached 'done' are asked about:
// nothing else announced a recording.
func (s *Store) ClaimDuePostLookups(ctx context.Context, limit int, lease time.Duration) ([]PostLookup, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE live_recording_imports
		SET post_lookup_next_at = NOW() + make_interval(secs => $2), post_lookup_last_at = NOW()
		WHERE stream_id IN (
		    SELECT stream_id FROM live_recording_imports
		    WHERE state = 'done' AND post_lookup_state = 'pending' AND post_lookup_next_at <= NOW()
		    ORDER BY post_lookup_next_at
		    LIMIT $1
		    FOR UPDATE SKIP LOCKED)
		RETURNING `+postLookupColumns, limit, lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PostLookup
	for rows.Next() {
		l, err := scanPostLookup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ClaimPostLookup is the stream detail read's claim on one stream: granted
// (ok) only while the lookup is pending and nobody asked within minGap. It
// leaves the sweeper's schedule alone.
func (s *Store) ClaimPostLookup(ctx context.Context, streamID uuid.UUID, minGap time.Duration) (PostLookup, bool, error) {
	l, err := scanPostLookup(s.db.QueryRow(ctx, `
		UPDATE live_recording_imports
		SET post_lookup_last_at = NOW()
		WHERE stream_id = $1 AND state = 'done' AND post_lookup_state = 'pending'
		  AND (post_lookup_last_at IS NULL OR post_lookup_last_at <= NOW() - make_interval(secs => $2))
		RETURNING `+postLookupColumns, streamID, minGap.Seconds()))
	if errors.Is(err, pgx.ErrNoRows) {
		return PostLookup{}, false, nil
	}
	if err != nil {
		return PostLookup{}, false, err
	}
	return l, true, nil
}

// SetRecordingPost stores the recording's post id and settles the lookup,
// in one transaction. Two guards: the lookup must still be pending (a lookup
// already settled — found, or stopped because the post was deleted — is not
// reopened), and recording_post_id is written only while it is NULL (an id
// already stored is never overwritten). stored reports whether THIS call
// wrote the id.
func (s *Store) SetRecordingPost(ctx context.Context, streamID, postID uuid.UUID) (stored bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE live_recording_imports
		SET post_lookup_state = 'found', post_lookup_last_at = NOW()
		WHERE stream_id = $1 AND state = 'done' AND post_lookup_state = 'pending'`, streamID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	tag, err = tx.Exec(ctx, `
		UPDATE live_streams SET recording_post_id = $2
		WHERE id = $1 AND recording_post_id IS NULL`, streamID, postID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// RetryPostLookup records a sweeper lookup that did not settle (no post
// yet, or an error) and schedules the next one.
func (s *Store) RetryPostLookup(ctx context.Context, streamID uuid.UUID, retryIn time.Duration) error {
	_, err := s.db.Exec(ctx, `
		UPDATE live_recording_imports
		SET post_lookup_attempts = post_lookup_attempts + 1,
		    post_lookup_next_at = NOW() + make_interval(secs => $2)
		WHERE stream_id = $1 AND post_lookup_state = 'pending'`, streamID, retryIn.Seconds())
	return err
}

// StopPostLookup ends a lookup without a post: state is PostLookupGaveUp
// (a pending lookup only) or PostLookupDeleted (the post was deleted: a
// pending lookup, or one another replica settled as found a moment ago; the
// id stored meanwhile is cleared, so it is not offered).
func (s *Store) StopPostLookup(ctx context.Context, streamID uuid.UUID, state string) error {
	if state != PostLookupDeleted && state != PostLookupGaveUp {
		return errors.New("stop post lookup: state must be deleted or gave_up")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE live_recording_imports
		SET post_lookup_state = $2, post_lookup_last_at = NOW()
		WHERE stream_id = $1
		  AND (post_lookup_state = 'pending' OR ($2 = 'deleted' AND post_lookup_state = 'found'))`, streamID, state); err != nil {
		return err
	}
	if state == PostLookupDeleted {
		if _, err := tx.Exec(ctx, `UPDATE live_streams SET recording_post_id = NULL WHERE id = $1`, streamID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// PostLookupState reads one stream's lookup state and attempts (tests, ops).
func (s *Store) PostLookupState(ctx context.Context, streamID uuid.UUID) (state string, attempts int, err error) {
	err = s.db.QueryRow(ctx, `SELECT post_lookup_state, post_lookup_attempts FROM live_recording_imports WHERE stream_id = $1`, streamID).
		Scan(&state, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, ErrNotFound
	}
	return state, attempts, err
}
