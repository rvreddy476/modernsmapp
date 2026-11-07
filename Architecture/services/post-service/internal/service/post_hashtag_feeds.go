// post_hashtag_feeds.go holds the hashtag and trending reads: posts under a
// hashtag, trending posts, hashtag search and the 24-hour trending
// hashtags. Moved out of post.go unchanged.

package service

import (
	"context"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ============================================================
// Hashtag Search
// ============================================================

// GetPostsByHashtag returns posts with a specific hashtag.
// sort accepts "top" or "recent" (default).
// contentTypes filters by content_type (e.g. ["post"], ["flick"], ["long_video"]); nil = all.
//
// The store lists public, viewer-eligible (effectively approved, live)
// posts; the author gate — private accounts the viewer does not follow,
// blocks, deactivated authors — is canViewPosts, the same fail-closed
// choke point GetRecentPosts and the sound page use. viewerID nil is the
// signed-out stranger. HashtagTotal counts exactly this set.
func (s *Service) GetPostsByHashtag(ctx context.Context, viewerID *uuid.UUID, hashtag string, limit int, cursor, sort string, contentTypes []string) ([]PostDetail, string, error) {
	mode := postgres.HashtagSortRecent
	if sort == "top" {
		mode = postgres.HashtagSortTop
	}
	// A page the gates empty reads on (list_pages.go).
	details, nextCursor, err := visiblePage(cursor,
		func(cur string) ([]postgres.Post, string, error) {
			return s.pgStore.GetPostsByHashtag(ctx, hashtag, limit, cur, mode, contentTypes)
		},
		func(posts []postgres.Post) ([]PostDetail, error) {
			posts = s.hashtagPostsVisibleTo(ctx, viewerID, posts)
			// Counts and polls for the page in batched reads (list_reads.go).
			details := s.pageDetails(ctx, viewerID, posts)
			// Instant publish (processing.go): public discovery surface with no
			// viewer — a still-processing post is nobody's to see here.
			return s.attachMediaStateToDetails(ctx, details, nil)
		})
	if err != nil {
		return nil, "", err
	}
	// Members-only rows, for the real viewer: members and the author read
	// them in full here too (members_read.go).
	s.redactDetailsForViewer(ctx, viewerID, details)
	return details, nextCursor, nil
}

// HashtagTotal is in hashtag_total.go.

// GetTrendingPosts returns trending posts globally, optionally scoped to one
// or more content types. Used by the Posttube/Reels "Trending" tabs and the
// general discover surface. cursor is the same opaque base64 string used by
// the hashtag top sort.
//
// The store lists public, viewer-eligible rows (ViewerEligibleSQL: live,
// effectively approved — no pending, flagged, rejected or held post); the
// author gate — private accounts the viewer does not follow, blocks in
// either direction, deactivated authors — is postsVisibleToViewer, the same
// fail-closed choke point GetRecentPosts and the hashtag page use
// (2026-10-30 audit B2). viewerID nil is the signed-out stranger.
func (s *Service) GetTrendingPosts(ctx context.Context, contentTypes []string, limit int, cursor string, viewerID *uuid.UUID) ([]PostDetail, string, error) {
	store := s.listReads()
	if store == nil {
		return nil, "", ErrAuthoringStoreUnavailable
	}
	// The first page through a short cache; a page the gates empty reads on
	// (list_pages.go).
	details, nextCursor, err := visiblePage(cursor,
		func(cur string) ([]postgres.Post, string, error) {
			return s.trendingPage(ctx, store, contentTypes, limit, cur)
		},
		func(posts []postgres.Post) ([]PostDetail, error) {
			posts = s.postsVisibleToViewer(ctx, viewerID, posts)
			// Counts and polls for the page in batched reads (list_reads.go).
			details := s.pageDetails(ctx, viewerID, posts)
			// Instant publish (processing.go): public discovery surface with no
			// viewer — a still-processing post is nobody's to see here.
			return s.attachMediaStateToDetails(ctx, details, nil)
		})
	if err != nil {
		return nil, "", err
	}
	// Members-only rows, for the real viewer: members and the author read
	// them in full here too (members_read.go).
	s.redactDetailsForViewer(ctx, viewerID, details)
	// A trending long video arrived with no channel, so the watch card had
	// no name or avatar to draw a poster with. Same best-effort attach
	// GetBookmarks and the watch-progress list do — one batched lookup for
	// the page's distinct authors, and the page reads the same without it.
	ptrs := make([]*PostDetail, len(details))
	for i := range details {
		ptrs[i] = &details[i]
	}
	attachViewer := uuid.Nil
	if viewerID != nil {
		attachViewer = *viewerID
	}
	s.attachChannelRefs(ctx, attachViewer, ptrs)
	return details, nextCursor, nil
}

// SearchHashtags returns hashtag suggestions matching a prefix query.
// Reads directly from posts.hashtags via the store; no Redis index is wired.
func (s *Service) SearchHashtags(ctx context.Context, query string, limit int) ([]postgres.HashtagSuggestion, error) {
	return s.pgStore.SearchHashtags(ctx, query, limit)
}

// GetTrendingHashtags24h returns the most-used hashtags in the last 24 hours.
// SQL fallback used until the Redis trending writer ships.
func (s *Service) GetTrendingHashtags24h(ctx context.Context, limit int) ([]postgres.HashtagTrending24h, error) {
	return s.pgStore.GetTrendingHashtags24h(ctx, limit)
}
