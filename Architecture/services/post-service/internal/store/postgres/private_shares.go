package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

/*
	Private sharing (Creator Hub part 6b, 2026-09-28; migration 053).

	post_private_shares lists the users a 'private' post is ALSO readable by.
	The list is consulted only by single-post reads (the detail, playback
	authorisation, the comments read); no listing query joins it, so a
	shared post can never surface in a feed, search, channel list, Up next
	or notification. The service owns that rule (service/private_shares.go);
	this file is storage only.
*/

// PrivateShare is one row of a post's share list.
type PrivateShare struct {
	UserID  uuid.UUID `json:"user_id"`
	AddedAt time.Time `json:"added_at"`
}

// ErrPrivateShareUnknownUser: a user id the users foreign key refused
// (migration 053 adds it when the app users table exists).
var ErrPrivateShareUnknownUser = errors.New("private share names a user that does not exist")

// ListPrivateShares returns a post's share list, oldest first.
func (s *Store) ListPrivateShares(ctx context.Context, postID uuid.UUID) ([]PrivateShare, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, added_at FROM post_private_shares
		WHERE post_id = $1
		ORDER BY added_at ASC, user_id ASC`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PrivateShare{}
	for rows.Next() {
		var sh PrivateShare
		if err := rows.Scan(&sh.UserID, &sh.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// ReplacePrivateShares makes userIDs the post's whole share list, in one
// transaction guarded by author_id: users already on the list keep their
// added_at, users not named are removed, and a change writes one
// 'post.private_shares' post_edit_audit row ({from: [...ids], to: [...ids]}).
// Returns pgx.ErrNoRows for a missing / deleted post, ErrPostEditNotOwned
// for someone else's, ErrPrivateShareUnknownUser when the users FK refuses.
// The service has already validated, deduped and capped userIDs.
func (s *Store) ReplacePrivateShares(ctx context.Context, postID, ownerID uuid.UUID, userIDs []uuid.UUID) ([]PrivateShare, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var author uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT author_id FROM posts WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, postID).Scan(&author); err != nil {
		return nil, err // pgx.ErrNoRows for a missing / deleted post
	}
	if author != ownerID {
		return nil, ErrPostEditNotOwned
	}

	before, err := txPrivateShareIDs(ctx, tx, postID)
	if err != nil {
		return nil, err
	}
	if userIDs == nil {
		userIDs = []uuid.UUID{}
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM post_private_shares WHERE post_id = $1 AND NOT (user_id = ANY($2))`, postID, userIDs); err != nil {
		return nil, fmt.Errorf("trim private shares: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_private_shares (post_id, user_id)
		SELECT $1, u FROM unnest($2::uuid[]) AS u
		ON CONFLICT (post_id, user_id) DO NOTHING`, postID, userIDs); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return nil, ErrPrivateShareUnknownUser
		}
		return nil, fmt.Errorf("add private shares: %w", err)
	}
	after, err := txPrivateShareIDs(ctx, tx, postID)
	if err != nil {
		return nil, err
	}
	if !sameUUIDSet(before, after) {
		if err := insertPostEditAudit(ctx, tx, postID, ownerID, "post.private_shares",
			map[string]PostEditChange{"private_shares": {From: uuidStrings(before), To: uuidStrings(after)}}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.ListPrivateShares(ctx, postID)
}

// PrivateSharedPostIDs reports which of postIDs name viewerID on their share
// list. One query for a page; an empty input asks nothing.
func (s *Store) PrivateSharedPostIDs(ctx context.Context, viewerID uuid.UUID, postIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool, len(postIDs))
	if viewerID == uuid.Nil || len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT post_id FROM post_private_shares WHERE user_id = $1 AND post_id = ANY($2)`, viewerID, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func txPrivateShareIDs(ctx context.Context, tx pgx.Tx, postID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx,
		`SELECT user_id FROM post_private_shares WHERE post_id = $1 ORDER BY added_at ASC, user_id ASC`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func sameUUIDSet(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[uuid.UUID]struct{}, len(a))
	for _, id := range a {
		set[id] = struct{}{}
	}
	for _, id := range b {
		if _, ok := set[id]; !ok {
			return false
		}
	}
	return true
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}
