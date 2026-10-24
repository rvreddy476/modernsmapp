package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Followed hashtags (migration 074, 2026-10-24). The tag is the form
// posts.hashtags holds; the service normalises it before it reaches here.

// ErrHashtagFollowLimit: the user already follows the maximum number of
// hashtags.
var ErrHashtagFollowLimit = errors.New("hashtag follow limit reached")

// FollowedHashtag is one row of the caller's followed list.
type FollowedHashtag struct {
	Tag        string    `json:"tag"`
	FollowedAt time.Time `json:"followed_at"`
}

// FollowHashtag records that userID follows tag. Following a tag already
// followed is a no-op that succeeds (created=false), at the limit too; a new
// follow past max is ErrHashtagFollowLimit.
func (s *Store) FollowHashtag(ctx context.Context, userID uuid.UUID, tag string, max int) (created bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('hashtag_follows:' || $1::text, 0))`, userID); err != nil {
		return false, err
	}
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM hashtag_follows WHERE user_id = $1 AND tag = $2)`, userID, tag).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, tx.Commit(ctx)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM hashtag_follows WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return false, err
	}
	if n >= max {
		return false, ErrHashtagFollowLimit
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO hashtag_follows (user_id, tag, created_at) VALUES ($1, $2, NOW()) ON CONFLICT DO NOTHING`, userID, tag); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// UnfollowHashtag removes the follow; removed=false when there was none.
func (s *Store) UnfollowHashtag(ctx context.Context, userID uuid.UUID, tag string) (removed bool, err error) {
	t, err := s.db.Exec(ctx, `DELETE FROM hashtag_follows WHERE user_id = $1 AND tag = $2`, userID, tag)
	if err != nil {
		return false, err
	}
	return t.RowsAffected() > 0, nil
}

// IsFollowingHashtag reports whether userID follows tag.
func (s *Store) IsFollowingHashtag(ctx context.Context, userID uuid.UUID, tag string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM hashtag_follows WHERE user_id = $1 AND tag = $2)`, userID, tag).Scan(&ok)
	return ok, err
}

// encodeKeysetCursor / decodeKeysetCursor are the (followed_at, tag) and
// (created_at, id) keyset cursors of the two lists below.
func encodeKeysetCursor(at time.Time, key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + key))
}

func decodeKeysetCursor(cursor string) (time.Time, string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", false
	}
	at, key, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, "", false
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, "", false
	}
	return t, key, true
}

// ListFollowedHashtags is the user's followed tags, newest follow first.
func (s *Store) ListFollowedHashtags(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]FollowedHashtag, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	args := []any{userID, limit + 1}
	q := `SELECT tag, created_at FROM hashtag_follows WHERE user_id = $1`
	if at, tag, ok := decodeKeysetCursor(cursor); ok {
		args = append(args, at, tag)
		q += ` AND (created_at, tag) < ($3, $4)`
	}
	q += ` ORDER BY created_at DESC, tag DESC LIMIT $2`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []FollowedHashtag{}
	for rows.Next() {
		var f FollowedHashtag
		if err := rows.Scan(&f.Tag, &f.FollowedAt); err != nil {
			return nil, "", err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		next = encodeKeysetCursor(last.FollowedAt, last.Tag)
	}
	return out, next, nil
}

// FollowedHashtagTags is every tag the user follows (bounded by the follow
// limit), for the followed-posts read.
func (s *Store) FollowedHashtagTags(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT tag FROM hashtag_follows WHERE user_id = $1 ORDER BY tag`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// GetPostsByHashtags is the recent public posts carrying any of tags, newest
// first: the hashtag page's eligibility (public, approved, published, not
// deleted), never the viewer's own posts, nothing older than since. The
// author gate (private accounts, blocks, deactivated authors) and the
// members-only redaction are the service's, as on the hashtag page.
func (s *Store) GetPostsByHashtags(ctx context.Context, tags []string, viewerID uuid.UUID, limit int, cursor string, since time.Time) ([]Post, string, error) {
	if len(tags) == 0 {
		return []Post{}, "", nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	args := []any{tags, limit + 1, viewerID, since}
	q := `SELECT ` + postCols + `
		FROM posts p
		WHERE p.hashtags && $1::text[]
		  AND p.visibility = 'public'
		  AND ` + ViewerEligibleSQL("p") + `
		  AND p.author_id <> $3
		  AND p.created_at >= $4`
	if at, key, ok := decodeKeysetCursor(cursor); ok {
		if id, err := uuid.Parse(key); err == nil {
			args = append(args, at, id)
			q += fmt.Sprintf(` AND (p.created_at, p.id) < ($%d, $%d)`, len(args)-1, len(args))
		}
	}
	q += ` ORDER BY p.created_at DESC, p.id DESC LIMIT $2`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	posts, err := scanPostRows(rows)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(posts) > limit {
		posts = posts[:limit]
		last := posts[limit-1]
		next = encodeKeysetCursor(last.CreatedAt, last.ID.String())
	}
	if err := s.attachPostMedia(ctx, posts); err != nil {
		return nil, "", err
	}
	return posts, next, nil
}
