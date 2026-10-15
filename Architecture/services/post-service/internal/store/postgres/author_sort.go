package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Profile grid sort (T-21, 2026-10-14): GET /v1/posts/by-author/:id
// ?sort=latest|popular|oldest.
//
//   - latest (default): pinned first, then newest. Unchanged order, except
//     that a NULL is_pinned now sorts as unpinned rather than above pinned.
//   - popular: like count (post_engagement_counts.like_count) high to low.
//   - oldest: oldest first. Pinning does not apply.
//
// Every sort pages by keyset on its full sort key plus the post id, carried
// in an opaque cursor that names the sort it was minted for. The old latest
// cursor paged on created_at alone, which (a) re-served or skipped rows that
// share a timestamp and (b) after a pinned row ended a page, re-served the
// pinned rows older than it and skipped the unpinned rows newer than it.
//
// Popular is as stable as a live count allows: the cursor holds the like count
// the last row had when the page was read, so a post whose count crosses that
// value between pages can be missed or repeated once. Nothing else moves.
//
// Index: latest and oldest walk idx_posts_author_desc (author_id, created_at).
// Popular has no covering index — the count lives in post_engagement_counts,
// which has no author_id — so it reads the author's rows by that same index,
// probes post_engagement_counts by primary key per row and top-N sorts. That
// is bounded by one author's post count, which is acceptable for a profile;
// a covering index would need author_id denormalised onto the counts table.

// AuthorPostSort is the order of an author's post listing.
type AuthorPostSort string

const (
	AuthorSortLatest  AuthorPostSort = "latest"
	AuthorSortPopular AuthorPostSort = "popular"
	AuthorSortOldest  AuthorPostSort = "oldest"
)

var (
	// ErrInvalidAuthorSort is an unknown ?sort= value (HTTP 400).
	ErrInvalidAuthorSort = errors.New("invalid author post sort")
	// ErrInvalidAuthorCursor is a cursor that does not decode, or that was
	// minted for a different sort (HTTP 400).
	ErrInvalidAuthorCursor = errors.New("invalid author post cursor")
)

// ParseAuthorPostSort accepts the three sorts; empty is latest.
func ParseAuthorPostSort(raw string) (AuthorPostSort, error) {
	switch AuthorPostSort(strings.ToLower(strings.TrimSpace(raw))) {
	case "", AuthorSortLatest:
		return AuthorSortLatest, nil
	case AuthorSortPopular:
		return AuthorSortPopular, nil
	case AuthorSortOldest:
		return AuthorSortOldest, nil
	}
	return "", fmt.Errorf("%w: %q (want latest, popular or oldest)", ErrInvalidAuthorSort, raw)
}

// authorCursor is the decoded keyset position: the last row of the previous
// page, in the sort it was minted for.
type authorCursor struct {
	Sort      AuthorPostSort `json:"s"`
	Pinned    bool           `json:"p,omitempty"`
	CreatedAt time.Time      `json:"t"`
	Likes     int64          `json:"l,omitempty"`
	ID        uuid.UUID      `json:"id"`
	// legacy marks a pre-T-21 latest cursor (a bare RFC 3339 created_at).
	legacy bool
}

// AuthorPostSorts is every accepted ?sort= value, the default first.
var AuthorPostSorts = []AuthorPostSort{AuthorSortLatest, AuthorSortPopular, AuthorSortOldest}

func encodeAuthorCursor(c authorCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// EncodeAuthorCursor mints the cursor of a page whose last row is the one
// described. Clients treat it as opaque; it is exported for the contract
// fixtures and tests.
func EncodeAuthorCursor(sort AuthorPostSort, pinned bool, createdAt time.Time, likes int64, id uuid.UUID) string {
	return encodeAuthorCursor(authorCursor{Sort: sort, Pinned: pinned, CreatedAt: createdAt, Likes: likes, ID: id})
}

// decodeAuthorCursor reads a cursor for sort. A bare RFC 3339 timestamp is the
// cursor every client held before T-21; it is still honoured for latest (with
// its old created_at-only semantics) so an open profile keeps paging across
// the deploy.
func decodeAuthorCursor(sort AuthorPostSort, raw string) (authorCursor, error) {
	if sort == AuthorSortLatest {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return authorCursor{Sort: sort, CreatedAt: t, legacy: true}, nil
		}
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return authorCursor{}, ErrInvalidAuthorCursor
	}
	var c authorCursor
	if err := json.Unmarshal(b, &c); err != nil || c.ID == uuid.Nil || c.CreatedAt.IsZero() {
		return authorCursor{}, ErrInvalidAuthorCursor
	}
	if c.Sort != sort {
		return authorCursor{}, fmt.Errorf("%w: minted for sort %q, used with %q", ErrInvalidAuthorCursor, c.Sort, sort)
	}
	return c, nil
}

