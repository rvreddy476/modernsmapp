package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"time"

	"github.com/atpost/feed-service/internal/pipeline"
	"github.com/atpost/feed-service/internal/ranking"
	"github.com/google/uuid"
)

// Related videos — "what plays next".
//
// Tube had no up-next of any kind: when a video ended there was nothing to
// go to, on any surface. This is that endpoint.
//
// It is deliberately not a second recommender. The candidates go through
// the same block and mute filters as the feed, the same ranker, the same
// scoring formula, the same diversity pass and the same hydration tail —
// the only things that differ are where the candidates come from and one
// extra scoring term (ranking.SeedRelatedness) that says "this is like the
// thing you are watching". Everything a feed surface has learned about
// safety therefore applies here for free, and cannot be forgotten here
// separately.
//
// THE GUARD RAILS, AND WHERE EACH ONE IS ENFORCED
//
// suggestion-service's block_safety.go documents what happens when a
// recommendation surface filters at generation time instead of at egress:
// every cache-hit path bypasses the filter, and the product recommends
// people you blocked. The lesson taken from it here is that each rule is
// enforced on the ONE path everything funnels through, not sprinkled over
// the branches that collect candidates.
//
//	the viewer's own posts  — dropped in collectRelatedCandidates, and the
//	                          seed author's own listing is skipped entirely
//	                          when the viewer is the author
//	blocked / muted users   — applyBlockFilter + applyHiddenAuthorFilter on
//	                          the assembled pool, fail closed: an
//	                          unresolvable block set is an error, never an
//	                          unfiltered page
//	"don't recommend this
//	 account" / "not
//	 interested"            — applyFeedbackFilter, inside HydratePosts,
//	                          fail closed
//	private / processing /
//	 scheduled / keyword-
//	 filtered posts         — post-service's viewer-scoped reads plus the
//	                          rest of the hydration tail
//	already finished        — ranking.ExcludeSeen, from the completions set
//	the seed itself         — ranking.ExcludeSeen, and again at collection
//
// THE REEL / LONG-VIDEO SPLIT
//
// A related list never crosses the two families. Asked about a long video
// it returns long videos; asked about a reel it returns reels. This is
// enforced by content type at every collection call and re-checked on the
// assembled pool, because "a long video must never surface in Reels" is
// exactly the kind of rule a new endpoint quietly breaks.
//
// (Worth recording: the split is enforced by content_type, not by
// duration, and the duration boundary in shared/postclassify is 300
// seconds, not the 90 that gets quoted. content_type is the authoritative
// answer — post-service classifies once on upload and the timeline stores
// the result — so nothing here needs to know the threshold. But the two
// numbers should be reconciled somewhere.)

// ErrRelatedUnsupported: the seed is not a video, so "what plays next" has
// no meaning for it.
var ErrRelatedUnsupported = errors.New("related videos are only defined for video posts")

const (
	// relatedPoolLimit bounds how many candidates one request assembles
	// before ranking. Related lists are consumed a page or two deep; a
	// pool an order of magnitude larger than the first page is plenty and
	// keeps the post-service fan-out to three bounded calls.
	relatedPoolLimit = 150

	// relatedAuthorFetch / relatedTopicFetch / relatedRecentFetch are the
	// per-source caps. The creator's own back catalogue is the most
	// reliable source of "more like this", so it gets the largest share;
	// the recency fallback is there to keep the list from being empty for
	// a brand-new creator with no category, not to fill it.
	relatedAuthorFetch = 60
	relatedTopicFetch  = 60
	relatedRecentFetch = 60
)

// relatedFamily maps a content type onto the surface family a related list
// must stay inside. The empty string means "not a video".
func relatedFamily(contentType string) string {
	switch contentType {
	case "long_video", "video":
		return "long_video"
	case "flick", "reel":
		return "flick"
	}
	return ""
}

