package service

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// "Why you're seeing this post" (post "more" sheet, 2026-09-04).
//
// Every hydrated feed item carries `reason` — a stable token the client
// can branch on — and `reason_text`, a sentence it can show verbatim. Both
// are derived from what the feed already knows at hydration time: which
// path produced the candidate (FeedItem.Source), the post's own visibility
// and category, and, for the recommendation path only, the post's velocity
// score from the tracker's sorted set (one ZMSCORE per page, best-effort).
// No graph or profile lookup is added for this.
//
// Tokens:
//
//	following        a fanout row: the author (or, for a repost, the
//	                 reposter) is someone the viewer follows
//	connection       circle_only view, or a close-friends ("trusted") post
//	trending         recommendation with engagement velocity, no category
//	category:<id>    recommendation with velocity in a flick category
//	recommended      recommendation with nothing more specific to say
//	hashtag:<tag>    reserved — emitted once a hashtag-follow path exists;
//	                 no surface produces hashtag candidates today
//
// The viewer's own posts carry no reason: nobody needs to be told why
// their post is on their feed.

const (
	ReasonFollowing   = "following"
	ReasonConnection  = "connection"
	ReasonTrending    = "trending"
	ReasonRecommended = "recommended"
	reasonCategory    = "category:"
)

// categoryLabels mirrors post-service's flick taxonomy labels
// (internal/service/categories.go there). Ids are the API; labels are
// display text, so a drift here costs a sentence, not a contract.
var categoryLabels = map[string]string{
	"comedy": "Comedy", "music": "Music", "dance": "Dance", "food": "Food",
	"travel": "Travel", "sports": "Sports", "education": "Education",
	"tech": "Tech", "beauty": "Beauty", "fashion": "Fashion", "gaming": "Gaming",
	"fitness": "Fitness", "pets": "Pets", "art": "Art", "news": "News",
	"lifestyle": "Lifestyle", "business": "Business", "other": "Other",
}

func categoryLabel(id string) string {
	if l, ok := categoryLabels[id]; ok {
		return l
	}
	if id == "" {
		return ""
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

// deriveReason is the pure step: (candidate source, post, viewer, velocity)
// → (reason, reason_text). velocity is the recommendation path's engagement
// score; 0 when unknown.
func deriveReason(source string, post HydratedPost, viewerID uuid.UUID, velocity float64) (string, string) {
	if post.AuthorID == viewerID && !post.IsRepost {
		return "", ""
	}
	if source == sourceCircle {
		return ReasonConnection, "From your circle"
	}
	if post.Visibility == "trusted" {
		return ReasonConnection, "Shared with close friends"
	}
	if source == sourceColdStart {
		switch {
		case velocity > 0 && post.Category != "":
			return reasonCategory + post.Category, "Popular in " + categoryLabel(post.Category)
		case velocity > 0:
			return ReasonTrending, "Trending now"
		default:
			return ReasonRecommended, "Suggested for you"
		}
	}
	if post.IsRepost {
		return ReasonFollowing, "Reposted by someone you follow"
	}
	return ReasonFollowing, "From someone you follow"
}

// enrichReasons refines the reason on recommendation-path posts with the
// velocity tracker's score (post:velocity:ranked — the same sorted set the
// ranker reads). One pipelined ZMSCORE for the page; on any Redis problem
// the merge-time reason ("Suggested for you") stands. Fanout-path posts
// need nothing from Redis and are left as merged.
func (s *Service) enrichReasons(ctx context.Context, posts []HydratedPost, viewerID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	ids := make([]string, 0, len(posts))
	idx := make([]int, 0, len(posts))
	for i, p := range posts {
		if p.source == sourceColdStart {
			ids = append(ids, p.ID.String())
			idx = append(idx, i)
		}
	}
	if len(ids) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	scores, err := s.rdb.ZMScore(ctx, "post:velocity:ranked", ids...).Result()
	if err != nil || len(scores) != len(ids) {
		return
	}
	for j, i := range idx {
		posts[i].Reason, posts[i].ReasonText = deriveReason(posts[i].source, posts[i], viewerID, scores[j])
	}
}
