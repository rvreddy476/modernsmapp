package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
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
	// analyticsServiceURL serves the visible view count on every hydrated
	// post (view_counts.go). Fail-open: unreachable means 0 and a warning.
	analyticsServiceURL string
	ranker              *ranking.Ranker
	// Per-upstream HTTP clients with timeouts + circuit breakers. One
	// breaker per remote service so a slow graph-service doesn't open
	// the breaker on post-service calls (H1 risk in arch review plan).
	graphClient   *http.Client
	postClient    *http.Client
	profileClient *http.Client
	mediaClient   *http.Client
	userClient    *http.Client
	trustClient   *http.Client
	// analyticsClient reads view counts; vcCache is the 60 s in-process
	// cache in front of it — see view_counts.go.
	analyticsClient *http.Client
	vcMu            sync.Mutex
	vcCache         map[uuid.UUID]viewCountEntry
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
	// timelines and celebs are what FanoutPost writes to and asks: the
	// Scylla TimelineStore and the Postgres MetaStore in production,
	// swapped by tests so the fan-out legs can be exercised without a
	// live cluster. Every other path still reads the concrete stores.
	timelines timelineWriter
	celebs    celebStore
}

// timelineWriter is the slice of the Scylla store FanoutPost needs.
type timelineWriter interface {
	AddToAuthorTimeline(ctx context.Context, authorID uuid.UUID, postID uuid.UUID, createdAt time.Time, contentType string) error
	AddToHomeTimeline(ctx context.Context, userID uuid.UUID, postID, authorID uuid.UUID, createdAt time.Time, contentType string) error
}

