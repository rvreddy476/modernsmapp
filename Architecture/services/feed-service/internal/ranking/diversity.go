package ranking

import (
	"sort"
	"time"
)

// ApplyDiversity reorders scored candidates to enforce content diversity rules
// and returns at most `limit` items.
//
// Placement algorithm:
//  1. Sort by Score DESC.
//  2. Greedily place candidates while respecting constraints:
//     - Max 3 consecutive posts from the same author.
//     - Max 5 consecutive posts of the same content type.
//  3. If the page is short, place again with the content-type rule
//     relaxed but the author cap still enforced; only if it is STILL
//     short, place with both relaxed.
//  4. Apply a -0.1 per-consecutive-same-author penalty.
//  5. Freshness floor: at least 3 of the top 10 must be <4 h old.
func ApplyDiversity(scored []Candidate, limit int) []Candidate {
	if len(scored) == 0 {
		return nil
	}

	// Sort candidates by Score descending.
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	placed := make([]Candidate, 0, limit)
	used := make([]bool, len(scored))

	// Rolling windows for constraint checks. Shared across the passes
	// below so relaxing one rule does not amnesty the other.
	st := &placementState{
		lastAuthors: make([]string, 0, maxConsecutiveAuthor),
		lastTypes:   make([]string, 0, maxConsecutiveType),
	}

	// Placement runs in three passes, each relaxing one more rule.
	//
	// THE BUG THIS REPLACES
	//
	// There used to be two passes: everything, then nothing. The second
	// existed for a real case — "a cold-start user whose timeline has only
	// their own posts gets capped at 3, even though they expect to see all
	// of them" — but it dropped BOTH rules the moment EITHER of them
	// stalled the first pass, and on the video surfaces the type rule
	// stalls it every single time.
	//
	// Every candidate on /feed/videos and /feed/watch is a long video, so
	// after five placements the consecutive-content-type check rejected
	// the entire remaining pool, the first pass gave up at five, and the
	// relaxed pass filled the rest of the page in pure score order with no
	// author cap at all. On a page of twenty that is fifteen slots where
	// one creator could take every one.
	//
	// It did not show, because until the affinity signal was written every
	// author scored identically and the ordering was recency. Making
	// personalisation real is exactly what would have turned this into a
	// feed of one person. Fixing it here rather than weakening the
	// affinity term keeps the intended behaviour of both rules: the type
	// rule is advisory (it interleaves media when there is media to
	// interleave), the author rule is the one that protects the viewer
	// from a monoculture, and only a pool that genuinely has nothing else
	// in it should be able to set the author rule aside.
	placeCandidates(scored, used, &placed, limit, st, true, true)
	// Pass 2: drop the content-type rule, KEEP the author cap. This is
	// where a video-only surface finishes its page.
	placeCandidates(scored, used, &placed, limit, st, true, false)
	// Pass 3: drop everything. Only reachable when the remaining pool
	// cannot satisfy the author cap at all — the single-author timeline
	// the original relaxed pass was written for.
	placeCandidates(scored, used, &placed, limit, st, false, false)

	// --- Freshness floor ---
	// At least 3 of the top 10 must be < 4 h old.
	enforceFreshnessFloor(placed, scored, used, limit)

	if len(placed) > limit {
		placed = placed[:limit]
	}

	return placed
}