// GetRelatedVideos returns what should play after `postID` for this viewer.
//
// `offset` is how far into the ranked pool this page starts — see
// RelatedCursor in the HTTP layer for why the cursor is an offset here
// rather than the timeline timeuuid the other ranked surfaces use.
//
// Returns the page and the offset to continue from (0 when the pool is
// exhausted, so the caller emits no cursor).
func (s *Service) GetRelatedVideos(ctx context.Context, viewerID, postID uuid.UUID, limit, offset int) ([]HydratedPost, int, error) {
	if limit <= 0 {
		return []HydratedPost{}, 0, nil
	}

	// The seed is fetched through post-service's viewer-scoped batch —
	// the same call hydration makes — so "related to a post you cannot
	// see" is a not-found rather than a leak. lookupPost is shared with
	// the feedback path for exactly that reason.
	seedPost, err := s.lookupPost(ctx, viewerID, postID)
	if err != nil {
		return nil, 0, err
	}
	family := relatedFamily(seedPost.ContentType)
	if family == "" {
		return nil, 0, ErrRelatedUnsupported
	}

	seedTopics := pipeline.TopicTokens(seedPost.Category, decodeHashtags(seedPost.Hashtags), seedPost.Tags)
	seed := &ranking.Seed{
		PostID:   postID,
		AuthorID: seedPost.AuthorID,
		Topics:   seedTopics,
	}

	// Warming the mutual-follow set here too: the related surface reads
	// the same social-proximity signal the feeds do, and someone who
	// arrives from a shared link may not have loaded a feed this session.
	s.warmViewerSignals(ctx, viewerID)

	pool, err := s.collectRelatedCandidates(ctx, viewerID, seedPost, family)
	if err != nil {
		return nil, 0, err
	}
	if len(pool) == 0 {
		return []HydratedPost{}, 0, nil
	}

	// Block / mute, fail closed — before ranking, so no blocked author can
	// occupy a slot, exactly as the feed surfaces do it.
	blocked, err := s.resolveBlockedSet(ctx, viewerID)
	if err != nil {
		return nil, 0, err
	}
	pool = applyBlockFilter(pool, blocked)
	pool = s.applyHiddenAuthorFilter(ctx, pool)

	ranked := pool
	if s.ranker != nil {
		out, err := s.ranker.RankRelated(ctx, viewerID, seed, feedItemsToCandidates(pool), len(pool))
		if err != nil {
			// Same policy as every other surface: a ranking failure
			// degrades to the collection order rather than failing the
			// request. The order is then "creator's work, then same
			// topic, then recent", which is a defensible up-next on its
			// own — but the seed and completed videos would no longer be
			// excluded by the ranker, so that is done here too.
			log.Printf("related videos ranking failed for %s, falling back to collection order: %v", postID, err)
			ranked = excludeByID(pool, postID)
		} else {
			ranked = candidatesToFeedItems(out)
		}
	} else {
		ranked = excludeByID(pool, postID)
	}

	// Hydrate forward from the offset until the page is full. Hydration is
	// where the remaining viewer-specific filters live (not-interested,
	// muted authors, keyword filters, private accounts, still-processing
	// posts) and any of them can drop a row, so the page is assembled by
	// consuming the ranked list rather than by slicing it — the same way
	// the category surface assembles its pages.
	page := make([]HydratedPost, 0, limit)
	cursor := offset
	if cursor < 0 {
		cursor = 0
	}
	for cursor < len(ranked) && len(page) < limit {
		end := cursor + limit
		if end > len(ranked) {
			end = len(ranked)
		}
		chunk := ranked[cursor:end]
		hydrated, err := s.HydratePosts(ctx, chunk, viewerID)
		if err != nil {
			return nil, 0, err
		}
		// HydratePosts may reorder and may drop; re-impose the ranked
		// order over what survived so the page reflects the ranking.
		hydrated = orderByCandidates(hydrated, chunk)
		for _, h := range hydrated {
			if len(page) >= limit {
				break
			}
			page = append(page, h)
		}
		cursor = end
	}

	next := cursor
	if cursor >= len(ranked) {
		next = 0 // pool exhausted: no continuation
	}
	return page, next, nil
}

