package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/atpost/feed-service/internal/ranking"
	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/atpost/shared/httpclient"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	scyllaStore       *scylla.TimelineStore
	pgStore           *postgres.MetaStore
	rdb               *redis.Client
	graphURL          string
	postServiceURL    string
	profileServiceURL string
	mediaServiceURL   string
	userServiceURL    string
	trustSafetyURL    string
	ranker            *ranking.Ranker
	// Per-upstream HTTP clients with timeouts + circuit breakers. One
	// breaker per remote service so a slow graph-service doesn't open
	// the breaker on post-service calls (H1 risk in arch review plan).
	graphClient   *http.Client
	postClient    *http.Client
	profileClient *http.Client
	mediaClient   *http.Client
	userClient    *http.Client
	trustClient   *http.Client
	// Viewer keyword-filter cache (60s TTL) — see keywordfilter.go.
	kwMu    sync.Mutex
	kwCache map[uuid.UUID]keywordCacheEntry
	// Author privacy (private accounts) cache, 3s TTL per (viewer, author)
	// — see privacyfilter.go. apNow is swapped by tests to drive the TTL.
	apMu    sync.Mutex
	apCache map[string]authorPrivacyEntry
	apNow   func() time.Time
	// lvTiers is the long-video frequency configuration (P0-4), loaded
	// once at construction from defaults + env overrides.
	lvTiers map[string]lvTier
	// feedback is the per-viewer Interested / Not-interested store — the
	// Postgres MetaStore in production, swapped by tests. See feedback.go.
	feedback feedbackStore
}

func New(scylla *scylla.TimelineStore, pg *postgres.MetaStore, rdb *redis.Client) *Service {
	graphURL := os.Getenv("GRAPH_SERVICE_URL")
	if graphURL == "" {
		graphURL = "http://graph-service:8083"
	}
	postServiceURL := os.Getenv("POST_SERVICE_URL")
	if postServiceURL == "" {
		postServiceURL = "http://post-service:8084"
	}
	profileServiceURL := os.Getenv("PROFILE_SERVICE_URL")
	if profileServiceURL == "" {
		profileServiceURL = "http://identity-profile:8098"
	}
	mediaServiceURL := os.Getenv("MEDIA_SERVICE_URL")
	if mediaServiceURL == "" {
		mediaServiceURL = "http://media-service:8087"
	}
	userServiceURL := os.Getenv("USER_SERVICE_URL")
	if userServiceURL == "" {
		userServiceURL = "http://identity-user:8110"
	}
	trustSafetyURL := os.Getenv("TRUST_SAFETY_SERVICE_URL")
	if trustSafetyURL == "" {
		trustSafetyURL = "http://trust-safety-service:8091"
	}
	svc := &Service{
		scyllaStore:       scylla,
		pgStore:           pg,
		rdb:               rdb,
		graphURL:          graphURL,
		postServiceURL:    postServiceURL,
		profileServiceURL: profileServiceURL,
		mediaServiceURL:   mediaServiceURL,
		userServiceURL:    userServiceURL,
		trustSafetyURL:    trustSafetyURL,
		graphClient:       httpclient.NewWithBreaker(5*time.Second, "feed->graph"),
		postClient:        httpclient.NewWithBreaker(5*time.Second, "feed->post"),
		profileClient:     httpclient.NewWithBreaker(5*time.Second, "feed->profile"),
		mediaClient:       httpclient.NewWithBreaker(5*time.Second, "feed->media"),
		userClient:        httpclient.NewWithBreaker(5*time.Second, "feed->user"),
		trustClient:       httpclient.NewWithBreaker(5*time.Second, "feed->trust-safety"),
		kwCache:           make(map[uuid.UUID]keywordCacheEntry),
		lvTiers:           loadLVTiers(),
	}
	// A typed-nil *MetaStore must not become a non-nil interface, or every
	// hydration would fail closed on a nil pool instead of on a real error.
	if pg != nil {
		svc.feedback = pg
	}
	return svc
}

// SetRanker injects the ranking middleware after construction.
func (s *Service) SetRanker(r *ranking.Ranker) {
	s.ranker = r
}