// enforceFreshnessFloor ensures that among the first min(10, len(placed))
// items, at least 3 are younger than 4 hours. If not, it swaps the
// lowest-scored items in positions 8-10 with fresher unplaced candidates.
func enforceFreshnessFloor(placed []Candidate, pool []Candidate, used []bool, limit int) {
	top := len(placed)
	if top > 10 {
		top = 10
	}
	if top == 0 {
		return
	}

	now := time.Now()
	fourHours := 4 * time.Hour

	// Count fresh items in the top window.
	freshCount := 0
	for i := 0; i < top; i++ {
		if now.Sub(placed[i].CreatedAt) < fourHours {
			freshCount++
		}
	}

	if freshCount >= 3 {
		return
	}

	// Collect fresh candidates from the remaining pool.
	var freshReplacements []Candidate
	for idx, c := range pool {
		if used[idx] {
			continue
		}
		if now.Sub(c.CreatedAt) < fourHours {
			freshReplacements = append(freshReplacements, c)
			used[idx] = true
		}
		if len(freshReplacements) >= 3-freshCount {
			break
		}
	}

	if len(freshReplacements) == 0 {
		return
	}

	// Determine swap range: positions 8-10 (0-indexed: 7-9), clamped to top.
	swapStart := 7
	if swapStart >= top {
		swapStart = top - 1
	}

	// Sort the swap zone by Score ascending so we replace the weakest first.
	swapEnd := top
	zone := placed[swapStart:swapEnd]
	sort.Slice(zone, func(i, j int) bool {
		return zone[i].Score < zone[j].Score
	})

	ri := 0
	for si := 0; si < len(zone) && ri < len(freshReplacements); si++ {
		// Only swap if the existing item is not already fresh.
		if now.Sub(zone[si].CreatedAt) >= fourHours {
			zone[si] = freshReplacements[ri]
			ri++
		}
	}
}

const (
	// maxConsecutiveAuthor is the run length one creator may occupy. The
	// rule that stops a personalised feed becoming one person's channel.
	maxConsecutiveAuthor = 3
	// maxConsecutiveType interleaves media formats where there is a mix
	// to interleave. Advisory: on a single-format surface it has nothing
	// to say, and must not be able to stall placement — see the passes in
	// ApplyDiversity.
	maxConsecutiveType = 5
	// sameAuthorPenalty is docked per consecutive slot a creator already
	// holds, so the second and third in a run have to be that much better
	// than the alternatives to earn their place.
	sameAuthorPenalty = 0.1
)

// placementState is the rolling window of what has been placed so far. It
// is threaded through the passes rather than rebuilt, so a run that starts
// under strict rules is still counted when a later pass relaxes them.
type placementState struct {
	lastAuthors []string
	lastTypes   []string
}

// placeCandidates greedily fills `placed` up to `limit` from the unused
// entries of `scored`, honouring whichever constraints are enabled. It
// returns when the page is full or when no remaining candidate satisfies
// the enabled rules.
func placeCandidates(scored []Candidate, used []bool, placed *[]Candidate, limit int, st *placementState, enforceAuthor, enforceType bool) {
	for len(*placed) < limit {
		found := false
		for idx := 0; idx < len(scored); idx++ {
			if used[idx] {
				continue
			}
			c := scored[idx]
			aid := c.AuthorID.String()

			consec := consecutiveTrailing(st.lastAuthors, aid)
			if enforceAuthor && consec >= maxConsecutiveAuthor {
				continue
			}
			if enforceType && consecutiveTrailing(st.lastTypes, c.ContentType) >= maxConsecutiveType {
				continue
			}

			// The same-author penalty is applied whether or not the cap
			// is being enforced: it is what makes a run cost something
			// even on the pass that permits it.
			if consec > 0 {
				c.Score -= sameAuthorPenalty * float64(consec)
			}

			*placed = append(*placed, c)
			used[idx] = true
			found = true

			st.lastAuthors = appendWindow(st.lastAuthors, aid, maxConsecutiveAuthor)
			st.lastTypes = appendWindow(st.lastTypes, c.ContentType, maxConsecutiveType)
			break
		}
		if !found {
			return
		}
	}
}

// consecutiveTrailing returns how many of the trailing entries in window
// match value.
func consecutiveTrailing(window []string, value string) int {
	count := 0
	for i := len(window) - 1; i >= 0; i-- {
		if window[i] == value {
			count++
		} else {
			break
		}
	}
	return count
}

// appendWindow appends value to window while keeping it at most maxLen entries.
func appendWindow(window []string, value string, maxLen int) []string {
	window = append(window, value)
	if len(window) > maxLen {
		window = window[len(window)-maxLen:]
	}
	return window
}
