package service

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// A creator's Reposts tab (2026-10-24).
//
//	GET /v1/creators/:creatorId/reposts?cursor=&limit=   {data:{items:[…], next_cursor}}
//	GET /v1/users/:userId/reposts                        the older route, same answer
//
// The older route sits under /v1/users, which the gateway sends to
// user-service, so no client could reach it; /v1/creators is proxied here
// (as "Appears in" is). Until now the list also answered every caller the
// same page, whoever they were. ListUserReposts is now the viewer's page:
//
//  1. the reposter's profile gate (canViewAuthor: a private account the
//     viewer does not follow, a block either way, a deactivated or
//     pending-delete account) — closed answers an EMPTY page, never an
//     error, so the response does not say which of those it was;
//  2. each repost row's own audience against the reposter, by the batch
//     rule (a `followers` row needs viewer→reposter, `friends` both edges,
//     anything else restricted is dropped). Fail closed: an edge lookup that
//     fails drops the restricted rows;
//  3. the original post through the batch read (GetPostsByIDs) — the
//     original author's privacy and blocks, per-post visibility, review
//     state, age gate, and the members-only redaction (redactForViewer).
//     A repost whose original does not come back is left off the page.
//
// The reposter reading their own tab skips 1 and 2 (their own rows), never
// 3: an original they can no longer read is not shown to them either.
//
// Paging is over the reposter's raw rows (next_cursor = the last raw row's
// created_at), so a page may hold fewer than `limit` items and still carry a
// next_cursor; clients page until next_cursor is absent.

// ErrInvalidRepostCursor is a cursor that is not an RFC 3339 timestamp
// (400 INVALID_CURSOR at the routes).
var ErrInvalidRepostCursor = errors.New("invalid cursor")

// userRepostsPage is the store read; the Postgres store satisfies it.
type userRepostsPage func(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]postgres.Repost, string, error)

func (s *Service) userRepostsReader() userRepostsPage {
	if s.userRepostsSeam != nil {
		return s.userRepostsSeam
	}
	return s.pgStore.ListUserReposts
}

func (s *Service) repostProfileVisible(ctx context.Context, viewerID *uuid.UUID, userID uuid.UUID) bool {
	if s.repostProfileGate != nil {
		return s.repostProfileGate(ctx, viewerID, userID)
	}
	return s.canViewAuthor(ctx, viewerID, userID)
}

func (s *Service) repostEdges(ctx context.Context, viewerID *uuid.UUID, reposterID uuid.UUID, reposts []postgres.Repost) map[uuid.UUID]followEdges {
	if s.repostEdgesSeam != nil {
		return s.repostEdgesSeam(ctx, viewerID, reposterID)
	}
	// One row per restricted audience is enough: followEdgesOfRestrictedPosts
	// asks once per distinct author, and every row here has the same one.
	rows := make([]postgres.Post, 0, 1)
	for _, r := range reposts {
		if restrictedAudience(r.Visibility) {
			rows = append(rows, postgres.Post{AuthorID: reposterID, Visibility: r.Visibility})
			break
		}
	}
	return s.followEdgesOfRestrictedPosts(ctx, rows, viewerID)
}

// ListUserReposts is the viewer's page of userID's reposts. See the file
// comment for the rule.
func (s *Service) ListUserReposts(ctx context.Context, userID uuid.UUID, viewerID *uuid.UUID, limit int, cursor string) (*ListUserRepostsResult, error) {
	if cursor != "" {
		if _, err := time.Parse(time.RFC3339Nano, cursor); err != nil {
			return nil, ErrInvalidRepostCursor
		}
	}
	empty := &ListUserRepostsResult{Items: []UserRepostItem{}}
	isSelf := viewerID != nil && *viewerID == userID
	if !isSelf && !s.repostProfileVisible(ctx, viewerID, userID) {
		return empty, nil
	}
	reposts, nextCursor, err := s.userRepostsReader()(ctx, userID, limit, cursor)
	if err != nil {
		return nil, err
	}
	if !isSelf {
		edges := s.repostEdges(ctx, viewerID, userID, reposts)[userID]
		kept := reposts[:0:0]
		for _, r := range reposts {
			if batchVisibilityAllowsEdges(r.Visibility, edges) {
				kept = append(kept, r)
			}
		}
		reposts = kept
	}
	if len(reposts) == 0 {
		empty.NextCursor = nextCursor
		return empty, nil
	}

	ids := make([]uuid.UUID, 0, len(reposts))
	seen := make(map[uuid.UUID]bool, len(reposts))
	for _, r := range reposts {
		if !seen[r.OriginalPostID] {
			seen[r.OriginalPostID] = true
			ids = append(ids, r.OriginalPostID)
		}
	}
	// GetPostsByIDs runs redactForViewer on its way out (members_read.go).
	originals, err := s.GetPostsByIDs(ctx, ids, viewerID)
	if err != nil {
		return nil, err
	}
	items := make([]UserRepostItem, 0, len(reposts))
	for _, r := range reposts {
		original := originals[r.OriginalPostID]
		if original == nil || original.Post == nil {
			continue
		}
		items = append(items, UserRepostItem{
			RepostID:       r.ID,
			Type:           r.RepostType,
			QuoteText:      r.QuoteText,
			OriginalPostID: r.OriginalPostID,
			CreatedAt:      r.CreatedAt.Format(time.RFC3339),
			Post:           original,
		})
	}
	return &ListUserRepostsResult{Items: items, NextCursor: nextCursor}, nil
}
