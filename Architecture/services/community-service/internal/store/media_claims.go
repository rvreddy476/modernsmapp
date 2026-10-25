package store

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/atpost/community-service/internal/claimsync"
	"github.com/atpost/community-service/internal/mediaclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Media claim support (internal/mediaclaims, internal/claimsync).

// LockMediaHolder serialises every write that changes which media one row
// holds (a community's avatar and banner), for as long as the caller holds
// it. The claim protocol claims, writes and releases in three steps; without
// this, two saves of the same row that each read it first can interleave so
// that one's release removes a claim the other's write relies on.
// Transaction-scoped, so a crash or a lost connection frees it. unlock is
// idempotent.
func (s *Store) LockMediaHolder(ctx context.Context, ref uuid.UUID) (unlock func(), err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	if _, err := tx.Exec(ctx, lockMediaHolderSQL, mediaHolderLockKey(ref)); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	done := false
	return func() {
		if !done {
			done = true
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}, nil
}

// lockMediaHolderSQL waits (never try_) for an exclusive, transaction-scoped
// lock. Pinned by TestMediaHolderLockWaitsAndIsTransactionScoped; proven live
// by TestMediaClaimsIT_HolderLockSerialises (CI).
const lockMediaHolderSQL = `SELECT pg_advisory_xact_lock($1)`

// mediaHolderLockKey namespaces the advisory lock so it cannot collide with
// another lock taken on the same id for a different purpose.
func mediaHolderLockKey(ref uuid.UUID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("community-media-holder:"))
	_, _ = h.Write(ref[:])
	return int64(h.Sum64())
}

// CommunityMediaClaimRows pages through every community with an avatar or a
// banner, deleted ones included, in id order after `after`; it returns the
// last community id read (the next cursor), since one community yields up to
// two rows.
func (s *Store) CommunityMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]claimsync.Row, uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, avatar_media_id, banner_media_id, status <> 'deleted'
		FROM communities
		WHERE (avatar_media_id IS NOT NULL OR banner_media_id IS NOT NULL) AND id > $1
		ORDER BY id
		LIMIT $2`, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var out []claimsync.Row
	last := after
	for rows.Next() {
		var id uuid.UUID
		var avatar, banner *uuid.UUID
		var live bool
		if err := rows.Scan(&id, &avatar, &banner, &live); err != nil {
			return nil, after, err
		}
		last = id
		if avatar != nil {
			out = append(out, claimsync.Row{Kind: mediaclient.KindCommunityAvatar, Ref: id, MediaIDs: []uuid.UUID{*avatar}, Live: live})
		}
		if banner != nil {
			out = append(out, claimsync.Row{Kind: mediaclient.KindCommunityBanner, Ref: id, MediaIDs: []uuid.UUID{*banner}, Live: live})
		}
	}
	return out, last, rows.Err()
}

// MediaClaimRowFor re-reads one holder row, for the reconcile tool, under
// LockMediaHolder. A row that no longer exists is not live and holds nothing.
func (s *Store) MediaClaimRowFor(ctx context.Context, kind string, ref uuid.UUID) (claimsync.Row, error) {
	row := claimsync.Row{Kind: kind, Ref: ref}
	col, err := communityMediaColumn(kind)
	if err != nil {
		return row, err
	}
	var id *uuid.UUID
	err = s.db.QueryRow(ctx, `SELECT `+col+`, status <> 'deleted' FROM communities WHERE id = $1`, ref).
		Scan(&id, &row.Live)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimsync.Row{Kind: kind, Ref: ref}, nil
	}
	if id != nil {
		row.MediaIDs = []uuid.UUID{*id}
	}
	return row, err
}

// communityMediaColumn maps a claim kind to its column. Never caller input in
// the SQL: only these two constants can come back.
func communityMediaColumn(kind string) (string, error) {
	switch kind {
	case mediaclient.KindCommunityAvatar:
		return "avatar_media_id", nil
	case mediaclient.KindCommunityBanner:
		return "banner_media_id", nil
	}
	return "", fmt.Errorf("unknown media holder kind %q", kind)
}
