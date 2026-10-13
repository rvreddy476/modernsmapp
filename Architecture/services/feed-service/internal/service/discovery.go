package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Out-of-network discovery on the ranked For You page (audit F-29).
//
// For You used to be the viewer's timeline and nothing else once it held
// anything: post-service's recent-public posts reached a viewer only through
// the cold-start fill, i.e. only while they followed nobody who posted. A
// viewer who followed three quiet accounts saw those three accounts, forever.
//
// Now each ranked, un-narrowed home page gives a share of its slots
// (FEED_DISCOVERY_SHARE, default 0.2; 0 turns it off) to posts from people
// the viewer does NOT follow, best engagement velocity first
// (post:velocity:ranked, the score the ranker already reads), then newest.
//
// What keeps this safe and the cursor honest:
//   - discovery items go through the same filters as the cold-start fill —
//     blocks and mutes, hidden authors (fail closed: no discovery rather
//     than an unfiltered one), the main-feed distribution policy, reels and
//     the hidden long-video tier — and hydration applies post-service's
//     per-viewer read rules and the listing-visibility filter to them like
//     every other item;
//   - they carry no CursorToken and are added AFTER homeWindow cuts the
//     page, which is cut that much shorter, so the timeline cursor is
//     exactly what it would be without them;
//   - each one is remembered per viewer for 48 h, so the same trending post
//     is not offered again on every page and every refresh;
//   - Following and Friends (narrowed) and chronological mode get none.
//
// They are marked sourceColdStart, so the item says why it is there
// ("Trending now" / "Suggested for you", reason.go).

const (
	discoveryPoolKey  = "feed:discovery:pool"
	discoveryPoolTTL  = 2 * time.Minute
	discoveryPoolSize = 100
	discoverySeenTTL  = 48 * time.Hour
	followingCacheTTL = 5 * time.Minute
	// maxDiscoveryShare caps FEED_DISCOVERY_SHARE: For You stays mostly
	// the viewer's own network.
	maxDiscoveryShare = 0.5
)

type discoveryConfig struct {
	share float64
}

func loadDiscoveryConfig() discoveryConfig {
	c := discoveryConfig{share: 0.2}
	if raw := os.Getenv("FEED_DISCOVERY_SHARE"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			c.share = f
		}
	}
	if c.share < 0 || math.IsNaN(c.share) {
		c.share = 0
	}
	if c.share > maxDiscoveryShare {
		c.share = maxDiscoveryShare
	}
	return c
}

// FollowingKey is the Redis key of a viewer's cached following list. The
// follow and unfollow consumers drop it.
func FollowingKey(viewerID uuid.UUID) string {
	return fmt.Sprintf("feed:following:%s", viewerID)
}

func discoverySeenKey(viewerID uuid.UUID) string {
	return fmt.Sprintf("feed:discovery:seen:%s", viewerID)
}

// discoveryAllowed: the ranked For You page only. A narrowed tab promises
// follows or friends, and the chronological feed promises the timeline.
func discoveryAllowed(feedMode string, circleOnly, followingOnly bool) bool {
	return feedMode == "ranked" && !circleOnly && !followingOnly
}

// discoverySlots is how many of a page of `limit` go to discovery: the
// share, rounded, at least one, never more than half. A page of one stays
// the viewer's network.
func discoverySlots(limit int, share float64) int {
	if share <= 0 || limit < 2 {
		return 0
	}
	n := int(math.Round(float64(limit) * share))
	if n < 1 {
		n = 1
	}
	if n > limit/2 {
		n = limit / 2
	}
	return n
}

// hasColdStart reports whether the cold-start fill already ran for this
// page: it is discovery for a viewer with nothing else to read.
func hasColdStart(items []FeedItem) bool {
	for _, it := range items {
		if it.Source == sourceColdStart {
			return true
		}
	}
	return false
}

