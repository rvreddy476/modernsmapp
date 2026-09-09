package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// "Why you're seeing this post" (post "more" sheet, 2026-09-04).
//
// Every hydrated feed item may carry `reason` — a stable token the client
// can branch on — and `reason_text`, a sentence it can show verbatim.
//
// Tokens:
//
//	following        the viewer FOLLOWS the author (or, for a repost, the
//	                 reposter). Established from the graph — see
//	                 resolveFollowReasons.
//	connection       circle_only view, a close-friends ("trusted") post, or
//	                 an author the graph reports as a connection.
//	trending         recommendation with engagement velocity, no category
//	category:<id>    recommendation with velocity in a flick category
//	recommended      recommendation with nothing more specific to say
//	hashtag:<tag>    reserved — emitted once a hashtag-follow path exists;
//	                 no surface produces hashtag candidates today
//
// The viewer's own posts carry no reason: nobody needs to be told why
// their post is on their feed. Neither does a post whose reason cannot be
// established — see WHY A TIMELINE ROW IS NOT A FOLLOW below.
//
// # WHY A TIMELINE ROW IS NOT A FOLLOW
//
// Until 2026-09-09 this file ended in an unconditional
//
//	return ReasonFollowing, "From someone you follow"
//
// so "following" was the DEFAULT: anything whose candidate source was not
// one of the recognised special cases was declared a follow. Two separate
// things were wrong with that.
//
//  1. It was a default, not a derivation. Every surface that builds
//     FeedItems without setting Source — GetFlickFeedPage (reels/flicks)
//     among them — silently claimed a follow for every row it produced.
//
//  2. Even the source it was meant to stand for, sourceTimeline, does not
//     mean the viewer follows the author. The home timeline is a
//     materialised fanout: FanoutPost writes a row to followers UNION the
//     author's connections at post time, and nothing retracts those rows on
//     unfollow. So a timeline row proves only that some fanout once
//     targeted this viewer — not that the follow existed then (a connection
//     never followed at all), and not that it exists now.
//
// Measured on the running stack (2026-09-09): momentum.sso.test@example.com
// has zero rows in `follows`, and /v1/feed/reels, /flicks, /videos, /watch
// and /home all returned "From someone you follow" for author
// 7cd6ea3a — whose home-timeline rows predate the account's follows being
// cleared.
//
// The bucket therefore cannot be relabelled into honesty; the follow edge
// has to be read from the graph, or nothing said. deriveReason now returns
// ("", "") for the fanout path — "not established" — and
// resolveFollowReasons upgrades it once graph-service has answered. The
// safe direction is silence: a row whose reason cannot be established
// reaches the client with the fields absent, which is exactly what the
// Android "more" sheet already handles (it offers the "Why you're seeing
// this post" row only when reason_text is non-blank).
//
// This is NOT the X-Feed-Empty-Reason pattern and should not follow it.
// That fact is about a whole response with no items to hang it on, and
// ApiMeta is shared platform-wide, so it needed a header. This one is
// per-item, and the item already has a place for it in the shipped
// contract; absence is how it says "unknown".

