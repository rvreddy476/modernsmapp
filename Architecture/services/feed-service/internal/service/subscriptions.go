package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/atpost/feed-service/internal/ranking"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Tube channel subscriptions: user:subscribed_owners:{viewerID}.
//
// The Subscriptions tab (`subscribed_only=true` on /v1/feed/watch and
// /v1/feed/videos) is the viewer's long-video timeline narrowed to the
// OWNERS of the channels they subscribe to, newest first. The Tube home
// ranker reads the same set for its subscription boost
// (ranking/scorer.go, SubscriptionWeight). post-service owns the
// subscription rows and serves them on an internal, keyset-paged route;
// this file is the cache in front of that route and the filter that uses
// it.
//
// WHY NOT THE FOLLOW GRAPH
//
// `following_only` means follows, on every surface, and the reels tab
// shares the helper (watch_following_test.go pins it). A subscription is
// a follow edge PLUS a per-channel notify preference in post-service, so
// the follow set is a superset that would put every followed friend's
// occasional upload under a heading that promises channels. The two
// narrowings are therefore different flags with different sources, and a
// request carrying both is refused at the handler.
//
// WHY CACHED, AND WHY THIS SHAPE
//
// The tab is read on every scroll and the ranker on every home page; a
// paged post-service read per request is far outside either budget. The
// set is cached the way mutuals.go caches the mutual-follow set: a Redis
// set with a six-hour TTL, a "looked" marker so an empty answer is
// remembered too (Redis stores no empty sets, and without the marker a
// viewer with no subscriptions would refetch on every page forever), and
// a DEL from the consumer on tube.channel.subscribed / unsubscribed so a
// change shows up on the next request rather than in six hours.
//
// WHY THE TAB FETCHES INLINE AND FAILS CLOSED
//
// Unlike the ranker's boost, which is fine one page late, the tab IS the
// set: served without it, it would show the viewer's whole timeline under
// a heading that promises subscribed channels, and nothing in the
// response would say so. So on a cache miss the tab waits for post-service
// (bounded by the post client's timeout and breaker), and a post-service
// error is the tab's error, exactly as an unresolved follow graph is the
// Following tab's. The ranker's copy is warmed detached from the request
// (warmSubscribedOwners), so a home page never blocks on it.

const (
	// subscribedOwnersTTL. The set changes only when the viewer
	// subscribes or unsubscribes, and the consumer invalidates it on both,
	// so the TTL is a backstop against a missed event rather than the
	// refresh clock. Six hours matches the mutual-follow set.
	subscribedOwnersTTL = 6 * time.Hour

	// subscribedOwnersPageSize is the page asked of post-service's
	// subscribed-owner-ids route. Its default is 1000; 500 keeps one page
	// well inside one response for a viewer with hundreds of channels
	// while still needing a single round trip for nearly everyone.
	subscribedOwnersPageSize = 500

	// maxSubscribedOwnerPages bounds the paging loop. A viewer with more
	// than 100k subscriptions is not a viewer, and a route that keeps
	// answering has_more=true with a repeating cursor must not spin the
	// request forever.
	maxSubscribedOwnerPages = 200

	// subscriberPageSize / maxSubscriberPages bound the fan-out leg's walk
	// over a channel's subscribers (post-service default 500). A million
	// subscribers is the ceiling at which the pull model would have to
	// take over for subscribers as it does for followers.
	subscriberPageSize = 500
	maxSubscriberPages = 2000

	// subscribedWarmCooldown throttles detached refresh ATTEMPTS per
	// viewer, so a viewer whose post-service calls are failing cannot fire
	// one on every home page they load. Set before the attempt, as in
	// mutuals.go, so a failure is thrown away for ten minutes too.
	subscribedWarmCooldown = 10 * time.Minute

	// subscribedWarmTimeout bounds the detached refresh.
	subscribedWarmTimeout = 5 * time.Second
)

// subscribedOwnersKey is the set the tab filters on and the ranker reads.
// Named in the ranking package so the two cannot drift.
func subscribedOwnersKey(viewerID uuid.UUID) string {
	return ranking.SubscribedOwnersKey(viewerID)
}

// subscribedLookedKey records that a fetch SUCCEEDED, separately from the
// set it produced, for the empty-answer case (see the file comment). Its
// value is the member count, for a human reading Redis.
func subscribedLookedKey(viewerID uuid.UUID) string {
	return "feed:subscriptions:looked:" + viewerID.String()
}

// subscribedWarmMarkerKey throttles detached refresh attempts.
func subscribedWarmMarkerKey(viewerID uuid.UUID) string {
	return "feed:subscriptions:warming:" + viewerID.String()
}