// FeedItem is the API response model
type FeedItem struct {
	PostID      uuid.UUID `json:"post_id"`
	AuthorID    uuid.UUID `json:"author_id"`
	CreatedAt   time.Time `json:"created_at"`
	Score       float64   `json:"score,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	CursorToken string    `json:"-"`
	// PolicyGoverned marks a candidate that could carry a distribution
	// policy (Codex P1-2). Posts created before the policy epoch predate
	// the `posts.distribution` column entirely, so they can never be an
	// opt-out — which lets degraded mode keep them while dropping only
	// genuinely uncertain candidates. Not serialized: internal only.
	PolicyGoverned bool `json:"-"`
	// Source records which path produced the candidate, so hydration can
	// tell the viewer WHY the post is in front of them (reason.go). Empty
	// or sourceTimeline: a fanout row on the viewer's own timeline.
	// sourceColdStart: post-service's recent-public fallback. sourceCircle:
	// the circle_only view. Not serialized: `reason` on the hydrated post
	// is the client-facing form.
	Source string `json:"-"`
}

const (
	sourceTimeline  = "timeline"
	sourceColdStart = "cold_start"
	sourceCircle    = "circle"
)

func (s *Service) GetHomeFeed(ctx context.Context, userID uuid.UUID, limit int, feedMode string, excludeSelf bool, circleOnly bool, followingOnly bool, before *time.Time) ([]FeedItem, error) {
	// Audit HF1: ranking over-fetch was 5x with a 500-row ceiling — each
	// feed request hit Scylla for up to 500 timeline rows and then the
	// ranker did per-post Redis reads on every one (audit HF2). 2.5x is
	// plenty of headroom for blocks/mutes/dedup churn while halving the
	// per-request cost. 200 is the hard ceiling because beyond that the
	// ranker's signal noise dominates the ordering anyway.
	fetchLimit := limit
	if feedMode == "ranked" || feedMode == "shadow" {
		fetchLimit = (limit * 5) / 2
		if fetchLimit > 200 {
			fetchLimit = 200
		}
	} else if excludeSelf {
		fetchLimit = limit + 10 // extra headroom for own posts removed
	}

	// 1. Get Home Timeline candidates
	var items []scylla.FeedItem
	var err error
	if before != nil {
		items, err = s.scyllaStore.GetHomeTimelineBefore(ctx, userID, *before, fetchLimit)
	} else {
		items, err = s.scyllaStore.GetHomeTimeline(ctx, userID, fetchLimit)
	}
	if err != nil {
		return nil, err
	}

	// Convert to FeedItems, optionally filtering out viewer's own original posts.
	// Reposts are kept even when excludeSelf is true — the user wants to see
	// content they reposted (it's someone else's post they chose to amplify).
	candidates := make([]FeedItem, 0, len(items))
	for _, item := range items {
		if excludeSelf && item.AuthorID == userID && item.ContentType != "repost" {
			continue
		}
		candidates = append(candidates, FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
			CursorToken: item.CursorToken,
		})
	}

	// Filter out blocked/muted authors.
	//
	// M2-P0-3: this must FAIL CLOSED. The previous code only filtered when
	// the lookup succeeded, so any graph-service outage served an
	// unfiltered feed — the one moment a blocked person's content reaching
	// their target is most likely to go unnoticed, because nothing in the
	// response says the safety filter was skipped. Returning an error
	// costs an unavailable feed; the alternative costs a safety guarantee.
	blockedMuted, bmErr := s.getBlockedAndMuted(ctx, userID)
	if bmErr != nil {
		return nil, fmt.Errorf("feed unavailable: block/mute state could not be resolved: %w", bmErr)
	}
	blockedSet := blockedSetOf(blockedMuted)
	candidates = applyBlockFilter(candidates, blockedSet)
	candidates = s.applyHiddenAuthorFilter(ctx, candidates)

	// Filter to circle-only (friends) if requested
	if circleOnly && len(candidates) > 0 {
		friends, err := s.fetchCircleMembers(ctx, userID)
		if err != nil {
			log.Printf("circle_only filter: failed to fetch friends for %s: %v", userID, err)
		} else if len(friends) > 0 {
			friendSet := make(map[uuid.UUID]struct{}, len(friends))
			for _, fid := range friends {
				friendSet[fid] = struct{}{}
			}
			filtered := candidates[:0]
			for _, c := range candidates {
				if _, ok := friendSet[c.AuthorID]; ok {
					c.Source = sourceCircle
					filtered = append(filtered, c)
				}
			}
			candidates = filtered
		} else {
			candidates = nil
		}
	}

	// Filter to following-only (one-way follow) if requested.
	// Distinct from circle_only, which is mutual friends. Following matches the
	// "Following" tab semantics: posts authored by users the viewer follows.
	if followingOnly && len(candidates) > 0 {
		following, err := s.fetchFollowing(ctx, userID)
		if err != nil {
			log.Printf("following_only filter: failed to fetch follows for %s: %v", userID, err)
		} else if len(following) > 0 {
			followSet := make(map[uuid.UUID]struct{}, len(following))
			for _, fid := range following {
				followSet[fid] = struct{}{}
			}
			filtered := candidates[:0]
			for _, c := range candidates {
				if _, ok := followSet[c.AuthorID]; ok {
					filtered = append(filtered, c)
				}
			}
			candidates = filtered
		} else {
			candidates = nil
		}
	}

	// Cold-start fallback: if timeline is empty, fetch recent public posts (only for ranked/discovery feeds)
	if before == nil && len(candidates) == 0 && feedMode == "ranked" {
		log.Printf("Cold-start fallback triggered for user %s (empty timeline), fetching from %s", userID, s.postServiceURL)
		coldItems, err := s.getRecentPublicPosts(ctx, limit*2)
		if err != nil {
			log.Printf("Cold-start fallback failed: %v", err)
		} else {
			log.Printf("Cold-start fallback returned %d posts", len(coldItems))
			// M2-P0-3: cold-start candidates come from post-service's
			// recent-public endpoint rather than the viewer's timeline, so
			// they have never passed through any per-viewer filtering.
			// They must go through the SAME block filter as timeline
			// candidates — this is the path most likely to surface a
			// stranger who blocked the viewer, because it is precisely the
			// path used for viewers with no graph of their own.
			for _, item := range s.applyHiddenAuthorFilter(ctx, applyBlockFilter(coldItems, blockedSet)) {
				if excludeSelf && item.AuthorID == userID {
					continue
				}
				candidates = append(candidates, item)
			}
		}
	}

	// P0-1: drop posts whose distribution policy opted out of social home.
	// Runs after the cold-start fallback so fallback items are covered too;
	// server-enforced on every page and mode — the client cannot opt back in.
	candidates = s.filterMainFeedExcluded(ctx, candidates)

	// P0-4: hidden tier is a hard content filter and must apply before
	// ranking so no page or fallback path can leak a long video.
	lvFreq := s.GetLongVideoFrequency(ctx, userID)
	if lvFreq == "hidden" {
		candidates = s.applyLongVideoFrequency(candidates, lvFreq, false)
	}

	// 2. Apply ranking if enabled
	if (feedMode == "ranked" || feedMode == "shadow") && s.ranker != nil && len(candidates) > 0 {
		rc := feedItemsToCandidates(candidates)
		rankedCandidates, err := s.ranker.Rank(ctx, userID, rc, limit)
		if err != nil {
			// Circuit breaker or error: fallback to chronological
			log.Printf("Ranking failed, falling back to chronological: %v", err)
		} else if feedMode == "ranked" {
			candidates = candidatesToFeedItems(rankedCandidates)
		}
		// In shadow mode: log ranked order but return chronological
		if feedMode == "shadow" {
			log.Printf("Shadow mode: ranked %d candidates for user %s", len(rankedCandidates), userID)
		}
	}

	// P0-4: apply the viewer's long-video tier (cold-start candidates
	// included). The hidden tier re-applies harmlessly; reduced/balanced/
	// preferred get the multiplier (ranked mode) + composition target.
	candidates = s.applyLongVideoFrequency(candidates, lvFreq, feedMode == "ranked")

	// 3. Trim to requested limit
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	return candidates, nil
}

// GetFlickFeed returns the first flick page for backward-compatible callers.
func (s *Service) GetFlickFeed(ctx context.Context, userID uuid.UUID, limit int) ([]FeedItem, error) {
	items, _, err := s.GetFlickFeedPage(ctx, userID, limit, "", false)
	return items, err
}

// GetFlickFeedPage returns a timestamp-keyset page. Ranking only reorders the
// fixed chronological window, so no candidate is skipped between pages.
//
// followingOnly is the reels "Following" tab: only reels by authors the
// viewer FOLLOWS (one-way, the social graph — the same meaning as the home
// feed's following_only, NOT the PostTube channel-subscription filter the
// watch surface uses). The viewer's own reels are not "followed" and are
// excluded, matching the home feed.
func (s *Service) GetFlickFeedPage(ctx context.Context, userID uuid.UUID, limit int, before string, followingOnly bool) ([]FeedItem, string, error) {
	target := limit + 1
	items, err := s.scyllaStore.GetHomeTimelineByContentTypesBefore(ctx, userID, []string{"flick", "reel"}, before, target*3)
	if err != nil {
		return nil, "", err
	}

	candidates := make([]FeedItem, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
			CursorToken: item.CursorToken,
		})
	}

	// M2-P0-6: block/mute safety, fail closed. Applied before scoring so
	// no blocked author can occupy a slot in the returned page.
	blocked, err := s.resolveBlockedSet(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	candidates = applyBlockFilter(candidates, blocked)
	candidates = s.applyHiddenAuthorFilter(ctx, candidates)

	// Reels "Following" tab. Fail CLOSED, unlike the home feed's older
	// version of this filter: an unresolved follow graph is an error, never
	// a page of reels from strangers labelled "Following".
	if followingOnly && len(candidates) > 0 {
		following, err := s.fetchFollowing(ctx, userID)
		if err != nil {
			log.Printf("reels following_only: failed to fetch follows for %s: %v", userID, err)
			return nil, "", fmt.Errorf("reels following filter: %w", err)
		}
		candidates = filterByAuthorSet(candidates, following)
	}

	window, next := keysetWindow(candidates, limit)
	return scoreReels(window), next, nil
}

// filterByAuthorSet keeps only candidates whose author is in `authors`. An
// empty set yields an empty page: a viewer who follows nobody has an empty
// Following tab, not their whole feed.
func filterByAuthorSet(candidates []FeedItem, authors []uuid.UUID) []FeedItem {
	if len(authors) == 0 {
		return nil
	}
	set := make(map[uuid.UUID]struct{}, len(authors))
	for _, a := range authors {
		set[a] = struct{}{}
	}
	out := candidates[:0]
	for _, c := range candidates {
		if _, ok := set[c.AuthorID]; ok {
			out = append(out, c)
		}
	}
	return out
}

// GetLongVideoFeed returns the first long-video page for backward-compatible callers.
func (s *Service) GetLongVideoFeed(ctx context.Context, userID uuid.UUID, limit int) ([]FeedItem, error) {
	items, _, err := s.GetLongVideoFeedPage(ctx, userID, limit, "")
	return items, err
}

// GetLongVideoFeedPage returns a ranked timestamp-keyset page.
func (s *Service) GetLongVideoFeedPage(ctx context.Context, userID uuid.UUID, limit int, before string) ([]FeedItem, string, error) {
	target := limit + 1
	items, err := s.scyllaStore.GetHomeTimelineByContentTypesBefore(ctx, userID, []string{"long_video", "video"}, before, target*3)
	if err != nil {
		return nil, "", err
	}

	candidates := make([]FeedItem, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
			CursorToken: item.CursorToken,
		})
	}

	// M2-P0-6: block/mute safety, fail closed, before ranking.
	blocked, err := s.resolveBlockedSet(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	candidates = applyBlockFilter(candidates, blocked)
	candidates = s.applyHiddenAuthorFilter(ctx, candidates)
	candidates, next := keysetWindow(candidates, limit)

	// Discovery fill (Tube, 2026-09-05). The timeline only holds long
	// videos from people the viewer follows, so a new viewer — or one whose
	// follows post no long-form — got an empty Tube. When the FIRST page
	// comes up short, top it up from recent public long videos through the
	// same post-service path the home cold start uses, evaluated as the
	// viewer so post-service's own read rules (private authors, a post
	// still processing is its author's alone, own posts included) hold.
	// The fill passes the SAME block/mute and hidden-author filters as the
	// timeline rows and fails closed with them: a fill error leaves the
	// page as the timeline produced it rather than serving unfiltered
	// strangers. Later pages stay timeline-only, keyed by the cursor.
	if before == "" && len(candidates) < limit {
		viewer := userID
		fill, err := s.getRecentPublicPostsFor(ctx, &viewer, []string{"long_video"}, limit*2)
		if err != nil {
			log.Printf("long video discovery fill failed for %s: %v", userID, err)
		} else {
			fill = s.applyHiddenAuthorFilter(ctx, applyBlockFilter(fill, blocked))
			candidates = mergeDiscoveryFill(candidates, fill, limit)
		}
	}

	if s.ranker != nil && len(candidates) > 0 {
		rc := feedItemsToCandidates(candidates)
		ranked, err := s.ranker.Rank(ctx, userID, rc, limit)
		if err != nil {
			log.Printf("Long video feed ranking failed, fallback to chronological: %v", err)
		} else {
			candidates = candidatesToFeedItems(ranked)
		}
	}

	return candidates, next, nil
}

// GetReelFeed returns the user's reel-only timeline, scored by recency.
// Acts as an alias for GetFlickFeed (backward compat).
func (s *Service) GetReelFeed(ctx context.Context, userID uuid.UUID, limit int) ([]FeedItem, error) {
	items, _, err := s.GetReelFeedPage(ctx, userID, limit, "", false)
	return items, err
}

// GetReelFeedPage is the legacy /reels spelling of the Flick page.
func (s *Service) GetReelFeedPage(ctx context.Context, userID uuid.UUID, limit int, before string, followingOnly bool) ([]FeedItem, string, error) {
	return s.GetFlickFeedPage(ctx, userID, limit, before, followingOnly)
}

// GetVideoFeed returns the user's long-video-only timeline.
// Aliases to GetLongVideoFeed (backward compat).
func (s *Service) GetVideoFeed(ctx context.Context, userID uuid.UUID, limit int, followingOnly bool) ([]FeedItem, error) {
	items, _, err := s.GetVideoFeedPage(ctx, userID, limit, "", followingOnly)
	return items, err
}

// GetVideoFeedPage is the paginated watch surface. followingOnly retains its
// legacy wire name but still means channel subscriptions only.
func (s *Service) GetVideoFeedPage(ctx context.Context, userID uuid.UUID, limit int, before string, followingOnly bool) ([]FeedItem, string, error) {
	target := limit + 1
	items, err := s.scyllaStore.GetHomeTimelineByContentTypesBefore(ctx, userID, []string{"long_video", "video"}, before, target*3)
	if err != nil {
		return nil, "", err
	}

	candidates := make([]FeedItem, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
			CursorToken: item.CursorToken,
		})
	}

	// M2-P0-6: block/mute safety, fail closed. Runs before the
	// subscriptions filter and the ranker so no later step can reintroduce
	// a blocked author.
	blocked, err := s.resolveBlockedSet(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	candidates = applyBlockFilter(candidates, blocked)
	candidates = s.applyHiddenAuthorFilter(ctx, candidates)

	// Subscriptions filter (Module 1 P0-3): the PostTube Subscriptions tab
	// is driven by real CHANNEL SUBSCRIPTIONS, not the social follow
	// graph. Following someone does not put their long video here, and
	// subscribing does not require following them. The parameter keeps
	// its wire name (`following_only`) for old clients, but the meaning is
	// now "subscriptions only" — there is no follow fallback: a viewer
	// with zero subscriptions sees an empty tab, not their follow feed.
	if followingOnly && len(candidates) > 0 {
		subscribed, err := s.fetchSubscribedCreators(ctx, userID)
		if err != nil {
			// Fail closed: showing follow-graph videos here would
			// silently reintroduce the exact conflation P0-3 removes.
			log.Printf("video feed subscriptions: failed to fetch subscriptions for %s: %v", userID, err)
			return nil, "", err
		}
		if len(subscribed) > 0 {
			subSet := make(map[uuid.UUID]struct{}, len(subscribed))
			for _, cid := range subscribed {
				subSet[cid] = struct{}{}
			}
			filtered := candidates[:0]
			for _, c := range candidates {
				if _, ok := subSet[c.AuthorID]; ok {
					filtered = append(filtered, c)
				}
			}
			candidates = filtered
		} else {
			candidates = nil
		}
	}
	candidates, next := keysetWindow(candidates, limit)

	// Long-video feed uses the main ranker with full signals
	if s.ranker != nil && len(candidates) > 0 {
		rc := feedItemsToCandidates(candidates)
		ranked, err := s.ranker.Rank(ctx, userID, rc, limit)
		if err != nil {
			log.Printf("Video feed ranking failed, fallback to chronological: %v", err)
		} else {
			candidates = candidatesToFeedItems(ranked)
		}
	}

	return candidates, next, nil
}

func keysetWindow(items []FeedItem, limit int) ([]FeedItem, string) {
	if limit <= 0 || len(items) == 0 {
		return []FeedItem{}, ""
	}
	if len(items) <= limit {
		return items, ""
	}
	window := make([]FeedItem, limit)
	copy(window, items[:limit])
	return window, window[len(window)-1].CursorToken
}

// scoreReels applies a pure recency score to reel candidates.
// score = 1.0 / (1.0 + ageMinutes * 0.01)
// This gives strong preference to content < 2 hours old without
// completely suppressing older content.
func scoreReels(items []FeedItem) []FeedItem {
	now := time.Now()
	scored := make([]FeedItem, len(items))
	copy(scored, items)
	for i := range scored {
		ageMin := now.Sub(scored[i].CreatedAt).Minutes()
		scored[i].Score = 1.0 / (1.0 + ageMin*0.01)
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	return scored
}

// GetUserFeedMode returns the user's saved feed mode preference.
func (s *Service) GetUserFeedMode(ctx context.Context, userID uuid.UUID) string {
	// Check Redis cache first
	cached, err := s.rdb.Get(ctx, fmt.Sprintf("feed:pref:%s", userID.String())).Result()
	if err == nil && cached != "" {
		return cached
	}

	// Check Postgres
	mode, err := s.pgStore.GetFeedMode(ctx, userID)
	if err != nil || mode == "" {
		return "chronological"
	}

	// Cache for 5 minutes
	s.rdb.Set(ctx, fmt.Sprintf("feed:pref:%s", userID.String()), mode, 5*time.Minute)
	return mode
}

// SetUserFeedMode persists the user's feed mode preference.
func (s *Service) SetUserFeedMode(ctx context.Context, userID uuid.UUID, mode string) error {
	if err := s.pgStore.SetFeedMode(ctx, userID, mode); err != nil {
		return err
	}
	// Update cache
	s.rdb.Set(ctx, fmt.Sprintf("feed:pref:%s", userID.String()), mode, 5*time.Minute)
	return nil
}

// RecordSignal handles "see_less" / "see_more" user signals.
func (s *Service) RecordSignal(ctx context.Context, userID, postID uuid.UUID, signal string) error {
	return s.pgStore.RecordSignal(ctx, userID, postID, signal)
}

// IsCelebAuthor exposes the celeb check to the Kafka consumer so it
// can short-circuit follow-backfill for pull-model authors (audit HF6).
func (s *Service) IsCelebAuthor(ctx context.Context, authorID uuid.UUID) (bool, error) {
	return s.pgStore.IsCeleb(ctx, authorID)
}

// DebugFeed returns full score breakdown for the user's feed candidates.
func (s *Service) DebugFeed(ctx context.Context, userID uuid.UUID) (interface{}, error) {
	items, err := s.scyllaStore.GetHomeTimeline(ctx, userID, 100)
	if err != nil {
		return nil, err
	}

	candidates := make([]FeedItem, len(items))
	for i, item := range items {
		candidates[i] = FeedItem{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			ContentType: item.ContentType,
		}
	}

	if s.ranker == nil {
		return map[string]interface{}{
			"candidates": candidates,
			"mode":       "no_ranker",
		}, nil
	}

	rc := feedItemsToCandidates(candidates)
	rankedCandidates, err := s.ranker.Rank(ctx, userID, rc, 20)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"candidates_count": len(candidates),
		"ranked":           candidatesToFeedItems(rankedCandidates),
	}, nil
}

func (s *Service) FanoutPost(ctx context.Context, postID, authorID uuid.UUID, createdAt time.Time, contentType, visibility string) error {
	// 1. Always add to Author Timeline
	if err := s.scyllaStore.AddToAuthorTimeline(ctx, authorID, postID, createdAt, contentType); err != nil {
		return err
	}

	// 2. Also add to Author's own Home Timeline (so they see their own posts)
	if err := s.scyllaStore.AddToHomeTimeline(ctx, authorID, postID, authorID, createdAt, contentType); err != nil {
		log.Printf("Failed to push to author's own home timeline: %v", err)
	}

	// 2b. Trusted ("close friends") audience: fan out only to the author's
	// Trusted Circle — not followers, not circle members — and only if the
	// author has the close-friends-posts feature on (friends-sheets spec §3.3,
	// §11 step 12). Independent of celeb status: the circle is small (≤10) so
	// a push is always cheap, and the pull model would not reliably surface a
	// restricted-audience post.
	if visibility == "trusted" {
		if !s.closeFriendsPostsEnabled(ctx, authorID) {
			return nil // toggle off — post stays on the author's own timeline
		}
		closeFriends, err := s.fetchCloseFriends(ctx, authorID)
		if err != nil {
			log.Printf("Failed to fetch close friends for trusted fanout: %v", err)
			return nil
		}
		for _, recipientID := range closeFriends {
			if recipientID == authorID {
				continue // already pushed above
			}
			if err := s.scyllaStore.AddToHomeTimeline(ctx, recipientID, postID, authorID, createdAt, contentType); err != nil {
				log.Printf("Failed to push trusted post to timeline for user %s: %v", recipientID, err)
			}
		}
		return nil
	}

	// 3. Check Celeb Status
	isCeleb, err := s.pgStore.IsCeleb(ctx, authorID)
	if err != nil {
		return err
	}

	if isCeleb {
		// Stop here (Pull model for celebs)
		return nil
	}

	// 4. Collect unique recipient IDs from followers + circle members.
	//
	// Audit HF5: previously the follower fetch and the circle-member
	// fetch ran serially even though they hit independent services
	// (graph-service vs profile-service). Run them in parallel — for
	// a non-celeb author with 5k followers + 200 friends, the wall
	// clock used to be ~50 graph pages + ~1 profile call back-to-back;
	// now those overlap.
	recipientSet := make(map[uuid.UUID]struct{})

	type fetchResult struct {
		ids []uuid.UUID
		err error
		tag string
	}
	results := make(chan fetchResult, 2)
	go func() {
		ids, err := s.fetchFollowers(ctx, authorID)
		results <- fetchResult{ids: ids, err: err, tag: "followers"}
	}()
	go func() {
		ids, err := s.fetchCircleMembers(ctx, authorID)
		results <- fetchResult{ids: ids, err: err, tag: "circle"}
	}()
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			log.Printf("Failed to fetch %s for fanout: %v", r.tag, r.err)
			continue
		}
		for _, id := range r.ids {
			recipientSet[id] = struct{}{}
		}
	}

	// 5. Push to all recipients' Home Timelines.
	//
	// Audit CF4: previously a sequential loop — a non-celeb author
	// with 100k followers blocked the Kafka consumer goroutine on
	// 100k serial Scylla writes. Real "celeb" status (gates the pull
	// model) is already short-circuited above; this path is the
	// "almost-celeb" tier. Parallelize through a bounded worker pool
	// so total wall-clock is concurrency-bounded but per-event Scylla
	// load can't explode beyond the worker count.
	const fanoutWorkers = 16
	recipientCh := make(chan uuid.UUID, fanoutWorkers*4)
	var fanoutWG sync.WaitGroup
	for w := 0; w < fanoutWorkers; w++ {
		fanoutWG.Add(1)
		go func() {
			defer fanoutWG.Done()
			for recipientID := range recipientCh {
				if err := s.scyllaStore.AddToHomeTimeline(ctx, recipientID, postID, authorID, createdAt, contentType); err != nil {
					log.Printf("Failed to push to timeline for user %s: %v", recipientID, err)
				}
			}
		}()
	}
	for recipientID := range recipientSet {
		if recipientID == authorID {
			continue // already pushed above
		}
		recipientCh <- recipientID
	}
	close(recipientCh)
	fanoutWG.Wait()
	return nil
}

// resolveBlockedSet fetches the viewer's suppression set and FAILS CLOSED
// (Module 2 M2-P0-6).
//
// Every feed surface that returns other people's content must call this
// before returning anything. Only the main home feed used to: the reel,
// flick, long-video and video tabs read the timeline and returned it with
// no block filtering at all, so a blocked author's posts reached the very
// person who blocked them through any of those tabs.
//
// Returning an error here makes the tab unavailable during a
// graph-service outage. That is the intended trade — the alternative is
// an unfiltered tab that looks completely normal.
func (s *Service) resolveBlockedSet(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	ids, err := s.getBlockedAndMuted(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("feed unavailable: block/mute state could not be resolved: %w", err)
	}
	return blockedSetOf(ids), nil
}

// blockedSetOf builds a lookup set from the graph-service response.
// Returns nil when there is nothing to exclude.
func blockedSetOf(ids []uuid.UUID) map[uuid.UUID]struct{} {
	if len(ids) == 0 {
		return nil
	}
	set := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

// applyBlockFilter drops every item authored by a user in the block set
// (Module 2 M2-P0-3).
//
// Extracted so the timeline path and the cold-start fallback provably run
// the same filter. They used to duplicate the logic inline, which is how
// the two paths drift: a change to one is easy to make without noticing
// the other exists.
func applyBlockFilter(items []FeedItem, blocked map[uuid.UUID]struct{}) []FeedItem {
	if len(blocked) == 0 || len(items) == 0 {
		return items
	}
	out := items[:0]
	for _, it := range items {
		if _, isBlocked := blocked[it.AuthorID]; isBlocked {
			continue
		}
		out = append(out, it)
	}
	return out
}

// getBlockedAndMuted calls graph-service to get the union of blocked and muted user IDs for userID.
func (s *Service) getBlockedAndMuted(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	url := fmt.Sprintf("%s/v1/internal/graph/blocked-and-muted?user_id=%s", s.graphURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// Forward the internal-service key so graph-service's CG2 gate accepts
	// the request — without it the call 401s and block/mute filtering
	// silently no-ops (blocked/muted authors leak back into the feed).
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := s.graphClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// M2-P0-3: the status code was previously ignored. A 401 from the
	// internal-key gate, or a 500 from graph-service, decoded to an empty
	// list and returned a nil error — so the caller filtered against
	// nothing and every blocked and muted author flowed straight back
	// into the feed, with no error anywhere to notice it by.
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("graph-service blocked-and-muted returned %d: %s",
			resp.StatusCode, string(body))
	}
	var result struct {
		UserIDs []uuid.UUID `json:"user_ids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		// A malformed body is indistinguishable from "nobody is blocked"
		// once decoded, so it must be an error rather than an empty set.
		return nil, fmt.Errorf("decode blocked-and-muted: %w", err)
	}
	return result.UserIDs, nil
}

