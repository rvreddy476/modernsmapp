package service

import (
	"context"
	"fmt"

	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Follow backfill (findings feed-08 and feed-60, 16 Nov 2026).
//
// When A follows B, B's recent posts are copied from B's author timeline
// into A's home timeline, so a freshly followed account shows up at once
// (only posts created AFTER the follow reach A through the fan-out).
//
// Two things changed when this moved here from the Kafka consumer:
//
// feed-08. The rows were written one after another (about 100 sequential
// Scylla writes per follow, on a consumer that handles one event at a
// time), and a failed row was logged and dropped. They now go through the
// fan-out's bounded worker pool (pushChunkWith), and a failed row fails the
// event: the consumer retries it until it succeeds, and every write upserts
// (scylla.TimelineTS), so the retry rewrites the same rows.
//
// feed-60. The author timeline holds EVERY post of the author, private and
// unlisted ones included (FanoutPost writes it whatever the visibility), and
// the backfill copied all of it. The rows were never served (hydration drops
// them on every read), but a private post sat in a stranger's timeline
// partition protected by one layer instead of two, and took a slot in the
// page window. The backfill now asks post-service which of the posts THIS
// follower may see (its batch, read as the follower: it leaves out private and
// deleted posts and checks the follow edge for followers-only and
// friends-only ones) and keeps only those with a listable visibility,
// the rule FanoutPost applies on the write side (listableVisibility).
//
// It fails closed: if post-service cannot be asked, nothing is written and
// the event is retried, rather than the unfiltered rows being copied.

const (
	// followBackfillLimit is how many of the followee's newest posts are
	// copied. It is below postBatchMax, so the visibility check is one call.
	followBackfillLimit = 50
	// followBackfillBuckets is how many monthly buckets the read walks back.
	followBackfillBuckets = 3
)

// authorTimelineReader is the slice of the Scylla store the follow backfill
// reads: the followee's own timeline, newest first.
type authorTimelineReader interface {
	GetAuthorTimelineMultiBucket(ctx context.Context, authorID uuid.UUID, limit, bucketLookback int) ([]scylla.FeedItem, error)
}

// FollowBackfillResult says what one backfill did, for the log line.
type FollowBackfillResult struct {
	Found   int // rows on the followee's author timeline
	Written int // rows written to the follower's home timeline
	Skipped int // rows the follower may not be listed (private, unlisted, deleted)
}

// BackfillHomeOnFollow copies the followee's recent listable posts into the
// follower's home timeline. Any failure — the read, the visibility check or
// a single row's write — is returned, so the event is retried. Idempotent.
func (s *Service) BackfillHomeOnFollow(ctx context.Context, followerID, followeeID uuid.UUID) (FollowBackfillResult, error) {
	var res FollowBackfillResult
	if s.authorTimelines == nil || s.timelines == nil {
		return res, nil
	}
	rows, err := s.authorTimelines.GetAuthorTimelineMultiBucket(ctx, followeeID, followBackfillLimit, followBackfillBuckets)
	if err != nil {
		return res, fmt.Errorf("read author timeline for backfill: %w", err)
	}
	res.Found = len(rows)
	if len(rows) == 0 {
		return res, nil
	}

	listable, err := s.listableToFollower(ctx, followerID, rows)
	if err != nil {
		return res, fmt.Errorf("follow backfill: visibility of %s's posts for %s not established, nothing written: %w", followeeID, followerID, err)
	}
	res.Skipped = len(rows) - len(listable)
	if len(listable) == 0 {
		return res, nil
	}

	// One write per post, through the fan-out's worker pool. The pool is
	// keyed by id; here the id is the post's.
	byPost := make(map[uuid.UUID]scylla.FeedItem, len(listable))
	postIDs := make([]uuid.UUID, 0, len(listable))
	for _, it := range listable {
		if _, dup := byPost[it.PostID]; dup {
			continue
		}
		byPost[it.PostID] = it
		postIDs = append(postIDs, it.PostID)
	}
	err = pushChunkWith(postIDs, func(postID uuid.UUID) error {
		it := byPost[postID]
		return s.timelines.AddToHomeTimeline(ctx, followerID, it.PostID, followeeID, it.CreatedAt, it.ContentType)
	})
	if err != nil {
		return res, fmt.Errorf("follow backfill for follower %s of %s incomplete, will be retried: %w", followerID, followeeID, err)
	}
	res.Written = len(postIDs)
	return res, nil
}

// listableToFollower keeps the rows whose post the follower may be listed:
// post-service returned it for this viewer AND its visibility is one a feed
// may list to someone other than the author. A post-service failure is an
// error, never "all of them".
func (s *Service) listableToFollower(ctx context.Context, followerID uuid.UUID, rows []scylla.FeedItem) ([]scylla.FeedItem, error) {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.PostID.String())
	}
	posts, err := s.postBatch(ctx, followerID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]scylla.FeedItem, 0, len(rows))
	for _, r := range rows {
		post, ok := posts[r.PostID.String()]
		if !ok {
			continue // post-service left it out for this viewer
		}
		if !listableVisibility(post.Visibility) {
			continue // unlisted, private, staged, ...: the author's alone
		}
		out = append(out, r)
	}
	return out, nil
}
