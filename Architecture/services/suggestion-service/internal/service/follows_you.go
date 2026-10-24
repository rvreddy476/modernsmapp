package service

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/atpost/suggestion-service/internal/store"
	"github.com/google/uuid"
)

/*
	"Follows you" (founder, 25 Oct 2026): people who follow the viewer and
	whom the viewer has not followed back are a People-you-may-know source,
	shown with the only CTA a suggestion card has, Follow.

	It reaches the list two ways:

	  - the friend batch scores the viewer's newest followsYouBatchPool (of store.FollowerScanLimit read)
	    followers alongside the other sources (runFriendBatchForUser);
	  - the read path merges the LIVE follower list into the stored
	    candidates on every cache miss (liveFollowsYou), because the batch
	    runs every six hours and a new follower should appear as soon as the
	    follow event clears the viewer's cache. The same merge drops a stored
	    follows-you card whose follower has since unfollowed, been followed
	    back, blocked, hidden or dismissed.

	Either way at most one card in followsYouOneIn is a follows-you card
	(capFollowsYouShare), unless nothing else is left, so a popular account's
	followers do not flood the list.
*/

// followsYouOneIn caps the follows-you share: in every prefix of the list at
// most ceil(n / followsYouOneIn) cards are follows-you cards (slots 1, 4, 7…
// when every candidate is one), unless only follows-you cards are left.
const followsYouOneIn = 3

// followsYouBatchPool is how many of the viewer's newest not-followed-back
// followers the friend batch scores. The store reads up to
// store.FollowerScanLimit so older followers found by other sources still
// carry FOLLOWS_YOU; scoring all of them costs two queries each.
const followsYouBatchPool = 100

// readPathCandidateLimit is how many candidates the read path keeps, the
// same number it reads from the store.
const readPathCandidateLimit = 50

// followsYouReader is the store surface the read-path merge needs.
type followsYouReader interface {
	GetActiveCooldowns(ctx context.Context, viewerID uuid.UUID) (map[uuid.UUID]store.CooldownEntry, error)
	GetFollowersNotFollowedBack(ctx context.Context, viewerID uuid.UUID, excludeIDs []uuid.UUID) ([]store.FollowerEdge, error)
}

// liveFollowsYou merges the viewer's live followers-not-followed-back into
// the stored friend candidates. A failed read returns stored unchanged: a
// cooldown that cannot be read could re-add someone the viewer dismissed, and
// a follower list that cannot be read cannot tell a stale card from a live one.
func liveFollowsYou(ctx context.Context, r followsYouReader, viewerID uuid.UUID, stored []store.SuggestionCandidate, now time.Time) []store.SuggestionCandidate {
	cooldowns, err := r.GetActiveCooldowns(ctx, viewerID)
	if err != nil {
		log.Printf("[suggestions] follows-you: cooldowns for %s: %v", viewerID, err)
		return stored
	}
	exclude := make([]uuid.UUID, 0, len(cooldowns))
	for id := range cooldowns {
		exclude = append(exclude, id)
	}
	followers, err := r.GetFollowersNotFollowedBack(ctx, viewerID, exclude)
	if err != nil {
		log.Printf("[suggestions] follows-you: followers of %s: %v", viewerID, err)
		return stored
	}
	return mergeFollowsYou(viewerID, stored, followers, now, readPathCandidateLimit)
}

// mergeFollowsYou combines stored candidates (score order) with the viewer's
// live followers-not-followed-back (newest first), then re-sorts, caps the
// follows-you share and truncates to limit (0 = no limit).
//
//   - A stored candidate who is a live follower but was stored without the
//     reason (they followed after the batch) gains it.
//   - A stored follows-you candidate who is no longer a live follower is
//     dropped: its main reason is gone.
//   - A live follower not stored becomes a new follows-you candidate.
func mergeFollowsYou(viewerID uuid.UUID, stored []store.SuggestionCandidate, followers []store.FollowerEdge, now time.Time, limit int) []store.SuggestionCandidate {
	live := make(map[uuid.UUID]time.Time, len(followers))
	for _, f := range followers {
		live[f.UserID] = f.FollowedAt
	}

	out := make([]store.SuggestionCandidate, 0, len(stored)+len(live))
	seen := make(map[uuid.UUID]bool, len(stored)+len(live))
	for _, c := range stored {
		followedAt, follows := live[c.CandidateID]
		switch {
		case !follows && isFollowsYou(c):
			continue
		case follows && !hasReason(c.ReasonCodes, ReasonFollowsYou):
			c = withFollowsYou(c, followedAt, now)
		}
		seen[c.CandidateID] = true
		out = append(out, c)
	}
	for _, f := range followers {
		if f.UserID == viewerID || seen[f.UserID] {
			continue
		}
		seen[f.UserID] = true
		res := ScoreFriendCandidate(ScoringInput{
			FollowsYou:        true,
			FollowsYouAgeDays: ageDays(f.FollowedAt, now),
		})
		out = append(out, store.SuggestionCandidate{
			ViewerID:       viewerID,
			CandidateID:    f.UserID,
			SuggestionType: "friend",
			BaseScore:      res.Score,
			ReasonCodes:    res.ReasonCodes,
			ExplainText:    res.ExplainText,
			SourceBucket:   BucketFollowsYou,
			GeneratedAt:    now,
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].BaseScore > out[j].BaseScore })
	out = capFollowsYouShare(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// withFollowsYou adds the follows-you signal to a candidate stored without
// it: its score, its reason code first, "Follows you" first in the explain
// text, and the follows-you bucket.
func withFollowsYou(c store.SuggestionCandidate, followedAt, now time.Time) store.SuggestionCandidate {
	c.BaseScore += float32(followsYouScore(ageDays(followedAt, now)))
	c.ReasonCodes = append([]string{ReasonFollowsYou}, c.ReasonCodes...)
	parts := []string{FollowsYouText}
	if first, _, _ := strings.Cut(c.ExplainText, " • "); first != "" {
		parts = append(parts, first)
	}
	c.ExplainText = buildExplainText(parts)
	c.SourceBucket = BucketFollowsYou
	return c
}

// capFollowsYouShare reorders a score-sorted list so that no prefix holds
// more than ceil(n / followsYouOneIn) follows-you cards, while there are
// other cards to place. Order within each group is kept, so a list already
// within the cap is unchanged.
func capFollowsYouShare(in []store.SuggestionCandidate) []store.SuggestionCandidate {
	var followers, others []int
	for i, c := range in {
		if isFollowsYou(c) {
			followers = append(followers, i)
		} else {
			others = append(others, i)
		}
	}
	if len(followers) == 0 || len(others) == 0 {
		return in
	}
	out := make([]store.SuggestionCandidate, 0, len(in))
	fi, oi, placed := 0, 0, 0
	for len(out) < len(in) {
		takeFollower := fi < len(followers) && (oi >= len(others) || followers[fi] < others[oi])
		if takeFollower && oi < len(others) && placed >= len(out)/followsYouOneIn+1 {
			takeFollower = false
		}
		if takeFollower {
			out = append(out, in[followers[fi]])
			fi++
			placed++
		} else {
			out = append(out, in[others[oi]])
			oi++
		}
	}
	return out
}

func isFollowsYou(c store.SuggestionCandidate) bool {
	return c.SourceBucket == BucketFollowsYou || hasReason(c.ReasonCodes, ReasonFollowsYou)
}

func hasReason(codes []string, code string) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

// ageDays is how many days before now t was (negative for a future t, which
// followsYouScore counts as today).
func ageDays(t, now time.Time) float64 {
	return now.Sub(t).Hours() / 24
}
