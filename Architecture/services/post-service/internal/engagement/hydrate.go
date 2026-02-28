package engagement

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// PostEngagement holds hydrated engagement data for a single post.
type PostEngagement struct {
	Likes    int64 `json:"likes"`
	Comments int64 `json:"comments"`
	Shares   int64 `json:"shares"`
}

// ViewerState holds the viewer's relationship to a post.
type ViewerState struct {
	Liked      bool `json:"liked"`
	Shared     bool `json:"shared"`
	Bookmarked bool `json:"bookmarked"`
}

// HydratedPost bundles engagement counts, viewer state, and recent likers.
type HydratedPost struct {
	PostID       uuid.UUID   `json:"post_id"`
	Engagement   PostEngagement `json:"engagement"`
	ViewerState  ViewerState    `json:"viewer_state"`
	RecentLikers []string       `json:"recent_likers"`
}

// HydrateEngagement uses a single Redis Pipeline (1 RTT) to fetch engagement
// data for N posts: counters, viewer membership checks, and recent likers.
// 5 commands × N posts in 1 pipeline round trip.
func HydrateEngagement(ctx context.Context, rdb *redis.Client, postIDs []uuid.UUID, viewerID uuid.UUID) (map[uuid.UUID]*HydratedPost, error) {
	if len(postIDs) == 0 {
		return nil, nil
	}

	pipe := rdb.Pipeline()

	type pipeSlot struct {
		counters *redis.MapStringStringCmd
		liked    *redis.IntCmd
		shared   *redis.IntCmd
		bookmark *redis.IntCmd
		likers   *redis.StringSliceCmd
	}

	slots := make([]pipeSlot, len(postIDs))

	for i, pid := range postIDs {
		engKey := fmt.Sprintf("post:eng:%s", pid)
		likedKey := fmt.Sprintf("liked:%s:%s", viewerID, pid)
		sharedKey := fmt.Sprintf("shared:%s:%s", viewerID, pid)
		bookmarkKey := fmt.Sprintf("bookmarked:%s:%s", viewerID, pid)
		likersKey := fmt.Sprintf("post:likers:%s", pid)

		slots[i] = pipeSlot{
			counters: pipe.HGetAll(ctx, engKey),
			liked:    pipe.Exists(ctx, likedKey),
			shared:   pipe.Exists(ctx, sharedKey),
			bookmark: pipe.Exists(ctx, bookmarkKey),
			likers:   pipe.SRandMemberN(ctx, likersKey, 3),
		}
	}

	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("hydrate pipeline: %w", err)
	}

	result := make(map[uuid.UUID]*HydratedPost, len(postIDs))

	for i, pid := range postIDs {
		slot := slots[i]

		counters, _ := slot.counters.Result()

		hp := &HydratedPost{
			PostID: pid,
			Engagement: PostEngagement{
				Likes:    parseCountStr(counters, "likes"),
				Comments: parseCountStr(counters, "comments"),
				Shares:   parseCountStr(counters, "shares"),
			},
			ViewerState: ViewerState{
				Liked:      slot.liked.Val() > 0,
				Shared:     slot.shared.Val() > 0,
				Bookmarked: slot.bookmark.Val() > 0,
			},
			RecentLikers: slot.likers.Val(),
		}
		if hp.RecentLikers == nil {
			hp.RecentLikers = []string{}
		}

		result[pid] = hp
	}

	return result, nil
}

// parseCountStr extracts a numeric value from a Redis hash result.
func parseCountStr(counters map[string]string, field string) int64 {
	if v, ok := counters[field]; ok {
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return 0
}