// collectRelatedCandidates assembles the pool from three post-service
// reads, in priority order.
//
// Every one of them goes through post-service AS the viewer rather than
// reading `posts` directly, so post-service's own read rules hold —
// private authors dropped, a still-processing post visible only to its
// author, a scheduled post invisible to everyone. Reading the table
// directly would be faster and would quietly re-implement all of that,
// badly. The cold-start fill on the Tube feed already made this choice for
// the same reason; this follows it.
//
// A failure in the FIRST source is fatal, because a related list without
// the creator's own other work is not a related list. Failures in the
// other two are logged and skipped: a narrower list is better than an
// error page.
func (s *Service) collectRelatedCandidates(ctx context.Context, viewerID uuid.UUID, seed *HydratedPost, family string) ([]FeedItem, error) {
	seen := map[uuid.UUID]struct{}{seed.ID: {}}
	pool := make([]FeedItem, 0, relatedPoolLimit)

	keep := func(items []FeedItem, source string) {
		for _, it := range items {
			if len(pool) >= relatedPoolLimit {
				return
			}
			if _, dup := seen[it.PostID]; dup {
				continue
			}
			// Never recommend a viewer their own content. Dropped here
			// rather than left to a later filter because this is the one
			// place the whole pool passes through, and because the
			// affinity model deliberately never learns from a creator
			// watching themselves either.
			if it.AuthorID == viewerID {
				continue
			}
			// The reel / long-video split, re-checked on the assembled
			// pool. Each source was already asked for one family; this is
			// the belt to that pair of braces, because a long video
			// leaking into a reel list is the failure that matters.
			if relatedFamily(it.ContentType) != family {
				continue
			}
			seen[it.PostID] = struct{}{}
			it.Source = source
			pool = append(pool, it)
		}
	}

	// 1. The same creator's other work. Skipped when the viewer IS the
	// creator — every row would be dropped by the self-check above, so
	// the call would be a guaranteed waste of a round trip.
	if seed.AuthorID != viewerID {
		byAuthor, err := s.fetchPostsByAuthor(ctx, viewerID, seed.AuthorID, family, relatedAuthorFetch)
		if err != nil {
			return nil, fmt.Errorf("related: author catalogue: %w", err)
		}
		keep(byAuthor, sourceRelatedAuthor)
	}

	// 2. The same topic. Only the category is queryable — post-service
	// exposes `category` on /v1/posts/recent, and a hashtag listing exists
	// but is not viewer-scoped, so it is not used here. This is the
	// practical ceiling of the topical signal today and the reason the
	// third source has to exist.
	if seed.Category != "" {
		byTopic, err := s.getRecentPublicPostsFor(ctx, &viewerID, []string{family}, seed.Category, relatedTopicFetch)
		if err != nil {
			log.Printf("related: category source failed for %s: %v", seed.ID, err)
		} else {
			keep(byTopic, sourceRelatedTopic)
		}
	}

	// 3. Recent public video of the same family. Without this a video by a
	// one-upload creator with no category would have an empty up-next,
	// which is the state Tube is in today.
	recent, err := s.getRecentPublicPostsFor(ctx, &viewerID, []string{family}, "", relatedRecentFetch)
	if err != nil {
		log.Printf("related: recent source failed for %s: %v", seed.ID, err)
	} else {
		keep(recent, sourceColdStart)
	}

	return pool, nil
}

// fetchPostsByAuthor reads one author's catalogue through post-service,
// evaluated as the viewer. `contentType` maps onto post-service's `type`
// query parameter (which is single-valued and named differently from
// /v1/posts/recent's `content_type` — an inconsistency in post-service,
// mirrored here rather than papered over).
func (s *Service) fetchPostsByAuthor(ctx context.Context, viewerID, authorID uuid.UUID, contentType string, limit int) ([]FeedItem, error) {
	url := fmt.Sprintf("%s/v1/posts/by-author/%s?limit=%d", s.postServiceURL, authorID.String(), limit)
	if contentType != "" {
		url += "&type=" + neturl.QueryEscape(contentType)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Service-Key", os.Getenv("INTERNAL_SERVICE_KEY"))
	req.Header.Set("X-User-Id", viewerID.String())

	resp, err := s.postClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post-service request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
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
		return nil, fmt.Errorf("decode by-author response: %w", err)
	}

	items := make([]FeedItem, 0, len(envelope.Data))
	for _, p := range envelope.Data {
		postID, err := uuid.Parse(p.ID)
		if err != nil {
			continue
		}
		aid, err := uuid.Parse(p.AuthorID)
		if err != nil {
			continue
		}
		items = append(items, FeedItem{
			PostID:      postID,
			AuthorID:    aid,
			CreatedAt:   p.CreatedAt,
			ContentType: p.ContentType,
		})
	}
	return items, nil
}

// decodeHashtags reads the hashtag list off a hydrated post. It arrives as
// raw JSON because HydratedPost passes post-service's field through
// untouched; an unparseable value means no hashtags rather than an error,
// since a missing topic token only ever costs a little ranking quality.
func decodeHashtags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return nil
	}
	return tags
}

// excludeByID drops one post from a candidate list. Used on the ranker
// bypass paths, where ranking.ExcludeSeen has not run and the seed would
// otherwise be offered as the thing to play after itself.
func excludeByID(items []FeedItem, drop uuid.UUID) []FeedItem {
	out := make([]FeedItem, 0, len(items))
	for _, it := range items {
		if it.PostID == drop {
			continue
		}
		out = append(out, it)
	}
	return out
}

// orderByCandidates re-imposes the candidate order on a hydrated slice.
// HydratePosts is free to reorder and to drop; a row it dropped simply
// does not appear, and a row it returned that was not asked for is
// ignored.
func orderByCandidates(posts []HydratedPost, order []FeedItem) []HydratedPost {
	if len(posts) <= 1 {
		return posts
	}
	byID := make(map[uuid.UUID]HydratedPost, len(posts))
	for _, p := range posts {
		byID[p.ID] = p
	}
	out := make([]HydratedPost, 0, len(posts))
	for _, it := range order {
		if p, ok := byID[it.PostID]; ok {
			out = append(out, p)
			delete(byID, it.PostID)
		}
	}
	return out
}
