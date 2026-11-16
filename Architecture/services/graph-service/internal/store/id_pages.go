package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Keyset-paged id lists for in-cluster callers (16 Nov 2026).
//
// feed-service fans a post out by walking the author's followers and
// connections page by page, and resolves a viewer's following list the same
// way. The offset routes refuse anything past offset 10,000 (handler.go,
// OFFSET_TOO_LARGE) and shift under the walk when an edge is removed, so an
// author with more than ~10,100 followers could never be fanned out. These
// lists page by a cursor instead:
//
//   - order: created_at DESC, then the listed user's id DESC. created_at is
//     not unique (a bulk import shares one timestamp), the pair is: follows
//     has PRIMARY KEY (follower_id, followee_id) and connections PRIMARY KEY
//     (user_a, user_b), so within one owner's list the other id is unique;
//   - cursor: the last row of the page, "<created_at unix micros>:<id>".
//     timestamptz holds microseconds, so the cursor names the row exactly;
//   - a page is "strictly after the cursor", so a row inserted while a walk
//     is under way (a new follow: created_at = now, the head of the list)
//     neither repeats a row already returned nor pushes one out of a later
//     page, and a removed row shifts nothing. A new follower is not seen by
//     a walk that already started; feed-service's follow backfill covers it.
//
// Indexes: idx_follows_followee_desc (followee_id, created_at DESC) and
// idx_follows_follower_desc (follower_id, created_at DESC), both in
// database/setup.sql, serve the two follow lists as a range scan from the
// cursor. connections has no created_at index; a user's connections are read
// through the primary key and idx_connections_b and sorted. The feature was
// retired on 24 Oct 2026, so those lists no longer grow.

// The three lists ListIDPage serves.
const (
	ListFollowers   = "followers"
	ListFollowing   = "following"
	ListConnections = "connections"
)

const (
	// IDPageDefaultLimit is the page size when the caller names none.
	IDPageDefaultLimit = 100
	// IDPageMaxLimit caps a page. In-cluster callers only, so it is higher
	// than the public lists' 100.
	IDPageMaxLimit = 500
)

// ErrInvalidListCursor is a cursor that is not "<unix micros>:<uuid>".
var ErrInvalidListCursor = errors.New("invalid list cursor")

// ListCursor names the last row of a page.
type ListCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// String is the wire form: "<created_at unix micros>:<id>".
func (c ListCursor) String() string {
	return strconv.FormatInt(c.CreatedAt.UnixMicro(), 10) + ":" + c.ID.String()
}

// ParseListCursor reads the wire form strictly. A cursor that does not parse
// is an error, never "start from the top": a caller that resumed a walk from
// a damaged cursor would silently write the head of the list again and
// believe it had finished the tail.
func ParseListCursor(raw string) (ListCursor, error) {
	micros, id, ok := strings.Cut(raw, ":")
	if !ok {
		return ListCursor{}, ErrInvalidListCursor
	}
	n, err := strconv.ParseInt(micros, 10, 64)
	if err != nil || n < 0 {
		return ListCursor{}, ErrInvalidListCursor
	}
	// The canonical 36-character form only: uuid.Parse also accepts braces,
	// a urn: prefix and bare hex, none of which String() ever produces.
	if len(id) != 36 {
		return ListCursor{}, ErrInvalidListCursor
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return ListCursor{}, ErrInvalidListCursor
	}
	return ListCursor{CreatedAt: time.UnixMicro(n).UTC(), ID: parsed}, nil
}

// ClampIDPageLimit returns the page size a request gets.
func ClampIDPageLimit(limit int) int {
	if limit <= 0 {
		return IDPageDefaultLimit
	}
	if limit > IDPageMaxLimit {
		return IDPageMaxLimit
	}
	return limit
}

// idPageQueries holds, per list, the SELECT up to its cursor clause: $1 is
// the owner, and each yields (id, created_at) under those two names.
var idPageQueries = map[string]string{
	ListFollowers: `
		SELECT l.id, l.created_at FROM (
			SELECT follower_id AS id, created_at FROM follows WHERE followee_id = $1
		) l`,
	ListFollowing: `
		SELECT l.id, l.created_at FROM (
			SELECT followee_id AS id, created_at FROM follows WHERE follower_id = $1
		) l`,
	ListConnections: `
		SELECT l.id, l.created_at FROM (
			SELECT CASE WHEN user_a = $1 THEN user_b ELSE user_a END AS id, created_at
			FROM connections WHERE user_a = $1 OR user_b = $1
		) l`,
}

// ListIDPage returns one page of a user's followers, following or
// connections, newest first, and the cursor of the next page ("" on the last
// one). after == nil starts at the head. Hidden (deactivated or
// deletion-scheduled) accounts are left out, as on the offset lists.
func (s *Store) ListIDPage(ctx context.Context, kind string, userID uuid.UUID, limit int, after *ListCursor) ([]uuid.UUID, string, error) {
	q, ok := idPageQueries[kind]
	if !ok {
		return nil, "", fmt.Errorf("unknown id list %q", kind)
	}
	limit = ClampIDPageLimit(limit)
	args := []any{userID}
	q += `
		WHERE NOT EXISTS (SELECT 1 FROM hidden_users h WHERE h.user_id = l.id)`
	if after != nil {
		q += `
		  AND (l.created_at, l.id) < ($2, $3)`
		args = append(args, after.CreatedAt, after.ID)
	}
	// One row more than the page, to know whether another page exists.
	args = append(args, limit+1)
	q += `
		ORDER BY l.created_at DESC, l.id DESC
		LIMIT $` + strconv.Itoa(len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	edges := make([]FollowEdge, 0, limit+1)
	for rows.Next() {
		var e FollowEdge
		if err := rows.Scan(&e.UserID, &e.CreatedAt); err != nil {
			return nil, "", err
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	ids, next := cutIDPage(edges, limit)
	return ids, next, nil
}

// cutIDPage turns the limit+1 rows read into the page and its next cursor:
// the cursor is the LAST ROW RETURNED, and it is set only when a further row
// was read, so the final page always carries "".
func cutIDPage(edges []FollowEdge, limit int) ([]uuid.UUID, string) {
	next := ""
	if len(edges) > limit {
		edges = edges[:limit]
		last := edges[limit-1]
		next = ListCursor{CreatedAt: last.CreatedAt, ID: last.UserID}.String()
	}
	ids := make([]uuid.UUID, 0, len(edges))
	for _, e := range edges {
		ids = append(ids, e.UserID)
	}
	return ids, next
}