const (
	ReasonFollowing   = "following"
	ReasonConnection  = "connection"
	ReasonTrending    = "trending"
	ReasonRecommended = "recommended"
	// ReasonMoreFromCreator is the related-videos surface saying "this is
	// another video by the person you are watching".
	ReasonMoreFromCreator = "more_from_creator"
	reasonCategory        = "category:"
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
//
// It answers only for the sources that are self-evident from the candidate
// itself. The fanout path is not one of them: it returns ("", "") and
// resolveFollowReasons settles it against the graph. Nothing in here may
// return ReasonFollowing — TestDeriveReasonCannotClaimAFollow pins that.
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
	// Related-videos sources. Checked before the cold-start branch: they
	// are recommendations too, but ones with something much more specific
	// to say than "suggested for you", and they need no velocity lookup
	// to say it.
	if source == sourceRelatedAuthor {
		return ReasonMoreFromCreator, "More from this creator"
	}
	if source == sourceRelatedTopic {
		if post.Category != "" {
			return reasonCategory + post.Category, "More in " + categoryLabel(post.Category)
		}
		return ReasonRecommended, "Similar to what you're watching"
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
	// The fanout path. Whether the viewer follows this author is not
	// knowable from the candidate — see WHY A TIMELINE ROW IS NOT A FOLLOW
	// at the top of this file. Left unset for resolveFollowReasons.
	return "", ""
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

// reasonSubject is the person a fanout row's reason is ABOUT: the reposter
// for a repost (the feed event is "X reposted this", and X is who the
// viewer's follow would be with), the author otherwise.
//
// Returns uuid.Nil when there is nobody to ask about — the viewer's own
// non-repost, or a repost whose reposter did not survive hydration.
func reasonSubject(post HydratedPost, viewerID uuid.UUID) uuid.UUID {
	if post.IsRepost {
		if post.RepostedBy == nil || *post.RepostedBy == viewerID {
			return uuid.Nil
		}
		return *post.RepostedBy
	}
	if post.AuthorID == viewerID {
		return uuid.Nil
	}
	return post.AuthorID
}

// resolveFollowReasonsTimeout bounds the whole pass. The reason line is
// decoration on a page that is otherwise ready to send; a graph-service
// that has not answered in this long has cost the request more than the
// sentence is worth, and the fields are simply omitted.
const resolveFollowReasonsTimeout = 400 * time.Millisecond

// resolveFollowReasons settles the fanout path's reason against the actual
// social graph.
//
// deriveReason leaves those rows blank because a timeline row is not
// evidence of a follow (see the top of this file). This pass asks
// graph-service what the viewer's relationship to each distinct subject on
// the page really is — POST /v1/graph/relationships/batch, one call for the
// page, answered mostly out of that service's own `rel:` cache — and fills
// in only what the answer supports:
//
//	follows        → "following"
//	is_connection  → "connection"  (how a non-follower's fanout row got
//	                 here at all: FanoutPost writes to the author's
//	                 connections too)
//	neither        → nothing. The row keeps no reason.
//
// Best-effort in one direction only. A graph-service that errors, times out
// or answers unparseably leaves the reasons absent; it can never restore
// the old behaviour of asserting a follow nobody checked. Rows that already
// carry a reason (circle view, trusted, cold start, related) are untouched
// and cost no graph traffic.
func (s *Service) resolveFollowReasons(ctx context.Context, posts []HydratedPost, viewerID uuid.UUID) {
	if s.graphURL == "" || s.graphClient == nil || viewerID == uuid.Nil {
		return
	}

	// Distinct subjects among the rows still waiting for an answer.
	subjects := make([]uuid.UUID, 0, len(posts))
	seen := make(map[uuid.UUID]struct{}, len(posts))
	for i := range posts {
		if posts[i].Reason != "" {
			continue
		}
		subject := reasonSubject(posts[i], viewerID)
		if subject == uuid.Nil {
			continue
		}
		if _, dup := seen[subject]; dup {
			continue
		}
		seen[subject] = struct{}{}
		subjects = append(subjects, subject)
	}
	if len(subjects) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, resolveFollowReasonsTimeout)
	defer cancel()
	rels, err := s.fetchRelationships(ctx, viewerID, subjects)
	if err != nil {
		// Not an error for the request: the page is correct, it just
		// cannot explain itself. Logged because a persistently silent
		// reason line is a graph-service symptom worth seeing.
		log.Printf("reason: relationship lookup for %s failed, %d row(s) will carry no reason: %v",
			viewerID, len(subjects), err)
		return
	}

	for i := range posts {
		if posts[i].Reason != "" {
			continue
		}
		subject := reasonSubject(posts[i], viewerID)
		if subject == uuid.Nil {
			continue
		}
		rel, ok := rels[subject]
		if !ok {
			continue // absent means unknown, never "yes"
		}
		switch {
		case rel.Follows && posts[i].IsRepost:
			posts[i].Reason, posts[i].ReasonText = ReasonFollowing, "Reposted by someone you follow"
		case rel.Follows:
			posts[i].Reason, posts[i].ReasonText = ReasonFollowing, "From someone you follow"
		case rel.IsConnection && posts[i].IsRepost:
			posts[i].Reason, posts[i].ReasonText = ReasonConnection, "Reposted by someone in your circle"
		case rel.IsConnection:
			posts[i].Reason, posts[i].ReasonText = ReasonConnection, "From your circle"
		}
	}
}

// viewerRelationship is the slice of graph-service's Relationship this file
// needs. Deliberately narrow: block and mute are enforced by the safety
// filters long before hydration, and a reason line has no business
// re-deciding them.
type viewerRelationship struct {
	Follows      bool `json:"follows"`
	IsConnection bool `json:"is_connection"`
	// ViewerIsCloseFriendOfTarget: the TARGET has the viewer on the
	// TARGET's close-friends list — the audience direction, not the
	// viewer's own list (graph-service store.Relationship documents why
	// the two are not interchangeable). No reason line uses it; the
	// unfollow purge does, because "trusted" posts are fanned out to
	// exactly this set. See unfollow_purge.go.
	ViewerIsCloseFriendOfTarget bool `json:"viewer_is_close_friend_of_target"`
}

// maxRelationshipBatch mirrors graph-service's store.MaxRelationshipBatch.
// That service REJECTS an oversized batch rather than truncating it (it
// used to truncate, and every target past the cap came back looking
// unrelated), so the caller chunks. A feed page is far smaller than this;
// the loop exists so a future wider page degrades into two calls instead of
// into no reasons at all.
const maxRelationshipBatch = 100

// fetchRelationships asks graph-service for the viewer's relationship to
// each target. POST because the input is a list of ids; the route is on
// graph-service's read-only-POST allowlist.
func (s *Service) fetchRelationships(ctx context.Context, viewerID uuid.UUID, targets []uuid.UUID) (map[uuid.UUID]viewerRelationship, error) {
	out := make(map[uuid.UUID]viewerRelationship, len(targets))
	for start := 0; start < len(targets); start += maxRelationshipBatch {
		end := start + maxRelationshipBatch
		if end > len(targets) {
			end = len(targets)
		}
		ids := make([]string, 0, end-start)
		for _, id := range targets[start:end] {
			ids = append(ids, id.String())
		}
		body, err := json.Marshal(map[string]any{
			"viewer_id":  viewerID.String(),
			"target_ids": ids,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal relationship batch: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			s.graphURL+"/v1/graph/relationships/batch", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		// Same internal-key forwarding as every other graph call here.
		if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}

		resp, err := s.graphClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("graph-service request failed: %w", err)
		}
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("graph-service returned %d: %s", resp.StatusCode, truncateForLog(raw))
		}
		// The batch route answers with a bare {target_id: relationship}
		// map, not the {"data": ...} envelope the GET routes use.
		var page map[uuid.UUID]viewerRelationship
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("unmarshal relationships: %w", err)
		}
		for id, rel := range page {
			out[id] = rel
		}
	}
	return out, nil
}

func truncateForLog(b []byte) string {
	const max = 200
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