// authorLikesExpr is a post's like count for the popular sort. A correlated
// subquery rather than a join so postCols stays unqualified (posts and
// post_engagement_counts share column names such as updated_at).
const authorLikesExpr = `COALESCE((SELECT c.like_count FROM post_engagement_counts c WHERE c.post_id = posts.id), 0)::bigint`

// authorSortSQL returns the ORDER BY for sort and, given a cursor, the keyset
// predicate with its arguments numbered from next.
func authorSortSQL(sort AuthorPostSort, cur *authorCursor, next int) (orderBy, where string, args []any) {
	switch sort {
	case AuthorSortPopular:
		orderBy = authorLikesExpr + ` DESC, id DESC`
		if cur != nil {
			where = fmt.Sprintf(`(%s, id) < ($%d, $%d)`, authorLikesExpr, next, next+1)
			args = []any{cur.Likes, cur.ID}
		}
	case AuthorSortOldest:
		orderBy = `created_at ASC, id ASC`
		if cur != nil {
			where = fmt.Sprintf(`(created_at, id) > ($%d, $%d)`, next, next+1)
			args = []any{cur.CreatedAt, cur.ID}
		}
	default: // latest
		orderBy = `COALESCE(is_pinned, false) DESC, created_at DESC, id DESC`
		if cur != nil && cur.legacy {
			where = fmt.Sprintf(`created_at < $%d`, next)
			args = []any{cur.CreatedAt}
		} else if cur != nil {
			where = fmt.Sprintf(`(COALESCE(is_pinned, false), created_at, id) < ($%d, $%d, $%d)`, next, next+1, next+2)
			args = []any{cur.Pinned, cur.CreatedAt, cur.ID}
		}
	}
	return orderBy, where, args
}

// GetPostsByAuthorSorted is GetPostsByAuthor in the given order. See the
// file comment for the orders and the cursor.
func (s *Store) GetPostsByAuthorSorted(ctx context.Context, authorID uuid.UUID, contentType string, sort AuthorPostSort, limit int, cursor string, includeNonApproved bool) ([]Post, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if sort == "" {
		sort = AuthorSortLatest
	}
	var cur *authorCursor
	if cursor != "" {
		c, err := decodeAuthorCursor(sort, cursor)
		if err != nil {
			return nil, "", err
		}
		cur = &c
	}

	args := []any{authorID, limit + 1}
	query := `SELECT ` + postCols + `, ` + authorLikesSelect(sort) + `
		FROM posts
		WHERE author_id = $1 AND deleted_at IS NULL AND publish_at IS NULL`
	if !includeNonApproved {
		query += " AND " + viewerApprovedSQL
	}
	if contentType != "" && contentType != "all" {
		args = append(args, contentType)
		query += fmt.Sprintf(` AND content_type = $%d`, len(args))
	}
	orderBy, where, keyArgs := authorSortSQL(sort, cur, len(args)+1)
	if where != "" {
		query += ` AND ` + where
		args = append(args, keyArgs...)
	}
	query += ` ORDER BY ` + orderBy + ` LIMIT $2`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var posts []Post
	var likes []int64
	for rows.Next() {
		var p Post
		var n int64
		if err := rows.Scan(append(postScanDestinations(&p), &n)...); err != nil {
			return nil, "", err
		}
		p.deriveScheduled()
		p.deriveAudio()
		posts = append(posts, p)
		likes = append(likes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(posts) > limit {
		last := posts[limit-1]
		nextCursor = encodeAuthorCursor(authorCursor{
			Sort: sort, Pinned: last.IsPinned, CreatedAt: last.CreatedAt, Likes: likes[limit-1], ID: last.ID,
		})
		posts = posts[:limit]
	}

	// Ordered + normalized in one place; see post_media.go.
	if err := s.attachPostMedia(ctx, posts); err != nil {
		return nil, "", err
	}
	return posts, nextCursor, nil
}

// authorLikesSelect is the extra column every author listing scans: the like
// count for popular (the cursor needs the value the row was sorted by), a
// constant otherwise so the other sorts pay nothing for it.
func authorLikesSelect(sort AuthorPostSort) string {
	if sort == AuthorSortPopular {
		return authorLikesExpr + ` AS sort_likes`
	}
	return `0::bigint AS sort_likes`
}
