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
//  3. Apply a -0.1 per-consecutive-same-author penalty.
//  4. Freshness floor: at least 3 of the top 10 must be <4 h old.
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

	// Rolling windows for constraint checks.
	lastAuthors := make([]string, 0, 3) // track up to last 3 placed author IDs
	lastTypes := make([]string, 0, 5)   // track up to last 5 placed content types

	for len(placed) < limit {
		found := false
		for idx := 0; idx < len(scored); idx++ {
			if used[idx] {
				continue
			}
			c := scored[idx]
			aid := c.AuthorID.String()

			// --- Constraint: max 3 consecutive same author ---
			if consecutiveTrailing(lastAuthors, aid) >= 3 {
				continue
			}

			// --- Constraint: max 5 consecutive same content type ---
			if consecutiveTrailing(lastTypes, c.ContentType) >= 5 {
				continue
			}

			// Apply same-author penalty for consecutive runs.
			consec := consecutiveTrailing(lastAuthors, aid)
			if consec > 0 {
				c.Score -= 0.1 * float64(consec)
			}

			// Place candidate.
			placed = append(placed, c)
			used[idx] = true
			found = true

			// Update rolling windows.
			lastAuthors = appendWindow(lastAuthors, aid, 3)
			lastTypes = appendWindow(lastTypes, c.ContentType, 5)
			break
		}
		if !found {
			// No more candidates can satisfy constraints; stop.
			break
		}
	}

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