// fetchCloseFriends calls graph-service for a user's Trusted Circle. The
// close-friends endpoint resolves its subject from X-User-Id, so the call
// acts as the author. Returns the close-friend user IDs.
func (s *Service) fetchCloseFriends(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	url := fmt.Sprintf("%s/v1/graph/close-friends", s.graphURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-User-Id", userID.String())
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := s.graphClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph-service returned %d: %s", resp.StatusCode, string(body))
	}
	var envelope struct {
		Data []uuid.UUID `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal close friends: %w", err)
	}
	return envelope.Data, nil
}

// closeFriendsPostsEnabled reports whether the author has the close-friends-
// posts feature on (usr.user_settings.tc_close_friends_posts). Fail-open: a
// user-service blip must not silently drop a post the author chose to make
// "trusted".
func (s *Service) closeFriendsPostsEnabled(ctx context.Context, userID uuid.UUID) bool {
	url := fmt.Sprintf("%s/v1/users/%s/settings", s.userServiceURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return true
	}
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := s.userClient.Do(req)
	if err != nil {
		return true // fail-open
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return true // fail-open
	}
	var envelope struct {
		Data struct {
			TcCloseFriendsPosts bool `json:"tc_close_friends_posts"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return true // fail-open
	}
	return envelope.Data.TcCloseFriendsPosts
}

