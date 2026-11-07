// post_reads.go is the read side of posts: a single post (GetPost), the
// author's profile lists and counts, the recent feed, and the batch read
// the feed hydrates from (GetPostsByIDs / loadPostsByIDs /
// batchVisibility). Moved out of post.go unchanged.

package service

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/google/uuid"
)

func (s *Service) GetPost(ctx context.Context, id uuid.UUID, viewerID *uuid.UUID) (*PostDetail, error) {
	// Tier 1b: cached read. Falls through to pgStore on miss; nil
	// rdb is supported.
	p, err := s.getCachedPostBody(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}

	// reels/posttube item 5: a post the spam detector or auto-moderation
	// flagged/rejected — or one still pending a verdict — must not be
	// reachable by direct link. Feeds already filter on review_status;
	// this closes the GetPost hole. The author still sees their own.
	if p.EffectiveReviewStatus() != "" && p.EffectiveReviewStatus() != "approved" {
		if viewerID == nil || *viewerID != p.AuthorID {
			return nil, nil
		}
	}

	// Instant publish (processing.go): overlay the live media pipeline
	// state — AFTER the cache, never from it — and hide a still-processing
	// post from everyone but its author. Same 404 as the other gates.
	if err := s.attachMediaState(ctx, []*postgres.Post{p}); err != nil {
		return nil, err
	}
	if hiddenFromViewer(p, viewerID) {
		return nil, nil
	}

	// Visibility gate on the direct-link read. The engagement endpoints,
	// feed batch and repost paths were already gated; this was the one
	// door left open — anyone with the id could read a private or
	// followers-only post. Renders as 404, not 403: a denial that
	// confirms existence is itself a leak.
	if !s.viewerMayViewPost(ctx, p, viewerID) {
		return nil, nil
	}
	// Age-restricted (age_gate.go): only after the viewer may see the post
	// at all, so the refusal never confirms a post they could not open.
	if err := s.checkAgeGate(ctx, p, viewerID); err != nil {
		return nil, err
	}

	counts, err := s.countsForPost(ctx, id)
	if err != nil {
		return nil, err
	}

	// Load poll if present: the poll and the viewer's votes, as GET
	// /v1/posts/:id/poll reads them (GetPoll). Best-effort, as before.
	if hasPoll, _ := s.pgStore.HasPoll(ctx, id); hasPoll {
		if poll, err := s.GetPoll(ctx, id, viewerID); err == nil && poll != nil {
			p.Poll = poll
		}
	}

	detail := &PostDetail{Post: p, Counts: counts}
	detail.ViewCount = s.getViewCount(ctx, id)
	detail.Author = s.fetchPostAuthor(ctx, viewerID, p.AuthorID)

	// Repost count from PG
	repostCount, _ := s.pgStore.GetRepostCount(ctx, id)
	detail.RepostCount = repostCount

	// A post is repostable if it's public (non-private) and not a reel
	// (heart_only.go).
	detail.IsRepostable = repostableContent(p.ContentType, p.Visibility)

	// Enrich with viewer-specific state
	if viewerID != nil {
		// A Scylla error shows no reaction (logged) rather than failing
		// the open (2026-10-30 audit B6); the body is PostgreSQL's.
		stored, err := s.viewerStoredReaction(ctx, id, *viewerID)
		if err != nil {
			slog.WarnContext(ctx, "viewer reaction unavailable; serving none", "post_id", id, "err", err)
			stored = ""
		}
		// An old emoji reads as the heart (heart_only.go).
		applyViewerReaction(detail, stored)
		bookmarked, err := s.pgStore.IsBookmarked(ctx, *viewerID, id)
		if err != nil {
			return nil, fmt.Errorf("load viewer bookmark: %w", err)
		}
		detail.IsBookmarked = bookmarked

		// Repost state
		repostState, err := s.GetRepostState(ctx, *viewerID, id)
		if err != nil {
			return nil, fmt.Errorf("load viewer repost: %w", err)
		}
		if repostState != nil && repostState.HasReposted {
			detail.ViewerRepost = repostState
			detail.HasReposted = true
		}
		// MTube (2026-09-27): the viewer's private dislike and Queue state.
		detail.ViewerDisliked = s.viewerDisliked(ctx, viewerID, id)
		detail.ViewerQueued = s.viewerQueued(ctx, *viewerID, id)
	}
	detail.Chapters = s.chaptersForDetail(ctx, p)

	// Tube: the author's channel card on a long video (channels.go).
	attachViewer := uuid.Nil
	if viewerID != nil {
		attachViewer = *viewerID
	}
	s.attachChannelRefs(ctx, attachViewer, []*PostDetail{detail})
	// Creator Hub (hub_detail.go): like_count / hide_like_count, the
	// related post card, the owner's notify_subscribers.
	s.applyHubDetail(ctx, detail, viewerID)
	// The sound the post plays, for this viewer (sounds.go). After the
	// cache read, like everything else that depends on who is asking.
	s.attachSounds(ctx, viewerID, []*PostDetail{detail})
	// A long video's credits, as this viewer may see them (credits.go).
	s.attachCredits(ctx, detail, viewerID)

	return detail, nil
}

