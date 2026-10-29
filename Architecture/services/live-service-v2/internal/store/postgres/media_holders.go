package postgres

// The store side of the cover claim protocol (service/media_claims.go) and
// of its reconcile tool (internal/claimsync, cmd/media-claims).

import (
	"context"
	"errors"
	"hash/fnv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// optionalID is NULL for uuid.Nil, so the column default applies.
func optionalID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// LockMediaHolder serialises every write that changes which cover one stream
// holds, for as long as the caller holds it. The claim protocol claims,
// writes and releases in three steps; without this, two edits of the same
// stream that each read it first can interleave so that one's release
// removes a claim the other's write relies on. Transaction-scoped, so a
// crash or a lost connection frees it. unlock is idempotent.
//
// Callers pass the in-process holderGate before taking a connection, so lock
// holders can never exhaust the pool (media_holder_gate.go).
func (s *Store) LockMediaHolder(ctx context.Context, streamID uuid.UUID) (unlock func(), err error) {
	return lockMediaHolder(ctx, s.holders, s.db, mediaHolderLockKey(streamID))
}

// lockMediaHolderSQL waits (never try_) for an exclusive, transaction-scoped
// lock. Pinned by TestMediaHolderLockWaitsAndIsTransactionScoped; proven live
// by TestIntegrationMediaHolderLockSerialises (CI).
const lockMediaHolderSQL = `SELECT pg_advisory_xact_lock($1)`

// mediaHolderLockKey namespaces the advisory lock so it cannot collide with
// another lock taken on the same id for a different purpose.
func mediaHolderLockKey(ref uuid.UUID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("live-cover-holder:"))
	_, _ = h.Write(ref[:])
	return int64(h.Sum64())
}

// StreamIDsWithCover lists the creator's streams that name a cover (the
// purge releases their claims before it erases them).
func (s *Store) StreamIDsWithCover(ctx context.Context, creatorID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id FROM live_streams WHERE creator_user_id = $1 AND cover_media_id IS NOT NULL ORDER BY id`, creatorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CoverClaimRow is one stream and the cover it names (nil: none, or the row
// is gone), for the reconcile tool.
type CoverClaimRow struct {
	StreamID uuid.UUID
	Cover    *uuid.UUID
}

// CoverClaimRows pages through every stream that names a cover (with all,
// every stream: one whose cover was cleared may still hold a stale claim), in
// id order after `after`. Every status counts: an ended stream's page and
// recording still show the cover.
func (s *Store) CoverClaimRows(ctx context.Context, after uuid.UUID, limit int, all bool) ([]CoverClaimRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, cover_media_id FROM live_streams
		WHERE ($3::boolean OR cover_media_id IS NOT NULL) AND id > $1
		ORDER BY id LIMIT $2`, after, limit, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CoverClaimRow
	for rows.Next() {
		var r CoverClaimRow
		if err := rows.Scan(&r.StreamID, &r.Cover); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CoverClaimRowFor re-reads one stream's cover, under LockMediaHolder. A
// stream that no longer exists holds nothing.
func (s *Store) CoverClaimRowFor(ctx context.Context, streamID uuid.UUID) (CoverClaimRow, error) {
	r := CoverClaimRow{StreamID: streamID}
	err := s.db.QueryRow(ctx, `SELECT cover_media_id FROM live_streams WHERE id = $1`, streamID).Scan(&r.Cover)
	if errors.Is(err, pgx.ErrNoRows) {
		return CoverClaimRow{StreamID: streamID}, nil
	}
	return r, err
}

// ImportHoldsSQL is the media id a live_recording_imports row holds a claim
// on (service/recording_claims.go): its media_id while the job is pending,
// or done with the post lookup still pending; NULL otherwise.
const ImportHoldsSQL = `(CASE WHEN state = 'pending' OR (state = 'done' AND post_lookup_state = 'pending') THEN media_id END)`

// StreamIDsWithImportMedia lists the creator's streams whose recording import
// names a media id, held or not (the purge releases their claims before it
// erases them; releasing is idempotent).
func (s *Store) StreamIDsWithImportMedia(ctx context.Context, creatorID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT i.stream_id FROM live_recording_imports i
		JOIN live_streams st ON st.id = i.stream_id
		WHERE st.creator_user_id = $1 AND i.media_id IS NOT NULL
		ORDER BY i.stream_id`, creatorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ImportClaimRow is one stream's recording import and the media id it holds
// (nil: none, the job no longer relies on it, or the row is gone).
type ImportClaimRow struct {
	StreamID uuid.UUID
	Held     *uuid.UUID
}

// ImportClaimRows pages through every import that names a media id (with
// all, every import), in stream id order after `after`. A row that names an
// id it no longer holds is listed too: its claim is released.
func (s *Store) ImportClaimRows(ctx context.Context, after uuid.UUID, limit int, all bool) ([]ImportClaimRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT stream_id, `+ImportHoldsSQL+` FROM live_recording_imports
		WHERE ($3::boolean OR media_id IS NOT NULL) AND stream_id > $1
		ORDER BY stream_id LIMIT $2`, after, limit, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImportClaimRow
	for rows.Next() {
		var r ImportClaimRow
		if err := rows.Scan(&r.StreamID, &r.Held); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ImportClaimRowFor re-reads one stream's import, under LockMediaHolder. An
// import that no longer exists holds nothing.
func (s *Store) ImportClaimRowFor(ctx context.Context, streamID uuid.UUID) (ImportClaimRow, error) {
	r := ImportClaimRow{StreamID: streamID}
	err := s.db.QueryRow(ctx, `SELECT `+ImportHoldsSQL+` FROM live_recording_imports WHERE stream_id = $1`, streamID).Scan(&r.Held)
	if errors.Is(err, pgx.ErrNoRows) {
		return ImportClaimRow{StreamID: streamID}, nil
	}
	return r, err
}