// fetchFollowers calls graph-service to get the follower list for a user.
// It paginates through all results (max 100 per page).
func (s *Service) fetchFollowers(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	var allFollowers []uuid.UUID
	offset := 0
	limit := 100

	for {
		url := fmt.Sprintf("%s/v1/graph/followers/%s?limit=%d&offset=%d", s.graphURL, userID.String(), limit, offset)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		// Forward the internal-service key so graph-service's CG2 gate
		// accepts the request when configured.
		if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}

		resp, err := s.graphClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("graph-service request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("graph-service returned %d: %s", resp.StatusCode, string(body))
		}

		var envelope struct {
			Data []uuid.UUID `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, fmt.Errorf("unmarshal followers: %w", err)
		}

		allFollowers = append(allFollowers, envelope.Data...)

		// If we got fewer than limit, we've fetched all pages
		if len(envelope.Data) < limit {
			break
		}
		offset += limit
	}

	return allFollowers, nil
}

// fetchFollowing calls graph-service for the list of user IDs the viewer
// follows (one-way), used by the home feed's following_only filter.
func (s *Service) fetchFollowing(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	var allFollowing []uuid.UUID
	offset := 0
	limit := 100

	for {
		url := fmt.Sprintf("%s/v1/graph/following/%s?limit=%d&offset=%d", s.graphURL, userID.String(), limit, offset)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		// Forward the internal-service key so graph-service's CG2 gate
		// accepts the request when configured.
		if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}

		resp, err := s.graphClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("graph-service request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("graph-service returned %d: %s", resp.StatusCode, string(body))
		}

		var envelope struct {
			Data []uuid.UUID `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, fmt.Errorf("unmarshal following: %w", err)
		}

		allFollowing = append(allFollowing, envelope.Data...)

		if len(envelope.Data) < limit {
			break
		}
		offset += limit
	}

	return allFollowing, nil
}