// GetPostsByAuthor returns paginated posts by a specific author. The author
// sees their own posts regardless of review status (a flagged reel still
// shows in their own profile grid); every other viewer sees only approved.
//
// sort is latest (pinned first, newest), popular or oldest (T-21,
// postgres/author_sort.go). A cursor minted for another sort, or one that does
// not decode, is postgres.ErrInvalidAuthorCursor.
func (s *Service) GetPostsByAuthor(ctx context.Context, authorID uuid.UUID, contentType string, sort postgres.AuthorPostSort, limit int, cursor string, viewerID *uuid.UUID) ([]PostDetail, string, error) {
	return s.GetPostsByAuthorFiltered(ctx, AuthorListFilter{
		AuthorID: authorID, ContentType: contentType, Sort: sort, Limit: limit, Cursor: cursor,
	}, viewerID)
}

// AuthorListFilter is one page of GET /v1/posts/by-author/:id. MembersOnly
// (?members_only=true, 2026-10-23) keeps only the author's members-only
// posts; every row still goes through the members-only rule, so a
// non-member gets them redacted (the "members" shelf of a profile shows the
// lock cards).
type AuthorListFilter struct {
	AuthorID    uuid.UUID
	ContentType string
	Sort        postgres.AuthorPostSort
	Limit       int
	Cursor      string
	MembersOnly bool
}

// authorPostsQuery is the store query for one page of f.
func authorPostsQuery(f AuthorListFilter, isAuthor bool) postgres.AuthorPostsQuery {
	return postgres.AuthorPostsQuery{
		AuthorID: f.AuthorID, ContentType: f.ContentType, Sort: f.Sort, Limit: f.Limit, Cursor: f.Cursor,
		IncludeNonApproved: isAuthor, MembersOnly: f.MembersOnly,
	}
}

// GetPostsByAuthorFiltered is GetPostsByAuthor with the list's filters.
func (s *Service) GetPostsByAuthorFiltered(ctx context.Context, f AuthorListFilter, viewerID *uuid.UUID) ([]PostDetail, string, error) {
	authorID := f.AuthorID
	isAuthor := viewerID != nil && *viewerID == authorID
	// Private account: the whole grid is follower-only. An empty page — not
	// an error — is the honest answer for a stranger: the profile itself
	// stays reachable (with is_private=true) and the client renders the
	// "This account is private" state. Fail-closed on an unresolved graph.
	if !isAuthor && !s.canViewAuthor(ctx, viewerID, authorID) {
		return []PostDetail{}, "", nil
	}
	posts, nextCursor, err := s.pgStore.GetPostsByAuthorQuery(ctx, authorPostsQuery(f, isAuthor))
	if err != nil {
		return nil, "", err
	}

	// Visibility filter for the profile grid. ONE graph lookup covers the
	// whole page — every row shares the author — and it runs only when a
	// followers-only post is actually present.
	viewerFollows := func() func() bool {
		checked, follows := false, false
		return func() bool {
			if !checked {
				checked = true
				if viewerID != nil {
					f, err := s.checkViewerFollowsAuthor(ctx, *viewerID, authorID)
					if err != nil {
						log.Printf("Warning: profile visibility graph lookup failed; hiding restricted posts: %v", err)
					} else {
						follows = f
					}
				}
			}
			return follows
		}
	}()
	// The friends audience needs the other edge too: one more lookup, made
	// only when a friends-only post is on the page. Fail closed.
	viewerMutual := func() func() bool {
		checked, mutual := false, false
		return func() bool {
			if !checked {
				checked = true
				if viewerID != nil {
					m, err := s.checkViewerMutualWithAuthor(ctx, *viewerID, authorID)
					if err != nil {
						log.Printf("Warning: profile friends visibility graph lookup failed; hiding friends posts: %v", err)
					} else {
						mutual = m
					}
				}
			}
			return mutual
		}
	}()

	visible := authorGridRows(posts, f.MembersOnly, viewerID, viewerFollows, viewerMutual)
	// Counts and polls for the page in batched reads (list_reads.go).
	details := s.pageDetails(ctx, viewerID, visible)

	// Instant publish (processing.go): overlay live media state, drop what
	// this viewer may not see while it processes.
	details, err = s.attachMediaStateToDetails(ctx, details, viewerID)
	if err != nil {
		return nil, "", err
	}
	// Members-only rows: redacted for this viewer (members_read.go).
	s.redactDetailsForViewer(ctx, viewerID, details)
	return details, nextCursor, nil
}