// rankDiscovery orders the pool for discovery: drop what exclude rejects,
// then highest velocity first, newest first among equals. Pure.
func rankDiscovery(pool []FeedItem, velocity map[uuid.UUID]float64, exclude func(FeedItem) bool) []FeedItem {
	out := make([]FeedItem, 0, len(pool))
	seen := make(map[uuid.UUID]struct{}, len(pool))
	for _, p := range pool {
		if _, dup := seen[p.PostID]; dup || exclude(p) {
			continue
		}
		seen[p.PostID] = struct{}{}
		p.Source = sourceColdStart
		p.CursorToken = ""
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		vi, vj := velocity[out[i].PostID], velocity[out[j].PostID]
		if vi != vj {
			return vi > vj
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// discoveryFor returns up to n out-of-network posts for this viewer's page,
// already filtered, and remembers them as offered. page is every timeline
// candidate this request read; none of those is offered again. Any lookup
// failing means no discovery on this page, never an unfiltered one.
func (s *Service) discoveryFor(ctx context.Context, viewerID uuid.UUID, page []FeedItem, n int, blocked map[uuid.UUID]struct{}, longVideoHidden bool) []FeedItem {
	if n <= 0 || s.rdb == nil {
		return nil
	}
	pool, err := s.discoveryPool(ctx)
	if err != nil {
		log.Printf("discovery: pool unavailable, page served without it: %v", err)
		return nil
	}
	if len(pool) == 0 {
		return nil
	}
	following, err := s.followingSet(ctx, viewerID)
	if err != nil {
		log.Printf("discovery: following set unavailable for %s, page served without it: %v", viewerID, err)
		return nil
	}

	ids := make([]string, len(pool))
	for i, p := range pool {
		ids[i] = p.PostID.String()
	}
	velocity := make(map[uuid.UUID]float64, len(pool))
	if scores, err := s.rdb.ZMScore(ctx, "post:velocity:ranked", ids...).Result(); err == nil && len(scores) == len(pool) {
		for i, p := range pool {
			velocity[p.PostID] = scores[i]
		}
	}
	members, err := s.rdb.SMembers(ctx, discoverySeenKey(viewerID)).Result()
	if err != nil {
		log.Printf("discovery: offered set unavailable for %s, page served without it: %v", viewerID, err)
		return nil
	}
	offered := make(map[uuid.UUID]struct{}, len(members))
	for _, m := range members {
		if id, err := uuid.Parse(m); err == nil {
			offered[id] = struct{}{}
		}
	}
	inPage := make(map[uuid.UUID]struct{}, len(page))
	for _, p := range page {
		inPage[p.PostID] = struct{}{}
	}

	ranked := rankDiscovery(pool, velocity, func(p FeedItem) bool {
		if p.AuthorID == viewerID {
			return true
		}
		if _, ok := following[p.AuthorID]; ok {
			return true
		}
		if _, ok := blocked[p.AuthorID]; ok {
			return true
		}
		if _, ok := inPage[p.PostID]; ok {
			return true
		}
		_, ok := offered[p.PostID]
		return ok
	})
	if len(ranked) == 0 {
		return nil
	}
	ranked, err = s.applyHiddenAuthorFilter(ctx, ranked)
	if err != nil {
		return nil
	}
	ranked = dropShortForm(ranked)
	if s.pgStore != nil { // the distribution policy lives in Postgres
		ranked = s.filterMainFeedExcluded(ctx, ranked)
	}
	if longVideoHidden {
		ranked = s.applyLongVideoFrequency(ranked, "hidden", false)
	}
	if len(ranked) > n {
		ranked = ranked[:n]
	}
	if len(ranked) == 0 {
		return nil
	}

	served := make([]interface{}, len(ranked))
	for i, r := range ranked {
		served[i] = r.PostID.String()
	}
	pipe := s.rdb.Pipeline()
	pipe.SAdd(ctx, discoverySeenKey(viewerID), served...)
	pipe.Expire(ctx, discoverySeenKey(viewerID), discoverySeenTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("discovery: could not remember offered posts for %s: %v", viewerID, err)
	}
	return ranked
}

// discoveryCached is the pool's cache form: FeedItem hides Source and
// CursorToken from JSON, and the pool needs neither.
type discoveryCached struct {
	PostID      uuid.UUID `json:"p"`
	AuthorID    uuid.UUID `json:"a"`
	CreatedAt   time.Time `json:"t"`
	ContentType string    `json:"c"`
}

// discoveryPool is post-service's recent public posts, read anonymously and
// shared by every viewer for discoveryPoolTTL. Per-viewer rules come later:
// discoveryFor's filters, then hydration.
func (s *Service) discoveryPool(ctx context.Context) ([]FeedItem, error) {
	if raw, err := s.rdb.Get(ctx, discoveryPoolKey).Bytes(); err == nil {
		var cached []discoveryCached
		if json.Unmarshal(raw, &cached) == nil {
			out := make([]FeedItem, len(cached))
			for i, c := range cached {
				out[i] = FeedItem{PostID: c.PostID, AuthorID: c.AuthorID, CreatedAt: c.CreatedAt, ContentType: c.ContentType, Source: sourceColdStart}
			}
			return out, nil
		}
	}
	if s.postServiceURL == "" || s.postClient == nil {
		return nil, nil
	}
	items, err := s.getRecentPublicPosts(ctx, discoveryPoolSize)
	if err != nil {
		return nil, err
	}
	cached := make([]discoveryCached, len(items))
	for i, it := range items {
		cached[i] = discoveryCached{PostID: it.PostID, AuthorID: it.AuthorID, CreatedAt: it.CreatedAt, ContentType: it.ContentType}
	}
	if raw, err := json.Marshal(cached); err == nil {
		s.rdb.Set(ctx, discoveryPoolKey, raw, discoveryPoolTTL)
	}
	return items, nil
}

// followingSet is the set of accounts the viewer follows, cached for
// followingCacheTTL (follow and unfollow events drop it).
func (s *Service) followingSet(ctx context.Context, viewerID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	var ids []uuid.UUID
	raw, err := s.rdb.Get(ctx, FollowingKey(viewerID)).Bytes()
	if err != nil || json.Unmarshal(raw, &ids) != nil {
		if ids, err = s.fetchFollowing(ctx, viewerID); err != nil {
			return nil, err
		}
		if raw, err := json.Marshal(ids); err == nil {
			s.rdb.Set(ctx, FollowingKey(viewerID), raw, followingCacheTTL)
		}
	}
	out := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out, nil
}
