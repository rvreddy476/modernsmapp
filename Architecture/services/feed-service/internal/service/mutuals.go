package service

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
)

// Mutual-follow proximity: user:mutual_follows:{viewerID}.
//
// The ranker's social_proximity term reads this set and doubles a
// candidate's proximity from 0.1 to 0.2 when the viewer and the author
// follow each other. Nothing has ever written the key, so the term has
// always been the flat 0.1 baseline and mutual follows have never counted
// for anything.
//
// WHY HERE AND NOT IN THE ANALYTICS JOB
//
// The other three empty signals are functions of watch history and are
// warmed by analytics-service, which owns the event stream. This one is
// not: it is the social graph, which analytics-service cannot reach and
// has no business reading. graph-service could publish it, but that would
// mean graph-service knowing the shape of a feed-ranking cache key —
// coupling the graph to a consumer that may change its mind about what it
// caches. feed-service already holds a circuit-broken graph client and
// already fetches both sides of this set for the "Following" tabs, so it
// warms its own.
//
// WHY DEMAND-DRIVEN AND NOT A SWEEP
//
// A periodic job would have to walk every user who has ever existed to
// keep a set that only matters for people currently looking at a feed.
// This refreshes a viewer's set when they ask for a feed and their set is
// missing or stale — so the work is proportional to active users, and a
// dormant account costs nothing.
//
// WHY OFF THE REQUEST PATH
//
// Two paginated graph-service calls is far more than a feed request's
// budget, and the ranker runs behind a 20 ms circuit breaker. The refresh
// therefore runs detached: this request is scored without the set (the
// same 0.1 baseline it has always used) and the NEXT one has it. A signal
// that arrives one page late is worth having; a feed that blocks on the
// graph is not.

const (
	// mutualFollowsTTL. The set only changes when someone follows or
	// unfollows, and being an hour out of date costs at most a 0.1 shift
	// in one term. Six hours keeps the refresh rate low without letting
	// the answer go properly stale.
	mutualFollowsTTL = 6 * time.Hour

	// mutualWarmCooldown throttles refresh ATTEMPTS per viewer,
	// independently of the TTL above.
	//
	// Without it, a viewer whose graph calls are failing would fire two
	// graph requests on every feed page they load — a retry storm aimed
	// at a service that is already unhealthy, from the one place that
	// must not add load during an incident. The cooldown marker is set
	// BEFORE the attempt, so a failure is thrown away for ten minutes
	// just as a success is.
	mutualWarmCooldown = 10 * time.Minute

	// mutualWarmTimeout bounds the detached refresh. Two paginated graph
	// reads; if they have not finished in five seconds the answer is not
	// worth waiting for and the next page will try again.
	mutualWarmTimeout = 5 * time.Second
)

func mutualFollowsKey(viewerID uuid.UUID) string {
	return "user:mutual_follows:" + viewerID.String()
}

// mutualWarmMarkerKey throttles refresh attempts.
func mutualWarmMarkerKey(viewerID uuid.UUID) string {
	return "feed:mutuals:warming:" + viewerID.String()
}

// mutualWarmedKey records that a refresh SUCCEEDED, separately from the set
// it produced.
//
// The obvious gate — "does the set exist?" — is wrong for the commonest
// case in a young product: a viewer with no mutual follows produces an
// empty set, Redis does not store empty sets, and the gate would therefore
// fire a fresh pair of graph calls for that viewer every time the cooldown
// lapsed, forever. This marker distinguishes "we have not looked" from "we
// looked and the answer was none".
func mutualWarmedKey(viewerID uuid.UUID) string {
	return "feed:mutuals:warmed:" + viewerID.String()
}

// warmViewerSignals is called at the top of each feed surface. It is
// non-blocking and best-effort by construction: nothing it does can fail
// a feed request, and everything it warms has a defined absent-value that
// the ranker already handles.
func (s *Service) warmViewerSignals(ctx context.Context, viewerID uuid.UUID) {
	if s.rdb == nil || viewerID == uuid.Nil {
		return
	}

	exists, err := s.rdb.Exists(ctx, mutualWarmedKey(viewerID)).Result()
	if err != nil {
		// A Redis error here is the caller's problem elsewhere; here it
		// just means we cannot tell whether a refresh is needed, and
		// guessing "yes" would turn a Redis blip into a graph-service
		// stampede.
		return
	}
	if exists == 1 {
		return
	}

	// SetNX is the whole of the concurrency control: the first request to
	// claim the marker does the work, every other request for the same
	// viewer — in this process or another replica — sees the key and
	// returns immediately.
	claimed, err := s.rdb.SetNX(ctx, mutualWarmMarkerKey(viewerID), "1", mutualWarmCooldown).Result()
	if err != nil || !claimed {
		return
	}

	// Detached from the request context on purpose: the refresh must
	// outlive the response it was triggered by, or it would be cancelled
	// the moment the feed is written and never actually complete.
	go func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), mutualWarmTimeout)
		defer cancel()
		if err := s.refreshMutualFollows(bg, viewerID); err != nil {
			log.Printf("mutual-follows warm failed for %s: %v", viewerID, err)
		}
	}()
}

// refreshMutualFollows recomputes the set and replaces it.
//
// A mutual follow is the intersection of "people who follow the viewer"
// and "people the viewer follows" — the same one-way sets the Following
// tabs read, so this and those cannot disagree about what following means.
func (s *Service) refreshMutualFollows(ctx context.Context, viewerID uuid.UUID) error {
	var mutual []uuid.UUID

	followers, err := s.fetchFollowers(ctx, viewerID)
	if err != nil {
		return err
	}
	if len(followers) > 0 {
		following, err := s.fetchFollowing(ctx, viewerID)
		if err != nil {
			return err
		}
		mutual = intersectIDs(followers, following)
	}

	key := mutualFollowsKey(viewerID)
	pipe := s.rdb.Pipeline()
	// DEL unconditionally: an unfollow has to be able to REMOVE someone
	// from the set, and a viewer whose last mutual follow just ended must
	// end up with no set rather than with a stale one. Adding to a set
	// that is never cleared would make the proximity boost outlive the
	// relationship that earned it.
	pipe.Del(ctx, key)
	if len(mutual) > 0 {
		members := make([]any, 0, len(mutual))
		for _, id := range mutual {
			members = append(members, id.String())
		}
		pipe.SAdd(ctx, key, members...)
		pipe.Expire(ctx, key, mutualFollowsTTL)
	}
	// The "we looked" marker is written even when the answer was none —
	// see mutualWarmedKey.
	pipe.Set(ctx, mutualWarmedKey(viewerID), len(mutual), mutualFollowsTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// intersectIDs returns the ids present in both slices, deduplicated. The
// smaller slice is indexed so the map is the size of the smaller side.
func intersectIDs(a, b []uuid.UUID) []uuid.UUID {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	set := make(map[uuid.UUID]struct{}, len(small))
	for _, id := range small {
		set[id] = struct{}{}
	}
	seen := make(map[uuid.UUID]struct{}, len(small))
	out := make([]uuid.UUID, 0, len(small))
	for _, id := range large {
		if _, ok := set[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