// authorGridRows is the profile grid's per-row drop, pure: the audience rule
// for anyone but the author (profileGridAllows, the graph asked lazily), the
// listing rule for unlisted videos (listedTo: the author's alone in a
// listing, though the link opens for anyone; unlisted_listing.go), and the
// members shelf's own filter (the store filters on the gate; an open row is
// never on the members-only list whatever it answered).
func authorGridRows(posts []postgres.Post, membersOnly bool, viewerID *uuid.UUID, follows, mutual func() bool) []postgres.Post {
	visible := make([]postgres.Post, 0, len(posts))
	for _, post := range posts {
		isAuthor := viewerID != nil && *viewerID == post.AuthorID
		if !isAuthor && !profileGridAllows(post.Visibility, follows, mutual) {
			continue
		}
		if !listedTo(post.Visibility, post.AuthorID, viewerID) {
			continue
		}
		if membersOnly && post.TierRequiredID == nil {
			continue
		}
		visible = append(visible, post)
	}
	return visible
}

// GetRecentPosts returns recent public posts from all users with engagement counts.
//
// Audit: this explore/recent surface previously went straight from the store
// query to hydration, never calling canViewPosts — the one privacy_gate.go
// choke point every other read path (GetPostsByIDs, GetPostsByAuthor,
// comments, ...) funnels through. That let a private author's posts, and a
// deactivated/pending-delete author's posts (post_hidden_authors), surface here
// even though every other surface correctly hid them. viewerID is now
// threaded through so the same fail-closed gate applies.
// contentTypes narrows the page (legacy spellings normalized); empty = all.
func (s *Service) GetRecentPosts(ctx context.Context, viewerID *uuid.UUID, excludeAuthor *uuid.UUID, contentTypes []string, category string, limit int, cursor string) ([]PostDetail, string, error) {
	posts, nextCursor, err := s.pgStore.GetRecentPosts(ctx, excludeAuthor, contentTypes, category, limit, cursor)
	if err != nil {
		return nil, "", err
	}

	authorSet := make(map[uuid.UUID]struct{}, len(posts))
	authorIDs := make([]uuid.UUID, 0, len(posts))
	for _, p := range posts {
		if viewerID != nil && *viewerID == p.AuthorID {
			continue
		}
		if _, ok := authorSet[p.AuthorID]; !ok {
			authorSet[p.AuthorID] = struct{}{}
			authorIDs = append(authorIDs, p.AuthorID)
		}
	}
	viewableAuthor := s.canViewPosts(ctx, viewerID, authorIDs)

	visible := make([]postgres.Post, 0, len(posts))
	for _, p := range posts {
		if viewerID == nil || *viewerID != p.AuthorID {
			if !viewableAuthor[p.AuthorID] {
				continue
			}
		}
		visible = append(visible, p)
	}
	// Counts and polls for the page in batched reads (list_reads.go).
	details := s.pageDetails(ctx, viewerID, visible)

	// Instant publish (processing.go): overlay live media state, drop what
	// this viewer may not see while it processes.
	details, err = s.attachMediaStateToDetails(ctx, details, viewerID)
	if err != nil {
		return nil, "", err
	}
	// Members-only rows: redacted for this viewer (members_read.go).
	s.redactDetailsForViewer(ctx, viewerID, details)
	return details, nextCursor, nil
}

