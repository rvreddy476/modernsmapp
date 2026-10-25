package store

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/atpost/identity-profile-service/internal/mediaclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Media claims (media-service media_claims, migration 031) ───────────────
//
// profile.profiles.avatar_media_id / cover_media_id name media-service assets.
// The claim protocol that keeps the owner from deleting them out from under
// the profile is internal/service/media_claims.go; these are its reads, its
// write and its per-profile lock. The ref media-service records is the
// profile's user id, for both kinds.

// profileMediaColumn maps a claim kind to the column that holds it. A
// constant table, never caller text: the column is spliced into SQL.
func profileMediaColumn(kind string) (string, error) {
	switch kind {
	case mediaclient.KindProfileAvatar:
		return "avatar_media_id", nil
	case mediaclient.KindProfileCover:
		return "cover_media_id", nil
	}
	return "", fmt.Errorf("unknown profile media kind %q", kind)
}

// GetProfileMediaID reads the media id the profile holds for kind. found is
// false when there is no profile row.
func (s *Store) GetProfileMediaID(ctx context.Context, userID uuid.UUID, kind string) (id *uuid.UUID, found bool, err error) {
	col, err := profileMediaColumn(kind)
	if err != nil {
		return nil, false, err
	}
	err = s.db.QueryRow(ctx, `SELECT `+col+` FROM profile.profiles WHERE user_id = $1`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return id, true, nil
}

// SetProfileMediaID writes the profile's media id for kind. found is false
// when there is no profile row (nothing was written).
func (s *Store) SetProfileMediaID(ctx context.Context, userID uuid.UUID, kind string, mediaID uuid.UUID) (found bool, err error) {
	col, err := profileMediaColumn(kind)
	if err != nil {
		return false, err
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE profile.profiles SET `+col+` = $1, updated_at = NOW() WHERE user_id = $2`, mediaID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// LockMediaHolder serialises every write that changes which media one profile
// holds, for as long as the caller holds it. The claim protocol reads the
// current id, claims the new one, writes and releases the old one in four
// steps; without this, two saves of the same profile can interleave so that
// one's release removes the claim the other's write relies on. Transaction-
// scoped, so a crash or a lost connection frees it. unlock is idempotent.
func (s *Store) LockMediaHolder(ctx context.Context, userID uuid.UUID) (unlock func(), err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	if _, err := tx.Exec(ctx, lockMediaHolderSQL, mediaHolderLockKey(userID)); err != nil {
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
// by TestProfileMediaClaimsIT_HolderLockSerialises (CI).
const lockMediaHolderSQL = `SELECT pg_advisory_xact_lock($1)`

// mediaHolderLockKey namespaces the advisory lock so it cannot collide with
// another lock taken on the same id for a different purpose.
func mediaHolderLockKey(userID uuid.UUID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("profile-media-holder:"))
	_, _ = h.Write(userID[:])
	return int64(h.Sum64())
}

// MediaClaimRow is one profile column that holds (or held) media, for the
// reconcile tool (cmd/media-claims). Live is false when the profile row is
// gone: its claims should be released.
type MediaClaimRow struct {
	Kind     string // mediaclient.KindProfile*
	Ref      uuid.UUID
	MediaIDs []uuid.UUID
	Live     bool
}

// ProfileMediaClaimRows pages through every profile with an avatar or cover,
// in user id order after `after`. The second value is the last user id read
// (the next cursor), since one profile yields up to two rows.
func (s *Store) ProfileMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]MediaClaimRow, uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, avatar_media_id, cover_media_id
		FROM profile.profiles
		WHERE (avatar_media_id IS NOT NULL OR cover_media_id IS NOT NULL) AND user_id > $1
		ORDER BY user_id
		LIMIT $2`, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var out []MediaClaimRow
	last := after
	for rows.Next() {
		var id uuid.UUID
		var avatar, cover *uuid.UUID
		if err := rows.Scan(&id, &avatar, &cover); err != nil {
			return nil, after, err
		}
		last = id
		if avatar != nil {
			out = append(out, MediaClaimRow{Kind: mediaclient.KindProfileAvatar, Ref: id, MediaIDs: []uuid.UUID{*avatar}, Live: true})
		}
		if cover != nil {
			out = append(out, MediaClaimRow{Kind: mediaclient.KindProfileCover, Ref: id, MediaIDs: []uuid.UUID{*cover}, Live: true})
		}
	}
	return out, last, rows.Err()
}

// MediaClaimRowFor re-reads one holder, for the reconcile tool, under
// LockMediaHolder. A profile that no longer exists is not live and holds
// nothing; a live profile with the column empty holds nothing either.
func (s *Store) MediaClaimRowFor(ctx context.Context, kind string, ref uuid.UUID) (MediaClaimRow, error) {
	id, found, err := s.GetProfileMediaID(ctx, ref, kind)
	if err != nil {
		return MediaClaimRow{Kind: kind, Ref: ref}, err
	}
	row := MediaClaimRow{Kind: kind, Ref: ref, Live: found}
	if id != nil {
		row.MediaIDs = []uuid.UUID{*id}
	}
	return row, nil
}
