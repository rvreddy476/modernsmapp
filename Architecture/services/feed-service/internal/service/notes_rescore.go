package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Echo For You rescoring: lessons from X's published For You code, applied
// with the real counts Echo has (docs/designs/echo-for-you-ranking.md).
// It runs after the shared ranker and only on the Echo For You pool.

const (
	// X's action weights (home-mixer/params/param.rs), on real counts.
	echoLikeWeight     = 0.7
	echoRepostWeight   = 1.0
	echoReplyWeight    = 5.0
	echoQuoteWeight    = 5.0
	echoBookmarkWeight = 2.0
	// echoViewPrior is phantom views added to every note, so a note with
	// one view and one like does not read as "every viewer acts".
	echoViewPrior = 20.0
	// echoConversationWeight scales the per-view rate (capped at 1).
	echoConversationWeight = 0.6

	// Out-of-network discount: X multiplies posts from accounts the viewer
	// does not follow by a factor below 1.
	echoOONFactor     = 0.75
	echoHashtagFactor = 0.9

	// Author decay, X's (1-floor)·decay^n + floor.
	echoAuthorDecay = 0.5
	echoAuthorFloor = 0.25

	// Already-shown demotion: served in the last echoServedWindow.
	echoServedFactor = 0.35
	echoServedWindow = 48 * time.Hour
	echoServedMax    = 2000

	// Fresh-note lift: young notes few have seen.
	echoFreshLift     = 0.1
	echoFreshMaxAge   = 24 * time.Hour
	echoFreshMaxViews = 30
)

// noteSignals are one note's counts, read from post-service's Note JSON.
type noteSignals struct {
	Likes     int64 `json:"like_count"`
	Reposts   int64 `json:"repost_count"`
	Replies   int64 `json:"reply_count"`
	Quotes    int64 `json:"quote_count"`
	Bookmarks int64 `json:"bookmark_count"`
	Views     int64 `json:"view_count"`
}

// conversationScore is the weighted actions per view, capped at 1.
func (n noteSignals) conversationScore() float64 {
	w := echoLikeWeight*float64(n.Likes) + echoRepostWeight*float64(n.Reposts) +
		echoReplyWeight*float64(n.Replies) + echoQuoteWeight*float64(n.Quotes) +
		echoBookmarkWeight*float64(n.Bookmarks)
	views := math.Max(0, float64(n.Views))
	return math.Min(1, w/(views+echoViewPrior))
}

// sourceFactor is the out-of-network discount for a candidate's source.
func sourceFactor(source string) float64 {
	switch source {
	case sourceTimeline:
		return 1
	case sourceEchoHashtag:
		return echoHashtagFactor
	default:
		return echoOONFactor
	}
}

// rescoreForYou applies the conversation score, out-of-network discount,
// already-shown demotion and fresh-note lift to the ranker's scores,
// re-sorts, then applies author decay and re-sorts again. Pure: signals
// missing for a note mean no counts, never a drop.
func rescoreForYou(ranked []FeedItem, signals map[uuid.UUID]noteSignals, served map[uuid.UUID]bool, now time.Time) []FeedItem {
	out := append([]FeedItem(nil), ranked...)
	for i := range out {
		c := &out[i]
		sig, known := signals[c.PostID]
		score := c.Score
		if known {
			score += echoConversationWeight * sig.conversationScore()
			if now.Sub(c.CreatedAt) < echoFreshMaxAge && sig.Views < echoFreshMaxViews {
				score += echoFreshLift
			}
		}
		score = scaleScore(score, sourceFactor(c.Source))
		if served[c.PostID] {
			score = scaleScore(score, echoServedFactor)
		}
		c.Score = score
	}
	sortByScore(out)
	seen := make(map[uuid.UUID]int, len(out))
	for i := range out {
		n := seen[out[i].AuthorID]
		seen[out[i].AuthorID] = n + 1
		if n > 0 {
			out[i].Score = scaleScore(out[i].Score, (1-echoAuthorFloor)*math.Pow(echoAuthorDecay, float64(n))+echoAuthorFloor)
		}
	}
	sortByScore(out)
	return out
}

// scaleScore multiplies a positive score; a negative one is pushed further
// down instead, so a discount never lifts a penalised note.
func scaleScore(score, factor float64) float64 {
	if score >= 0 {
		return score * factor
	}
	return score / factor
}

func sortByScore(items []FeedItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
}

// noteRankSignals reads the counts of ids for viewerID through the notes
// batch, in parallel chunks. A failed chunk is skipped: rescoring is best
// effort.
func (s *Service) noteRankSignals(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID) map[uuid.UUID]noteSignals {
	out := make(map[uuid.UUID]noteSignals, len(ids))
	if s.postServiceURL == "" || len(ids) == 0 {
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for start := 0; start < len(ids); start += notesBatchMax {
		end := min(start+notesBatchMax, len(ids))
		wg.Add(1)
		go func(chunk []uuid.UUID) {
			defer wg.Done()
			rows, err := s.fetchNotesBatch(ctx, chunk, viewerID)
			if err != nil {
				log.Printf("echo: For You counts unavailable for %d notes: %v", len(chunk), err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, r := range rows {
				var sig noteSignals
				if json.Unmarshal(r.raw, &sig) == nil {
					out[r.post.ID] = sig
				}
			}
		}(ids[start:end])
	}
	wg.Wait()
	return out
}

func notesServedKey(viewerID uuid.UUID) string {
	return fmt.Sprintf("feed:echo:served:%s", viewerID)
}

// servedNotes is the set of notes served to viewerID in the last
// echoServedWindow. Best effort: no Redis, no demotion.
func (s *Service) servedNotes(ctx context.Context, viewerID uuid.UUID, now time.Time) map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	if s.rdb == nil {
		return out
	}
	members, err := s.rdb.ZRangeByScore(ctx, notesServedKey(viewerID), &redis.ZRangeBy{
		Min: strconv.FormatInt(now.Add(-echoServedWindow).Unix(), 10), Max: "+inf",
	}).Result()
	if err != nil {
		return out
	}
	for _, m := range members {
		if id, err := uuid.Parse(m); err == nil {
			out[id] = true
		}
	}
	return out
}

// recordServedNotes notes a served page for the next build's demotion,
// trimming entries past the window and beyond echoServedMax.
func (s *Service) recordServedNotes(ctx context.Context, viewerID uuid.UUID, page []FeedItem, now time.Time) {
	if s.rdb == nil || len(page) == 0 {
		return
	}
	key := notesServedKey(viewerID)
	zs := make([]redis.Z, 0, len(page))
	for _, it := range page {
		zs = append(zs, redis.Z{Score: float64(now.Unix()), Member: it.PostID.String()})
	}
	pipe := s.rdb.Pipeline()
	pipe.ZAdd(ctx, key, zs...)
	pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(now.Add(-echoServedWindow).Unix(), 10))
	pipe.ZRemRangeByRank(ctx, key, 0, -echoServedMax-1)
	pipe.Expire(ctx, key, echoServedWindow)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("echo: served notes for %s not recorded: %v", viewerID, err)
	}
}
