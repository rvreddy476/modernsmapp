package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Celeb pull model (audit F-14).
//
// FanoutPost does not push a celeb's posts into followers' timelines — a
// million followers is a million writes per post, all on one Kafka
// partition. Until now nothing read the other side: no code ever marked an
// author celeb, and GetHomeFeed never fetched a celeb's own timeline, so
// flagging an account by hand would have made its posts vanish from every
// follower's feed.
//
// With FEED_CELEB_PULL=true:
//   - GetHomeFeed merges the author timelines of the celebs the viewer
//     follows into the timeline read, before any filter, by timeline key
//     (author and home rows of one post share scylla.TimelineTS), so the
//     page window and its cursor work unchanged;
//   - FanoutPost flags an author celeb once their follower count reaches
//     FEED_CELEB_FOLLOWER_THRESHOLD (0 = never automatically) and from then
//     on pushes nothing to followers (subscribers of a long video's channel
//     still get theirs).
//
// Both are off by default: turning pull on is safe at any time (it only
// adds rows), but the threshold must not be set without it.

type celebConfig struct {
	pull      bool
	threshold int
}

func loadCelebConfig() celebConfig {
	c := celebConfig{pull: os.Getenv("FEED_CELEB_PULL") == "true"}
	if n, err := strconv.Atoi(os.Getenv("FEED_CELEB_FOLLOWER_THRESHOLD")); err == nil && n > 0 {
		c.threshold = n
	}
	return c
}

// maxPulledCelebs bounds the author timelines one home read fans out to.
const maxPulledCelebs = 50

// followedCelebsTTL is how long a viewer's followed-celeb set is cached.
// Follow and unfollow events drop it at once; a new celeb flag reaches
// followers within this window.
const followedCelebsTTL = 5 * time.Minute

// FollowedCelebsKey is the Redis key of a viewer's cached followed-celeb set.
func FollowedCelebsKey(viewerID uuid.UUID) string {
	return fmt.Sprintf("feed:followed_celebs:%s", viewerID)
}

// celebMarker flags an author celeb; the Postgres MetaStore implements it.
type celebMarker interface {
	SetCelebStatus(ctx context.Context, authorID uuid.UUID, isCeleb bool) error
}

// followedCelebs returns the celebs the viewer follows (at most
// maxPulledCelebs), from the cache or from graph-service + celeb_authors.
func (s *Service) followedCelebs(ctx context.Context, viewerID uuid.UUID) ([]uuid.UUID, error) {
	key := FollowedCelebsKey(viewerID)
	if s.rdb != nil {
		if raw, err := s.rdb.Get(ctx, key).Bytes(); err == nil {
			var ids []uuid.UUID
			if json.Unmarshal(raw, &ids) == nil {
				return ids, nil
			}
		}
	}
	if s.pgStore == nil {
		return nil, nil
	}
	following, err := s.fetchFollowing(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	celebs, err := s.pgStore.CelebsAmong(ctx, following)
	if err != nil {
		return nil, err
	}
	if len(celebs) > maxPulledCelebs {
		celebs = celebs[:maxPulledCelebs]
	}
	if s.rdb != nil {
		if raw, err := json.Marshal(celebs); err == nil {
			s.rdb.Set(ctx, key, raw, followedCelebsTTL)
		}
	}
	return celebs, nil
}

// pullCelebRows reads the timelines of the celebs the viewer follows,
// strictly below beforeToken, up to limit rows each. A celeb whose read
// fails is skipped and logged: their posts are missing from this page,
// which is not a safety question (every pulled row still goes through the
// page's block, privacy and visibility filters).
func (s *Service) pullCelebRows(ctx context.Context, viewerID uuid.UUID, beforeToken string, limit int) ([]scylla.FeedItem, error) {
	celebs, err := s.followedCelebs(ctx, viewerID)
	if err != nil || len(celebs) == 0 {
		return nil, err
	}
	results := make([][]scylla.FeedItem, len(celebs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, celeb := range celebs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, celeb uuid.UUID) {
			defer wg.Done()
			defer func() { <-sem }()
			rows, err := s.scyllaStore.GetAuthorTimelineBefore(ctx, celeb, beforeToken, limit)
			if err != nil {
				log.Printf("celeb pull: author timeline %s unavailable: %v", celeb, err)
				return
			}
			results[i] = rows
		}(i, celeb)
	}
	wg.Wait()
	var out []scylla.FeedItem
	for _, r := range results {
		out = append(out, r...)
	}
	return out, nil
}

// mergeTimelineRows merges the viewer's home rows with pulled celeb rows into
// one newest-first list by timeline key, one row per post, at most limit
// long. Truncating to limit is what keeps the page cursor exact: every row
// left out is older than the oldest row kept, so the next page reads it.
func mergeTimelineRows(home, pulled []scylla.FeedItem, limit int) []scylla.FeedItem {
	all := make([]scylla.FeedItem, 0, len(home)+len(pulled))
	all = append(all, home...)
	all = append(all, pulled...)
	sort.SliceStable(all, func(i, j int) bool { return timelineKeyAfter(all[i].CursorToken, all[j].CursorToken) })
	seen := make(map[uuid.UUID]struct{}, len(all))
	out := all[:0]
	for _, row := range all {
		if _, dup := seen[row.PostID]; dup {
			continue
		}
		seen[row.PostID] = struct{}{}
		out = append(out, row)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// timelineKeyAfter orders two timeline keys newest first: by the timeuuid's
// timestamp, then by the key itself so equal timestamps order stably.
func timelineKeyAfter(a, b string) bool {
	ua, errA := uuid.Parse(a)
	ub, errB := uuid.Parse(b)
	if errA != nil || errB != nil {
		return a > b
	}
	if ta, tb := ua.Time(), ub.Time(); ta != tb {
		return ta > tb
	}
	return a > b
}
