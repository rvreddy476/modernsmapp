package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Who may still keep the author's fanned-out timeline rows after an
// unfollow.
//
// # WHY AN UNFOLLOW IS NOT A LICENCE TO DELETE
//
// The UserUnfollowed consumer purges the ex-follower's
// home_timeline_by_user rows authored by the followee, so the unfollow
// shows up immediately (feed-service/internal/store/scylla/timelines.go,
// DeleteHomeTimelineEntriesByAuthorForUser). Until 2026-09-09 that DELETE
// was malformed and purged nothing, so the question below never had to be
// answered. Making it work makes the question urgent, because the home
// timeline is NOT a materialised follower list:
//
//	FanoutPost (feed.go) writes a home-timeline row to
//	  · the author's FOLLOWERS, UNION
//	  · the author's CONNECTIONS ("circle" — /v1/graph/connections), or
//	  · for a visibility="trusted" post, the author's CLOSE FRIENDS only.
//
// So "F unfollowed A" does not imply "F should hold none of A's posts". If
// F is still a connection of A, A's NEXT post will be fanned out to F
// anyway — purging the earlier ones would delete content F is entitled to,
// leave F's feed inconsistent with the fanout that keeps feeding it, and
// there is no mechanism that ever puts those rows back (fan-out is
// write-time only; UserFollowed's backfill runs on follow, not on
// unfollow).
//
// # THE DECISION
//
// Ask graph-service for the real relationship and purge only when nothing
// else entitles the ex-follower to the author's fanout. Three facts keep
// the rows:
//
//	follows                        the pair follow again already — the
//	                               event is stale (unfollow → re-follow
//	                               consumed out of order, or a redelivery)
//	is_connection                  FanoutPost targets connections
//	viewer_is_close_friend_of_target  the AUTHOR has the ex-follower on
//	                               their close-friends list, which is the
//	                               audience for "trusted" posts
//
// # FAILING CLOSED
//
// An unestablished relationship means NO purge. The two failure modes are
// not symmetric: leaving rows is the status quo — visible content the
// viewer may not expect, but the reason line no longer lies about it (see
// reason.go) and every safety filter still runs at read time — whereas
// deleting a connection's rows destroys feed content with nothing to
// restore it. So a graph-service error, a timeout, or a missing entry in
// the answer all mean "keep", and the consumer logs why.
//
// # THE RESIDUAL, STATED PLAINLY
//
// A timeline row carries no provenance: nothing records whether the follow
// or the circle membership put it there, and no `visibility` column
// survives fan-out. So for someone who is a connection (or a close friend)
// AND was a follower, the follow's share of the rows cannot be separated
// from the circle's, and this keeps all of them. That is deliberate — the
// alternative is guessing at which rows to destroy.

// fanoutClaimTimeout bounds the graph lookup. The consumer is not latency
// sensitive, but it is single-goroutine (events.Consumer.Start reads and
// processes in one loop), so an unbounded call would stall every later feed
// event behind it.
const fanoutClaimTimeout = 3 * time.Second

// fanoutClaimSurvivesUnfollow is the pure decision: given the ex-follower's
// relationship to the author, may the purge proceed? Returns
// (retained, why) — `why` names the surviving claim for the log line, and
// is empty exactly when retained is false.
//
// Deliberately a total function over the relationship struct with no
// default-to-delete branch: a new field on viewerRelationship cannot change
// this answer without someone editing it here.
func fanoutClaimSurvivesUnfollow(rel viewerRelationship) (bool, string) {
	switch {
	case rel.Follows:
		// The graph says the follow exists NOW. Whatever this event
		// described has been superseded; deleting would break a live follow.
		return true, "follows the author again"
	case rel.IsConnection:
		return true, "is still a connection of the author"
	case rel.ViewerIsCloseFriendOfTarget:
		return true, "is on the author's close-friends list"
	default:
		return false, ""
	}
}

// FanoutClaimAfterUnfollow reports whether followerID still has a standing
// claim on followeeID's fanned-out home-timeline rows, and names it. Called
// by the UserUnfollowed consumer before it purges anything.
//
// An error means "not established" — the caller must NOT purge. See FAILING
// CLOSED above.
func (s *Service) FanoutClaimAfterUnfollow(ctx context.Context, followerID, followeeID uuid.UUID) (bool, string, error) {
	if s.graphURL == "" || s.graphClient == nil {
		return false, "", fmt.Errorf("graph-service not configured; relationship cannot be established")
	}
	if followerID == uuid.Nil || followeeID == uuid.Nil {
		return false, "", fmt.Errorf("empty follower or followee id")
	}

	ctx, cancel := context.WithTimeout(ctx, fanoutClaimTimeout)
	defer cancel()

	rels, err := s.fetchRelationships(ctx, followerID, []uuid.UUID{followeeID})
	if err != nil {
		return false, "", err
	}
	rel, ok := rels[followeeID]
	if !ok {
		// graph-service pre-populates an entry for every target it was
		// asked about, so an absent one is a contract break, not a "no".
		// Same rule as resolveFollowReasons: absent means unknown, never no.
		return false, "", fmt.Errorf("graph-service returned no relationship for %s", followeeID)
	}

	retained, why := fanoutClaimSurvivesUnfollow(rel)
	return retained, why, nil
}