// applySubscribedFilter narrows candidates to authors whose Tube channel
// the viewer subscribes to. Fails CLOSED: an unresolved subscription list
// is an error, never a page of strangers under a heading that promises
// subscribed channels. An empty candidate set short-circuits without a
// lookup. `surface` names the caller in the log line and the wrapped
// error, as applyFollowingFilter does.
func (s *Service) applySubscribedFilter(ctx context.Context, userID uuid.UUID, candidates []FeedItem, surface string) ([]FeedItem, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}
	owners, err := s.subscribedOwners(ctx, userID)
	if err != nil {
		log.Printf("%s subscribed_only: failed to resolve subscriptions for %s: %v", surface, userID, err)
		return nil, fmt.Errorf("%s subscribed filter: %w", surface, err)
	}
	return filterByAuthorSet(candidates, owners), nil
}

// subscribedOwners returns the channel owners the viewer subscribes to:
// from the Redis set when it is there (or the "looked" marker says the
// answer is none), otherwise from post-service, which then fills the set
// for the next request and the ranker. Without Redis it simply asks
// post-service every time.
func (s *Service) subscribedOwners(ctx context.Context, viewerID uuid.UUID) ([]uuid.UUID, error) {
	if s.rdb == nil {
		return s.fetchSubscribedOwners(ctx, viewerID)
	}
	if owners, hit := s.readSubscribedOwnersCache(ctx, viewerID); hit {
		return owners, nil
	}
	return s.refreshSubscribedOwners(ctx, viewerID)
}

// readSubscribedOwnersCache reads the set and the looked marker in one
// round trip. hit is false on a miss AND on a Redis error: a blip must
// cost one post-service read, not the tab.
func (s *Service) readSubscribedOwnersCache(ctx context.Context, viewerID uuid.UUID) (owners []uuid.UUID, hit bool) {
	pipe := s.rdb.Pipeline()
	membersCmd := pipe.SMembers(ctx, subscribedOwnersKey(viewerID))
	lookedCmd := pipe.Exists(ctx, subscribedLookedKey(viewerID))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		log.Printf("subscribed-owners cache read failed for %s: %v", viewerID, err)
		return nil, false
	}
	members, err := membersCmd.Result()
	if err != nil {
		return nil, false
	}
	if len(members) == 0 {
		looked, err := lookedCmd.Result()
		if err != nil || looked == 0 {
			return nil, false
		}
		return nil, true // looked, and the answer was none
	}
	owners = make([]uuid.UUID, 0, len(members))
	for _, m := range members {
		id, err := uuid.Parse(m)
		if err != nil {
			continue // a foreign member cannot become an author match
		}
		owners = append(owners, id)
	}
	return owners, true
}

// refreshSubscribedOwners fetches from post-service and replaces the set.
// A post-service error is returned (fail closed); a Redis write error is
// logged, because the answer in hand is still the right one to serve.
func (s *Service) refreshSubscribedOwners(ctx context.Context, viewerID uuid.UUID) ([]uuid.UUID, error) {
	owners, err := s.fetchSubscribedOwners(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	if err := s.writeSubscribedOwnersCache(ctx, viewerID, owners); err != nil {
		log.Printf("subscribed-owners cache write failed for %s: %v", viewerID, err)
	}
	return owners, nil
}

// writeSubscribedOwnersCache replaces the set. DEL unconditionally, as
// mutuals.go does: an unsubscribe has to be able to REMOVE an owner, and a
// viewer whose last subscription just ended must end up with no set rather
// than a stale one. The looked marker is written even when the answer was
// none, which is the whole point of having it.
func (s *Service) writeSubscribedOwnersCache(ctx context.Context, viewerID uuid.UUID, owners []uuid.UUID) error {
	key := subscribedOwnersKey(viewerID)
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, key)
	if len(owners) > 0 {
		members := make([]any, 0, len(owners))
		for _, id := range owners {
			members = append(members, id.String())
		}
		pipe.SAdd(ctx, key, members...)
		pipe.Expire(ctx, key, subscribedOwnersTTL)
	}
	pipe.Set(ctx, subscribedLookedKey(viewerID), len(owners), subscribedOwnersTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// InvalidateSubscribedOwners drops the viewer's cached set and its looked
// marker, so the next request rebuilds both from post-service. Called by
// the Kafka consumer on tube.channel.subscribed / unsubscribed. The
// warming cooldown is left alone: the next tab request refetches inline
// regardless, and a home page within the cooldown simply keeps the boost
// one event behind, which is the cost the cooldown exists to cap.
func (s *Service) InvalidateSubscribedOwners(ctx context.Context, viewerID uuid.UUID) error {
	if s.rdb == nil || viewerID == uuid.Nil {
		return nil
	}
	return s.rdb.Del(ctx, subscribedOwnersKey(viewerID), subscribedLookedKey(viewerID)).Err()
}

// warmSubscribedOwners fills the set for the ranker, detached from the
// request, when nothing has looked yet. Part of warmViewerSignals; the
// gating mirrors warmMutualFollows exactly and for the same reasons. A
// Subscriptions-tab request that misses the cache fetches inline as well,
// so the first such request for a cold viewer can fetch twice; the second
// write is identical and the cooldown stops a third.
func (s *Service) warmSubscribedOwners(ctx context.Context, viewerID uuid.UUID) {
	exists, err := s.rdb.Exists(ctx, subscribedLookedKey(viewerID)).Result()
	if err != nil || exists == 1 {
		return
	}
	claimed, err := s.rdb.SetNX(ctx, subscribedWarmMarkerKey(viewerID), "1", subscribedWarmCooldown).Result()
	if err != nil || !claimed {
		return
	}
	go func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), subscribedWarmTimeout)
		defer cancel()
		if _, err := s.refreshSubscribedOwners(bg, viewerID); err != nil {
			log.Printf("subscribed-owners warm failed for %s: %v", viewerID, err)
		}
	}()
}

