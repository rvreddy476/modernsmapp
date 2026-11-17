package search

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Trending hashtags (2026-11-17, reels finding reels-19 / V4-4).
//
// GET /v1/discover/trending used to read the Redis sorted set
// trending:hashtags:{date} that post-service increments on every create
// and comment. That counter is bumped for pending, private,
// followers-only, rejected and scheduled posts and is never decremented,
// so it leaked private tags and stayed inflated after a delete.
//
// It now aggregates over posts_v1 like every other hashtag surface
// (SearchHashtags, autocomplete): only documents that are public and
// approved count, and a removal, visibility downgrade, rejection, takedown
// or delete drops the post's contribution the moment its document becomes
// a tombstone. Nothing is incremented, so nothing needs decrementing.
// encodeQuery adds the viewer's block list, the private-author rule, the
// Echo-note exclusion and the hidden-author rule, as on every posts query.

// TrendingWindow is how far back a post counts towards trending.
const TrendingWindow = 24 * time.Hour

// TrendingHashtag is one row of the trending list: the tag and the number
// of eligible posts in the window that carry it.
type TrendingHashtag struct {
	Hashtag string  `json:"hashtag"`
	Score   float64 `json:"score"`
}

// BuildTrendingHashtagsQuery is the aggregation body before the viewer
// scope is applied (exported for tests).
func BuildTrendingHashtagsQuery(now time.Time, limit int) map[string]interface{} {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	filter := toIfaceSlice(publicApprovedFilter())
	filter = append(filter, map[string]interface{}{
		"range": map[string]interface{}{
			"created_at": map[string]interface{}{
				"gte": now.Add(-TrendingWindow).UTC().Format(time.RFC3339),
			},
		},
	})
	return map[string]interface{}{
		"size":    0,
		"timeout": "2s",
		"query": map[string]interface{}{
			"bool": map[string]interface{}{"filter": filter},
		},
		"aggs": map[string]interface{}{
			"trending": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "hashtags",
					"size":  limit,
				},
			},
		},
	}
}

// TrendingHashtags returns the top hashtags over eligible posts created in
// the last TrendingWindow, most used first. Always a non-nil slice.
func (s *Store) TrendingHashtags(ctx context.Context, limit int) ([]TrendingHashtag, error) {
	buf, err := encodeQuery(ctx, BuildTrendingHashtagsQuery(time.Now(), limit), privateAuthorField)
	if err != nil {
		return nil, err
	}
	tCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	res, err := s.client.Search(
		s.client.Search.WithContext(tCtx),
		s.client.Search.WithIndex(IndexPosts),
		s.client.Search.WithBody(buf),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, fmt.Errorf("trending hashtags: %s", res.String())
	}
	var r struct {
		Aggregations struct {
			Trending struct {
				Buckets []struct {
					Key      string  `json:"key"`
					DocCount float64 `json:"doc_count"`
				} `json:"buckets"`
			} `json:"trending"`
		} `json:"aggregations"`
	}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("trending hashtags: decode: %w", err)
	}
	out := make([]TrendingHashtag, 0, len(r.Aggregations.Trending.Buckets))
	for _, b := range r.Aggregations.Trending.Buckets {
		if b.Key == "" {
			continue
		}
		out = append(out, TrendingHashtag{Hashtag: b.Key, Score: b.DocCount})
	}
	return out, nil
}