// fetchCircleMembers returns the author's connections ("friends"). The friend
// system was consolidated onto graph-service, so this reads
// GET /v1/graph/connections/{userId} — the single source of truth the apps
// use — NOT the retired profile-service friends endpoint (which now returns
// nothing, so a post never reached the author's friends). Mirrors fetchFollowers.
func (s *Service) fetchCircleMembers(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	var allMembers []uuid.UUID
	offset := 0
	limit := 100

	for {
		url := fmt.Sprintf("%s/v1/graph/connections/%s?limit=%d&offset=%d", s.graphURL, userID.String(), limit, offset)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		// Forward the internal-service key so graph-service's CG2 gate
		// accepts the request when configured.
		if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}

		resp, err := s.graphClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("graph-service request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("graph-service returned %d: %s", resp.StatusCode, string(body))
		}

		var envelope struct {
			Data []uuid.UUID `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, fmt.Errorf("unmarshal connections: %w", err)
		}

		allMembers = append(allMembers, envelope.Data...)

		if len(envelope.Data) < limit {
			break
		}
		offset += limit
	}

	return allMembers, nil
}

// getRecentPublicPosts fetches recent public posts from post-service as a cold-start fallback
// for users with an empty home timeline (new users, no follows, etc.).
func (s *Service) getRecentPublicPosts(ctx context.Context, limit int) ([]FeedItem, error) {
	return s.getRecentPublicPostsFor(ctx, nil, nil, limit)
}

// getRecentPublicPostsFor is the discovery source behind the cold-start
// fills: post-service's recent-public page, optionally narrowed to content
// types and evaluated AS the viewer. With a viewer, post-service applies its
// own read rules for that person: private authors are dropped, a post still
// processing is returned only to its author, and the viewer's own posts are
// included. Without one it behaves as an anonymous read.
func (s *Service) getRecentPublicPostsFor(ctx context.Context, viewerID *uuid.UUID, contentTypes []string, limit int) ([]FeedItem, error) {
	url := fmt.Sprintf("%s/v1/posts/recent?limit=%d", s.postServiceURL, limit)
	if len(contentTypes) > 0 {
		url += "&content_type=" + strings.Join(contentTypes, ",")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Service-Key", os.Getenv("INTERNAL_SERVICE_KEY"))
	if viewerID != nil {
		req.Header.Set("X-User-Id", viewerID.String())
	}

	resp, err := s.postClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post-service request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("post-service returned %d: %s", resp.StatusCode, string(body))
	}

	var envelope struct {
		Data []struct {
			ID          string    `json:"id"`
			AuthorID    string    `json:"author_id"`
			CreatedAt   time.Time `json:"created_at"`
			ContentType string    `json:"content_type"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	items := make([]FeedItem, 0, len(envelope.Data))
	for _, p := range envelope.Data {
		postID, err := uuid.Parse(p.ID)
		if err != nil {
			continue
		}
		authorID, err := uuid.Parse(p.AuthorID)
		if err != nil {
			continue
		}
		ct := p.ContentType
		if ct == "" {
			ct = "post"
		}
		items = append(items, FeedItem{
			PostID:      postID,
			AuthorID:    authorID,
			CreatedAt:   p.CreatedAt,
			ContentType: ct,
			Source:      sourceColdStart,
		})
	}
	return items, nil
}

// feedItemsToCandidates converts service FeedItems to ranking Candidates.
func feedItemsToCandidates(items []FeedItem) []ranking.Candidate {
	out := make([]ranking.Candidate, len(items))
	for i, item := range items {
		out[i] = ranking.Candidate{
			PostID:      item.PostID,
			AuthorID:    item.AuthorID,
			CreatedAt:   item.CreatedAt,
			Score:       item.Score,
			ContentType: item.ContentType,
			Source:      item.Source,
		}
	}
	return out
}

// FanoutQuestion writes a Q&A question into the author's followers' home
// timelines using content_type = "qa_question". Mirrors FanoutPost but
// without the celeb/visibility short-circuits — Q&A questions always
// fan out to followers (community-scoped questions are filtered at read
// time by the feed hydrator, which respects community visibility).
func (s *Service) FanoutQuestion(ctx context.Context, questionID, authorID uuid.UUID, createdAt time.Time) error {
	const ct = "qa_question"

	// 1. Author timeline + author's own home timeline
	if err := s.scyllaStore.AddToAuthorTimeline(ctx, authorID, questionID, createdAt, ct); err != nil {
		return err
	}
	if err := s.scyllaStore.AddToHomeTimeline(ctx, authorID, questionID, authorID, createdAt, ct); err != nil {
		log.Printf("FanoutQuestion: failed to push to author's home timeline: %v", err)
	}

	// 2. Stop early for celebs (pull model — same rule as FanoutPost).
	isCeleb, err := s.pgStore.IsCeleb(ctx, authorID)
	if err == nil && isCeleb {
		return nil
	}

	// 3. Followers + circle members.
	recipientSet := make(map[uuid.UUID]struct{})
	if followerIDs, err := s.fetchFollowers(ctx, authorID); err == nil {
		for _, id := range followerIDs {
			recipientSet[id] = struct{}{}
		}
	} else {
		log.Printf("FanoutQuestion: fetch followers failed: %v", err)
	}
	if friendIDs, err := s.fetchCircleMembers(ctx, authorID); err == nil {
		for _, id := range friendIDs {
			recipientSet[id] = struct{}{}
		}
	} else {
		log.Printf("FanoutQuestion: fetch circle members failed: %v", err)
	}

	for recipientID := range recipientSet {
		if recipientID == authorID {
			continue
		}
		if err := s.scyllaStore.AddToHomeTimeline(ctx, recipientID, questionID, authorID, createdAt, ct); err != nil {
			log.Printf("FanoutQuestion: push to timeline for user %s failed: %v", recipientID, err)
		}
	}
	return nil
}

// MarkQuestionDeleted soft-removes a Q&A question from feed hydration by
// flipping a Redis flag identical to the post-deleted pattern.
func (s *Service) MarkQuestionDeleted(ctx context.Context, questionID uuid.UUID) error {
	deletedKey := fmt.Sprintf("post:deleted:%s", questionID)
	return s.rdb.Set(ctx, deletedKey, "1", 24*time.Hour).Err()
}

// FanoutRepost distributes a repost into the reposter's followers' home timelines.
// The feed entry points to the original post but is attributed to the reposter.
func (s *Service) FanoutRepost(ctx context.Context, repostID, originalPostID, reposterID uuid.UUID, createdAt time.Time, visibility string) error {
	// 1. Add to reposter's own home timeline so they see it
	if err := s.scyllaStore.AddToHomeTimeline(ctx, reposterID, originalPostID, reposterID, createdAt, "repost"); err != nil {
		log.Printf("Failed to push repost to reposter's home timeline: %v", err)
	}

	// 2. Check celeb status — if celeb, stop (pull model)
	isCeleb, err := s.pgStore.IsCeleb(ctx, reposterID)
	if err != nil {
		return err
	}
	if isCeleb {
		return nil
	}

	// 3. Only fan out public/default visibility reposts
	if visibility == "private" {
		return nil
	}

	// 4. Collect followers + friends
	recipientSet := make(map[uuid.UUID]struct{})

	followerIDs, err := s.fetchFollowers(ctx, reposterID)
	if err != nil {
		log.Printf("Failed to fetch followers for repost fanout: %v", err)
	} else {
		for _, id := range followerIDs {
			recipientSet[id] = struct{}{}
		}
	}

	friendIDs, err := s.fetchCircleMembers(ctx, reposterID)
	if err != nil {
		log.Printf("Failed to fetch circle members for repost fanout: %v", err)
	} else {
		for _, id := range friendIDs {
			recipientSet[id] = struct{}{}
		}
	}

	// 5. Push to all recipients' home timelines
	for recipientID := range recipientSet {
		if recipientID == reposterID {
			continue
		}
		if err := s.scyllaStore.AddToHomeTimeline(ctx, recipientID, originalPostID, reposterID, createdAt, "repost"); err != nil {
			log.Printf("Failed to push repost to timeline for user %s: %v", recipientID, err)
		}
	}

	return nil
}

// UndoRepostFanout marks a repost as deleted in Redis so feed hydration skips it.
func (s *Service) UndoRepostFanout(ctx context.Context, repostID, originalPostID uuid.UUID) error {
	// Mark repost as deleted in Redis for 24h — feed hydration will filter it out
	deletedKey := fmt.Sprintf("repost:deleted:%s", repostID)
	if err := s.rdb.Set(ctx, deletedKey, "1", 24*time.Hour).Err(); err != nil {
		log.Printf("Failed to mark repost deleted in Redis: %v", err)
	}
	return nil
}

// candidatesToFeedItems converts ranking Candidates back to service FeedItems.
func candidatesToFeedItems(candidates []ranking.Candidate) []FeedItem {
	out := make([]FeedItem, len(candidates))
	for i, c := range candidates {
		out[i] = FeedItem{
			PostID:      c.PostID,
			AuthorID:    c.AuthorID,
			CreatedAt:   c.CreatedAt,
			Score:       c.Score,
			ContentType: c.ContentType,
			Source:      c.Source,
		}
	}
	return out
}