// GetPostsByIDs returns a map of post_id → PostDetail for the given IDs.
// If viewerID is provided, viewer-specific state (reaction, bookmark) is included.
//
// It is POST /v1/posts/batch, which feed-service hydrates every feed from,
// and the read behind playlists, watch later, continue watching and
// history. Members-only rows are redacted for this viewer on the way out
// (members_read.go), whatever loaded them.
func (s *Service) GetPostsByIDs(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {
	load := s.loadPostsByIDs
	if s.batchPostsSeam != nil {
		load = s.batchPostsSeam
	}
	result, err := load(ctx, ids, viewerID)
	if err != nil {
		return nil, err
	}
	page := make([]*PostDetail, 0, len(result))
	for _, detail := range result {
		page = append(page, detail)
	}
	s.redactDetailPtrsForViewer(ctx, viewerID, page)
	return result, nil
}

// loadPostsByIDs is GetPostsByIDs before the members-only redaction: every
// visibility drop and the viewer-specific state.
func (s *Service) loadPostsByIDs(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {
	posts, err := s.pgStore.GetPostsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	// Instant publish (processing.go): one media_assets round trip for the
	// whole page, overlaid onto each post before the visibility loop.
	postPtrs := make([]*postgres.Post, len(posts))
	for i := range posts {
		postPtrs[i] = &posts[i]
	}
	if err := s.attachMediaState(ctx, postPtrs); err != nil {
		return nil, err
	}

	// Page hydration must stay batch-shaped. The previous loop issued one
	// PostgreSQL query for every bookmark and repost count/state, which merely
	// moved the client's N+1 problem behind the gateway. PostgreSQL-owned state
	// is loaded in a constant number of round trips for the whole page.
	repostCounts, err := s.pgStore.BatchGetRepostCounts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load repost counts: %w", err)
	}
	bookmarks := map[uuid.UUID]bool{}
	activeReposts := map[uuid.UUID]*postgres.Repost{}
	if viewerID != nil {
		bookmarks, err = s.pgStore.BatchIsBookmarked(ctx, *viewerID, ids)
		if err != nil {
			return nil, fmt.Errorf("load viewer bookmarks: %w", err)
		}
		activeReposts, err = s.pgStore.BatchGetActiveReposts(ctx, *viewerID, ids)
		if err != nil {
			return nil, fmt.Errorf("load viewer reposts: %w", err)
		}
	}

	// Scylla partitions reactions and counters by post. The store helper runs
	// those independent point reads with bounded concurrency, so one slow
	// partition cannot turn this into a serial page-length latency chain.
	// A counter or reaction error degrades (zero counts, no reaction,
	// logged) instead of failing the page (2026-10-30 audit B6): every feed
	// hydrates through here, and the bodies are PostgreSQL's.
	countsByPost := s.pageCounts(ctx, ids)
	reactions := map[uuid.UUID]string{}
	if viewerID != nil {
		reactions = s.pageReactions(ctx, ids, *viewerID)
	}

	// Every visibility drop (private accounts, per-post audience, review
	// status, media state, the age gate) in a fixed number of round trips
	// for the page: batchVisibility.
	visible := s.batchVisibility(ctx, posts, viewerID)

	result := make(map[uuid.UUID]*PostDetail, len(posts))
	for _, p := range posts {
		post := p // copy to avoid pointer reuse
		if !visible(&post) {
			continue
		}

		counts := countsByPost[post.ID]
		if counts == nil {
			counts = &scylla.Counts{}
		}

		detail := &PostDetail{
			Post:         &post,
			Counts:       counts,
			RepostCount:  repostCounts[post.ID],
			IsRepostable: repostableContent(post.ContentType, post.Visibility),
		}

		// Enrich with viewer-specific state
		if viewerID != nil {
			// An old emoji reads as the heart (heart_only.go).
			applyViewerReaction(detail, reactions[post.ID])
			detail.IsBookmarked = bookmarks[post.ID]
			if repost := activeReposts[post.ID]; repost != nil {
				detail.HasReposted = true
				detail.ViewerRepost = &RepostStateResult{
					HasReposted: true,
					RepostID:    &repost.ID,
					Type:        repost.RepostType,
					QuoteText:   repost.QuoteText,
					CreatedAt:   repost.CreatedAt.Format(time.RFC3339),
				}
			}
		}

		applyLikeCountPrivacy(detail, viewerID, false)
		result[post.ID] = detail
	}

	// The sounds the page plays: one read and one audience decision for
	// every row (sounds.go). Never a reason to fail the page.
	page := make([]*PostDetail, 0, len(result))
	pagePosts := make([]*postgres.Post, 0, len(result))
	for _, detail := range result {
		page = append(page, detail)
		pagePosts = append(pagePosts, detail.Post)
	}
	// Polls on the page: two batched reads, not two per poll row.
	s.attachPolls(ctx, viewerID, pagePosts)
	s.attachSounds(ctx, viewerID, page)

	return result, nil
}

// batchVisibility is the batch read's per-post drop, shared by
// loadPostsByIDs and viewablePostAuthors (series_visibility.go) so the two
// can never disagree. posts must already carry their media state
// (attachMediaState). One graph round trip for the page's authors, one for
// the follow edges of restricted posts, one age lookup.
func (s *Service) batchVisibility(ctx context.Context, posts []postgres.Post, viewerID *uuid.UUID) func(*postgres.Post) bool {
	// Private accounts: ONE graph round trip for the page's distinct authors
	// (≤100 per call, 3s cache). Fanout wrote these timeline rows without
	// consulting the author's account_visibility, and a follower who was
	// removed — or a viewer who never followed a since-private author —
	// must not read them here. Fail-closed: an unresolved author is dropped.
	authorSet := make(map[uuid.UUID]struct{}, len(posts))
	authorIDs := make([]uuid.UUID, 0, len(posts))
	for _, p := range posts {
		if viewerID != nil && *viewerID == p.AuthorID {
			continue
		}
		if _, ok := authorSet[p.AuthorID]; !ok {
			authorSet[p.AuthorID] = struct{}{}
			authorIDs = append(authorIDs, p.AuthorID)
		}
	}
	viewableAuthor := s.canViewPosts(ctx, viewerID, authorIDs)
	ageOK := s.ageAllowance(ctx, viewerID)
	edges := s.followEdgesOfRestrictedPosts(ctx, posts, viewerID)

	return func(post *postgres.Post) bool {
		if viewerID == nil || *viewerID != post.AuthorID {
			if !viewableAuthor[post.AuthorID] {
				return false
			}
			// Per-post visibility: the same rule as the direct read
			// (viewerMayViewPost), so the batch is never a way round it.
			// This used to drop only `private` and trust the fan-out
			// recipient set for `followers` — but this route is reachable
			// by any signed-in user with a post id, and a timeline row
			// outlives an unfollow whose purge failed. Private sharing
			// stays a single-post gate: a share list is never listed.
			if !batchVisibilityAllowsEdges(post.Visibility, edges[post.AuthorID]) {
				return false
			}
		}

		// reels/posttube item 5: hide non-approved posts (flagged /
		// rejected / pending) from everyone but the author — mirrors the
		// GetPost gate so feed hydration never surfaces moderated-out
		// content even when fanout already wrote a recipient timeline row.
		if post.EffectiveReviewStatus() != "" && !strings.EqualFold(post.EffectiveReviewStatus(), "approved") {
			if viewerID == nil || *viewerID != post.AuthorID {
				return false
			}
		}

		// Instant publish: a post whose media is not yet ready+passed is
		// the author's alone. Feed fanout wrote the follower timeline rows
		// at create time; this is where they stay hidden until the media
		// lands (feed-service mirrors it at its hydration tail).
		if hiddenFromViewer(post, viewerID) {
			return false
		}
		// Creator Hub: an age-restricted post leaves feed hydration for
		// anonymous / under-18 / unknown-age viewers (age_gate.go).
		return ageOK(post)
	}
}

// GetAuthorCounts returns post counts grouped by content type.
// GetAuthorCounts is the ungated count (the admin console's creator view,
// AdminCreatorCounts). Viewer-facing reads go through
// GetAuthorCountsForViewer.
func (s *Service) GetAuthorCounts(ctx context.Context, authorID uuid.UUID) (map[string]int64, error) {
	return s.pgStore.GetPostCountsByAuthor(ctx, authorID)
}

// GetAuthorCountsForViewer is GetAuthorCounts behind the profile's own
// audience rule (security sweep #24): the author always; anyone else only
// if canViewAuthor says so — the same fail-closed answer GetPostsByAuthor
// gives, so a private account's post counts are not readable by a stranger
// whose grid is empty, and a blocked viewer learns nothing. A refused
// viewer gets the empty map (every count zero), never an error.
func (s *Service) GetAuthorCountsForViewer(ctx context.Context, authorID uuid.UUID, viewerID *uuid.UUID) (map[string]int64, error) {
	isAuthor := viewerID != nil && *viewerID == authorID
	if !isAuthor && !s.canViewAuthor(ctx, viewerID, authorID) {
		return map[string]int64{}, nil
	}
	return s.pgStore.GetPostCountsByAuthor(ctx, authorID)
}