// celebStore answers the pull-model question FanoutPost gates on.
type celebStore interface {
	IsCeleb(ctx context.Context, authorID uuid.UUID) (bool, error)
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
	analyticsServiceURL := os.Getenv("ANALYTICS_SERVICE_URL")
	if analyticsServiceURL == "" {
		analyticsServiceURL = "http://analytics-service:8094"
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
		// The view count is decoration on a page, not the page: a short
		// timeout and its own breaker, so a slow analytics-service costs
		// the feed nothing but zeros.
		analyticsServiceURL: analyticsServiceURL,
		analyticsClient:     httpclient.NewWithBreaker(2*time.Second, "feed->analytics"),
		graphClient:         httpclient.NewWithBreaker(5*time.Second, "feed->graph"),
		postClient:          httpclient.NewWithBreaker(5*time.Second, "feed->post"),
		profileClient:       httpclient.NewWithBreaker(5*time.Second, "feed->profile"),
		mediaClient:         httpclient.NewWithBreaker(5*time.Second, "feed->media"),
		userClient:          httpclient.NewWithBreaker(5*time.Second, "feed->user"),
		trustClient:         httpclient.NewWithBreaker(5*time.Second, "feed->trust-safety"),
		kwCache:             make(map[uuid.UUID]keywordCacheEntry),
		lvTiers:             loadLVTiers(),
	}
	// A typed-nil *MetaStore must not become a non-nil interface, or every
	// hydration would fail closed on a nil pool instead of on a real error.
	if pg != nil {
		svc.feedback = pg
		svc.celebs = pg
	}
	if scylla != nil {
		svc.timelines = scylla
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
	//
	// A fanout row (empty / sourceTimeline) says only that some fanout once
	// targeted this viewer — NOT that they follow the author. FanoutPost
	// writes to the author's connections as well as their followers, and
	// nothing retracts a row on unfollow. reason.go therefore asks the
	// graph rather than reading a follow off this field.
	Source string `json:"-"`
}

const (
	sourceTimeline  = "timeline"
	sourceColdStart = "cold_start"
	sourceCircle    = "circle"
	// The related-videos surface's two specific sources (related.go).
	// They exist so the up-next list can say WHY each row is there —
	// "more from this creator" reads very differently from "suggested for
	// you", and the client has no other way to tell them apart.
	sourceRelatedAuthor = "related_author"
	sourceRelatedTopic  = "related_topic"
)

// HomeFeedResult is one page of the home feed plus the reason it is empty,
// when it is. Three different situations used to reach the client as the
// same bare `[]`: the viewer follows nobody, the people they follow have
// posted nothing recently, and the feed failed. The third is now always an
// error status (the narrowing filters below fail closed), and EmptyReason
// separates the first two.
type HomeFeedResult struct {
	Items []FeedItem
	// EmptyReason is set ONLY when Items is empty. It is surfaced as the
	// X-Feed-Empty-Reason response header rather than as a body field:
	// the shipped Android client decodes the shared `data`/`meta`
	// envelope, `meta` is the platform-wide struct shared by every
	// service, and an empty feed must stay a plain `[]` for it. A header
	// is additive for every existing client and invisible to one that
	// does not read it.
	EmptyReason string
}

// The values of HomeFeedResult.EmptyReason.
const (
	// EmptyNoFollows: following_only, and the viewer follows nobody. The
	// client should offer accounts to follow, not "nothing new".
	EmptyNoFollows = "no_follows"
	// EmptyNoConnections: circle_only, and the viewer has no connections.
	EmptyNoConnections = "no_connections"
	// EmptyNoRecentPosts: the graph is non-empty (or was not consulted)
	// and simply produced nothing for this page.
	EmptyNoRecentPosts = "no_recent_posts"
)

// coldStartAllowed reports whether the cold-start backfill of recommended
// public posts may run for this request.
//
// It exists as a named predicate because the condition it replaced was
// spelled inline and silently omitted the narrowing flags: a viewer who
// follows nobody, asking explicitly for only the people they follow, was
// served recommended strangers under the Following heading with nothing in
// the response to reveal the substitution.
//
// The backfill itself is deliberately kept — a brand-new account with no
// follows would otherwise land on an empty front door, and the web client
// asks for `ranked` for exactly that reason. It is only forbidden when the
// caller narrowed the request, where an empty page is the honest answer.
func coldStartAllowed(before *time.Time, feedMode string, candidateCount int, circleOnly, followingOnly bool) bool {
	if circleOnly || followingOnly {
		return false // an explicit narrowing is never backfilled
	}
	return before == nil && candidateCount == 0 && feedMode == "ranked"
}

func (s *Service) GetHomeFeed(ctx context.Context, userID uuid.UUID, limit int, feedMode string, excludeSelf bool, circleOnly bool, followingOnly bool, before *time.Time) (HomeFeedResult, error) {
	// Why this page might come back empty, filled in by the narrowing
	// filters below and reported only if it actually is.
	emptyReason := ""

	// Refresh the viewer's mutual-follow set if it is missing or stale.
	// Non-blocking and detached — this request is scored with whatever is
	// already there. See mutuals.go.
	s.warmViewerSignals(ctx, userID)

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
		return HomeFeedResult{}, err
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
		return HomeFeedResult{}, fmt.Errorf("feed unavailable: block/mute state could not be resolved: %w", bmErr)
	}
	blockedSet := blockedSetOf(blockedMuted)
	candidates = applyBlockFilter(candidates, blockedSet)
	candidates = s.applyHiddenAuthorFilter(ctx, candidates)

	// Filter to circle-only (friends) if requested.
	//
	// Resolved even when the timeline came back empty, unlike the version
	// that only ran on a non-empty candidate list: "you have no
	// connections" and "your connections have posted nothing recently" are
	// different answers, and the empty reason below is the only place the
	// client can learn which. Fails CLOSED, like the reels and watch
	// Following tabs: an unresolved graph is an error, never a page under
	// a heading that promises friends.
	if circleOnly {
		friends, err := s.fetchCircleMembers(ctx, userID)
		if err != nil {
			log.Printf("circle_only filter: failed to fetch friends for %s: %v", userID, err)
			return HomeFeedResult{}, fmt.Errorf("home circle filter: %w", err)
		}
		if len(friends) == 0 {
			emptyReason = EmptyNoConnections
		}
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
	}

	// Filter to following-only (one-way follow) if requested.
	// Distinct from circle_only, which is mutual friends. Following matches the
	// "Following" tab semantics: posts authored by users the viewer follows.
	// Same two rules as circle_only above: resolved unconditionally, and
	// failing closed rather than serving the unfiltered timeline.
	if followingOnly {
		following, err := s.fetchFollowing(ctx, userID)
		if err != nil {
			log.Printf("following_only filter: failed to fetch follows for %s: %v", userID, err)
			return HomeFeedResult{}, fmt.Errorf("home following filter: %w", err)
		}
		if len(following) == 0 && emptyReason == "" {
			emptyReason = EmptyNoFollows
		}
		candidates = filterByAuthorSet(candidates, following)
	}

	// Cold-start fallback: if the timeline is empty, fetch recent public
	// posts. See coldStartAllowed — in particular, an explicit narrowing
	// (following_only / circle_only) forbids it, because backfilling
	// strangers under a heading that promises follows is a lie the client
	// has no way to detect.
	if coldStartAllowed(before, feedMode, len(candidates), circleOnly, followingOnly) {
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

	if len(candidates) == 0 {
		if emptyReason == "" {
			emptyReason = EmptyNoRecentPosts
		}
		return HomeFeedResult{Items: candidates, EmptyReason: emptyReason}, nil
	}
	return HomeFeedResult{Items: candidates}, nil
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
// feed's and the watch feed's following_only). The viewer's own reels are
// not "followed" and are excluded, matching the home feed.
func (s *Service) GetFlickFeedPage(ctx context.Context, userID uuid.UUID, limit int, before string, followingOnly bool) ([]FeedItem, string, error) {
	s.warmViewerSignals(ctx, userID) // see mutuals.go
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
	if followingOnly {
		candidates, err = s.applyFollowingFilter(ctx, userID, candidates, "reels")
		if err != nil {
			return nil, "", err
		}
	}

	window, next := keysetWindow(candidates, limit)
	return scoreReels(window), next, nil
}

// applyFollowingFilter narrows candidates to authors the viewer FOLLOWS
// (graph-service, one-way) — the shared meaning of following_only on the
// reels and watch surfaces. Fails CLOSED: an unresolved follow graph is an
// error, never a page of strangers labelled "Following". An empty
// candidate set short-circuits without a graph round trip. `surface`
// names the caller in the log line and the wrapped error.
func (s *Service) applyFollowingFilter(ctx context.Context, userID uuid.UUID, candidates []FeedItem, surface string) ([]FeedItem, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}
	following, err := s.fetchFollowing(ctx, userID)
	if err != nil {
		log.Printf("%s following_only: failed to fetch follows for %s: %v", surface, userID, err)
		return nil, fmt.Errorf("%s following filter: %w", surface, err)
	}
	return filterByAuthorSet(candidates, following), nil
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
	items, _, err := s.GetLongVideoFeedPage(ctx, userID, limit, "", false, false)
	return items, err
}

// GetLongVideoFeedPage returns a ranked timestamp-keyset page.
//
// followingOnly has the same meaning as everywhere else on this handler:
// only long videos by authors the viewer follows. /v1/feed/videos used to
// accept the parameter and silently drop it, so a client that narrowed the
// request got the whole surface back — including the discovery fill's
// recommended strangers — with nothing in the response to say the
// narrowing had been ignored.
//
// subscribedOnly is the Tube Subscriptions tab: only long videos by
// channel owners the viewer subscribes to (post-service, subscriptions.go),
// newest first with no ranker and no discovery fill. The two flags are
// distinct narrowings and the handler refuses both at once.
func (s *Service) GetLongVideoFeedPage(ctx context.Context, userID uuid.UUID, limit int, before string, followingOnly, subscribedOnly bool) ([]FeedItem, string, error) {
	candidates, next, blocked, err := s.videoTimelineWindow(ctx, userID, limit, before, followingOnly, subscribedOnly)
	if err != nil {
		return nil, "", err
	}

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
	//
	// followingOnly forbids it outright, for the same reason the home
	// feed's cold start is forbidden under a narrowing: a short page is
	// the honest answer to "only the people I follow", and topping it up
	// with recommendations is a substitution the client cannot see.
	// subscribedOnly is a narrowing in exactly the same sense.
	if discoveryFillAllowed(followingOnly || subscribedOnly, before, len(candidates), limit) {
		fill, err := s.longVideoDiscoveryFill(ctx, userID, blocked, "", limit*2)
		if err != nil {
			log.Printf("long video discovery fill failed for %s: %v", userID, err)
		} else {
			candidates = mergeDiscoveryFill(candidates, fill, limit)
		}
	}

	// The Subscriptions tab is chronological by decision: the window is
	// already newest first, and reordering it would turn "what my channels
	// posted, in order" into another ranked surface.
	if subscribedOnly {
		return candidates, next, nil
	}
	return s.rankVideoWindow(ctx, userID, candidates, limit, "Long video feed"), next, nil
}

// videoTimelineWindow is the keyset window both Tube surfaces read: the
// viewer's long-form timeline rows, block/mute and hidden-author filtered
// (M2-P0-6, fail closed), optionally narrowed to authors the viewer follows
// (followingOnly — the watch "Following" tab, resolved exactly as the reels
// tab is), cut to `limit` with the cursor of the row after it.
// Chronological and unfilled — ranking and the discovery fill are the
// callers' business, so the category path (category.go) can pull several
// windows and rank once. The resolved block set is returned so a caller's
// fill can pass the same filter without a second graph round trip.
// subscribedOnly narrows to channel owners the viewer subscribes to
// (post-service, cached in Redis; subscriptions.go), with the same
// fail-closed rule as followingOnly.
func (s *Service) videoTimelineWindow(ctx context.Context, userID uuid.UUID, limit int, before string, followingOnly, subscribedOnly bool) ([]FeedItem, string, map[uuid.UUID]struct{}, error) {
	s.warmViewerSignals(ctx, userID) // see mutuals.go
	target := limit + 1
	items, err := s.scyllaStore.GetHomeTimelineByContentTypesBefore(ctx, userID, []string{"long_video", "video"}, before, target*3)
	if err != nil {
		return nil, "", nil, err
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
	// following filter and the ranker so no later step can reintroduce
	// a blocked author.
	blocked, err := s.resolveBlockedSet(ctx, userID)
	if err != nil {
		return nil, "", nil, err
	}
	candidates = applyBlockFilter(candidates, blocked)
	candidates = s.applyHiddenAuthorFilter(ctx, candidates)

	// Watch "Following" tab: authors the viewer follows, the social graph
	// (graph-service) — the same set and the same fail-closed rule as the
	// reels Following tab. A viewer who follows nobody sees an empty tab,
	// never their whole feed; an unresolved graph is an error, never a
	// page of strangers.
	if followingOnly {
		candidates, err = s.applyFollowingFilter(ctx, userID, candidates, "watch")
		if err != nil {
			return nil, "", nil, err
		}
	}

	// Tube "Subscriptions" tab: channel owners the viewer subscribes to,
	// per post-service. Same rules as the Following tab: an empty
	// subscription list is an empty tab, and an unresolved one is an
	// error, never a page of strangers under a heading that promises
	// subscribed channels.
	if subscribedOnly {
		candidates, err = s.applySubscribedFilter(ctx, userID, candidates, "watch")
		if err != nil {
			return nil, "", nil, err
		}
	}
	candidates, next := keysetWindow(candidates, limit)
	return candidates, next, blocked, nil
}

// discoveryFillAllowed reports whether the Tube first-page discovery fill
// may top a short page up with recommended public long videos.
//
// The sibling of coldStartAllowed, and forbidden for the same reason: a
// narrowed request ("only the people I follow") is answered with what the
// narrowing produced, however short, never topped up with strangers. The
// category surface adds its own condition — the timeline must be exhausted,
// not merely out of window budget — on top of this one.
func discoveryFillAllowed(followingOnly bool, before string, have, limit int) bool {
	if followingOnly {
		return false // an explicit narrowing is never filled
	}
	return before == "" && have < limit
}

// longVideoDiscoveryFill is the recent-public long-video source behind the
// Tube first-page fill, evaluated as the viewer and passed through the SAME
// block/mute and hidden-author filters as the timeline rows. `category`
// narrows it at post-service (empty = any).
func (s *Service) longVideoDiscoveryFill(ctx context.Context, userID uuid.UUID, blocked map[uuid.UUID]struct{}, category string, limit int) ([]FeedItem, error) {
	viewer := userID
	fill, err := s.getRecentPublicPostsFor(ctx, &viewer, []string{"long_video"}, category, limit)
	if err != nil {
		return nil, err
	}
	return s.applyHiddenAuthorFilter(ctx, applyBlockFilter(fill, blocked)), nil
}

// rankVideoWindow runs the main ranker over one fixed window (full
// signals), falling back to the chronological order on error.
func (s *Service) rankVideoWindow(ctx context.Context, userID uuid.UUID, candidates []FeedItem, limit int, surface string) []FeedItem {
	if s.ranker == nil || len(candidates) == 0 {
		return candidates
	}
	ranked, err := s.ranker.Rank(ctx, userID, feedItemsToCandidates(candidates), limit)
	if err != nil {
		log.Printf("%s ranking failed, fallback to chronological: %v", surface, err)
		return candidates
	}
	return candidatesToFeedItems(ranked)
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
	items, _, err := s.GetVideoFeedPage(ctx, userID, limit, "", followingOnly, false)
	return items, err
}

// GetVideoFeedPage is the paginated watch surface. followingOnly is the
// watch "Following" tab: only long videos by authors the viewer follows
// (graph-service), resolved exactly as the reels Following tab is.
// subscribedOnly is the "Subscriptions" tab: channel owners the viewer
// subscribes to, newest first, no ranker (see GetLongVideoFeedPage).
func (s *Service) GetVideoFeedPage(ctx context.Context, userID uuid.UUID, limit int, before string, followingOnly, subscribedOnly bool) ([]FeedItem, string, error) {
	candidates, next, _, err := s.videoTimelineWindow(ctx, userID, limit, before, followingOnly, subscribedOnly)
	if err != nil {
		return nil, "", err
	}
	if subscribedOnly {
		return candidates, next, nil // chronological by decision
	}
	// Long-video feed uses the main ranker with full signals
	return s.rankVideoWindow(ctx, userID, candidates, limit, "Video feed"), next, nil
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

// FanoutPost writes a new post into the timelines that should show it.
//
// Who receives a row: the author (own timeline and home), then for a
// "trusted" post the author's close friends only, otherwise the author's
// FOLLOWERS UNION CONNECTIONS unless the author is a celeb (pull model:
// nothing pushed, the read path fetches the author timeline). Long videos
// add one more leg: the SUBSCRIBERS of the post's Tube channel
// (channelID, from PostCreatedPayload.ChannelID; uuid.Nil skips the leg).
// That leg runs even for a celeb, deliberately: the celeb short-circuit
// exists because a million followers is too many rows to push, but a
// subscription is an explicit "show me every upload" that the
// Subscriptions tab reads straight off the home timeline, so a subscriber
// must hold the row whatever the pull model does for followers. A
// subscriber who is also a follower or connection gets one row, not two.
//
// unfollow_purge.go describes which of these rows an unfollow may delete;
// a subscriber's row carries no provenance either, and post-service makes
// a subscribe a follow edge as well, so an unsubscribe reaches this
// service as an unfollow.
func (s *Service) FanoutPost(ctx context.Context, postID, authorID uuid.UUID, createdAt time.Time, contentType, visibility string, channelID uuid.UUID) error {
	// 1. Always add to Author Timeline
	if err := s.timelines.AddToAuthorTimeline(ctx, authorID, postID, createdAt, contentType); err != nil {
		return err
	}

	// 2. Also add to Author's own Home Timeline (so they see their own posts)
	if err := s.timelines.AddToHomeTimeline(ctx, authorID, postID, authorID, createdAt, contentType); err != nil {
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
			if err := s.timelines.AddToHomeTimeline(ctx, recipientID, postID, authorID, createdAt, contentType); err != nil {
				log.Printf("Failed to push trusted post to timeline for user %s: %v", recipientID, err)
			}
		}
		return nil
	}

	// 3. Check Celeb Status
	isCeleb, err := s.celebs.IsCeleb(ctx, authorID)
	if err != nil {
		return err
	}

	// 4. Collect unique recipient IDs from followers + circle members.
	//
	// Audit HF5: previously the follower fetch and the circle-member
	// fetch ran serially even though they hit independent services
	// (graph-service vs profile-service). Run them in parallel — for
	// a non-celeb author with 5k followers + 200 friends, the wall
	// clock used to be ~50 graph pages + ~1 profile call back-to-back;
	// now those overlap.
	//
	// Skipped entirely for a celeb (pull model): the set stays empty and
	// only the subscriber leg below can add anyone.
	recipientSet := make(map[uuid.UUID]struct{})
	if !isCeleb {
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
				if err := s.timelines.AddToHomeTimeline(ctx, recipientID, postID, authorID, createdAt, contentType); err != nil {
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

	// 6. Subscriber leg (long videos with a channel): the channel's
	// subscribers, through the same bounded pool, skipping anyone the
	// follower/circle set already covered. The pages stream straight into
	// the pool rather than being collected first, so a channel with many
	// subscribers costs memory proportional to one page, not to the
	// channel. A post-service failure here is logged, not returned: the
	// follower rows are already in flight and Kafka redelivery would
	// duplicate them (AddToHomeTimeline is not idempotent; see
	// scylla/timelines.go), which is worse than a subscriber missing
	// one upload from the tab.
	if channelID != uuid.Nil && isLongVideoContentType(contentType) {
		err := s.eachChannelSubscriber(ctx, channelID, func(subscriberID uuid.UUID) {
			if subscriberID == authorID {
				return
			}
			if _, covered := recipientSet[subscriberID]; covered {
				return
			}
			recipientSet[subscriberID] = struct{}{} // a subscriber listed twice is still one row
			recipientCh <- subscriberID
		})
		if err != nil {
			log.Printf("Subscriber fanout for channel %s (post %s) incomplete: %v", channelID, postID, err)
		}
	}

	close(recipientCh)
	fanoutWG.Wait()
	return nil
}

// isLongVideoContentType names the two spellings the catalogue uses for
// a Tube upload (post-service normalises "video" to "long_video" on write;
// older rows and producers still say "video").
func isLongVideoContentType(contentType string) bool {
	return contentType == "long_video" || contentType == "video"
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
	return s.getRecentPublicPostsFor(ctx, nil, nil, "", limit)
}

// getRecentPublicPostsFor is the discovery source behind the cold-start
// fills: post-service's recent-public page, optionally narrowed to content
// types and evaluated AS the viewer. With a viewer, post-service applies its
// own read rules for that person: private authors are dropped, a post still
// processing is returned only to its author, and the viewer's own posts are
// included. Without one it behaves as an anonymous read. A non-empty
// `category` asks post-service for that taxonomy id only (Tube category
// filter, 2026-09-05); it is already a validated slug by the time it gets
// here, so it goes on the query string escaped but otherwise verbatim.
func (s *Service) getRecentPublicPostsFor(ctx context.Context, viewerID *uuid.UUID, contentTypes []string, category string, limit int) ([]FeedItem, error) {
	url := fmt.Sprintf("%s/v1/posts/recent?limit=%d", s.postServiceURL, limit)
	if len(contentTypes) > 0 {
		url += "&content_type=" + strings.Join(contentTypes, ",")
	}
	if category != "" {
		url += "&category=" + neturl.QueryEscape(category)
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