// fetchSubscribedOwners pages post-service's internal
// /internal/users/{userId}/subscribed-owner-ids until has_more is false.
func (s *Service) fetchSubscribedOwners(ctx context.Context, viewerID uuid.UUID) ([]uuid.UUID, error) {
	var all []uuid.UUID
	after := ""
	for page := 0; page < maxSubscribedOwnerPages; page++ {
		url := fmt.Sprintf("%s/internal/users/%s/subscribed-owner-ids?limit=%d", s.postServiceURL, viewerID, subscribedOwnersPageSize)
		if after != "" {
			url += "&after=" + after
		}
		ids, next, more, err := s.getInternalIDPage(ctx, url, "owner_ids")
		if err != nil {
			return nil, fmt.Errorf("subscribed owners for %s: %w", viewerID, err)
		}
		all = append(all, ids...)
		if !more || next == "" || next == after {
			return all, nil
		}
		after = next
	}
	return nil, fmt.Errorf("subscribed owners for %s: more than %d pages", viewerID, maxSubscribedOwnerPages)
}

// eachChannelSubscriber pages post-service's internal
// /internal/channels/{channelId}/subscriber-ids and hands every id to fn as
// it arrives, so the caller holds one page at a time. Returns the first
// page error; ids already delivered stay delivered.
func (s *Service) eachChannelSubscriber(ctx context.Context, channelID uuid.UUID, fn func(uuid.UUID)) error {
	after := ""
	for page := 0; page < maxSubscriberPages; page++ {
		url := fmt.Sprintf("%s/internal/channels/%s/subscriber-ids?limit=%d", s.postServiceURL, channelID, subscriberPageSize)
		if after != "" {
			url += "&after=" + after
		}
		ids, next, more, err := s.getInternalIDPage(ctx, url, "subscriber_ids")
		if err != nil {
			return fmt.Errorf("subscribers of channel %s: %w", channelID, err)
		}
		for _, id := range ids {
			fn(id)
		}
		if !more || next == "" || next == after {
			return nil
		}
		after = next
	}
	return fmt.Errorf("subscribers of channel %s: more than %d pages", channelID, maxSubscriberPages)
}

// getInternalIDPage reads one page of an internal keyset route. Both routes
// answer in the shared envelope with the id list under `field`, plus
// next_after and has_more; the field name is the only difference between
// them, so one reader serves both. A non-200, an unreachable post-service
// and a malformed body are all errors: decoded, each would look exactly
// like "no subscriptions", which is the one answer this must never invent.
func (s *Service) getInternalIDPage(ctx context.Context, url, field string) (ids []uuid.UUID, nextAfter string, hasMore bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", false, err
	}
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := s.postClient.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("post-service request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, "", false, fmt.Errorf("post-service returned %d: %s", resp.StatusCode, string(body))
	}
	var envelope struct {
		Data struct {
			OwnerIDs      []string `json:"owner_ids"`
			SubscriberIDs []string `json:"subscriber_ids"`
			NextAfter     string   `json:"next_after"`
			HasMore       bool     `json:"has_more"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, "", false, fmt.Errorf("decode %s page: %w", field, err)
	}
	raw := envelope.Data.OwnerIDs
	if field == "subscriber_ids" {
		raw = envelope.Data.SubscriberIDs
	}
	ids = make([]uuid.UUID, 0, len(raw))
	for _, r := range raw {
		id, err := uuid.Parse(r)
		if err != nil {
			continue // not a user id; cannot match an author or receive a row
		}
		ids = append(ids, id)
	}
	return ids, envelope.Data.NextAfter, envelope.Data.HasMore, nil
}
