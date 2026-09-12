package ranking

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ViewerSignals holds all scoring signals fetched from Redis for a viewer.
type ViewerSignals struct {
	AuthorAffinities map[string]float64 // author_id -> affinity score
	MediaPrefs       MediaPrefs
	Velocities       map[string]float64 // post_id -> velocity
	Interactions     map[string]bool    // post_id -> already interacted
	MutualFollows    map[string]bool    // author_id -> mutual follow
	// Subscribed is the set of candidate authors whose Tube channel the
	// viewer subscribes to, read from user:subscribed_owners:{viewerID}
	// (SubscribedOwnersKey). feed-service writes that set itself, on
	// demand, for the Subscriptions tab (service/subscriptions.go); the
	// ranker reads the same key so the two surfaces cannot disagree about
	// who is subscribed. An absent author is simply not subscribed, which
	// is what a viewer with no subscriptions gets for everyone.
	Subscribed     map[string]bool    // author_id -> subscribed to their channel
	ContentQuality map[string]float64 // post_id -> CQS (Content Quality Score)
	// AuthorFeedback is the viewer's net "Interested" (+1) / "Not
	// interested" (-1) answers per author, mirrored from feed_feedback by
	// service.RecordFeedback into feed:author_feedback:{viewerID}. Absent
	// authors are 0. Feeds the authorPenalty term in ScoreCandidates.
	AuthorFeedback map[string]float64 // author_id -> net feedback

	// TopicAffinity is how much this viewer likes a SUBJECT rather than a
	// person: `cat:<category>` and `tag:<hashtag>` tokens scored on the
	// same [0,1] scale as AuthorAffinities, written by analytics-service's
	// personalization warmer into user:topic_affinity:{viewerID}.
	//
	// Author affinity alone cannot answer "more videos like this one" for
	// a creator the viewer has never seen — this is what can. Absent
	// tokens are neutral, which is what a cold viewer gets for everything.
	TopicAffinity map[string]float64 // topic token -> affinity

	// PostTopics is the catalogue side of the same signal: which topic
	// tokens each candidate carries, projected into post:topics:{postID}
	// by feed-service's own pipeline (pipeline/topics.go). It has to be a
	// projection rather than a field on the timeline row because ranking
	// runs BEFORE hydration on every surface, so the post's category and
	// hashtags are not yet known when the score is computed.
	PostTopics map[string][]string // post_id -> topic tokens

	// Completions is what the viewer has already watched to the end
	// (>= 95 %), from user:completions:{viewerID}. The related-videos
	// endpoint excludes these outright; the scorer folds them into the
	// already-interacted penalty on the ordinary feeds.
	Completions map[string]bool // post_id -> finished it

	// Seed is set only by the related-videos path: the video the viewer is
	// watching now, whose author and topics the candidates are being
	// judged against. Nil on every ordinary feed request, which is what
	// makes the relatedness term free for them.
	Seed *Seed
}

// Seed is the "playing now" video the related-videos endpoint ranks
// against. It lives on ViewerSignals rather than in a second ranker so
// that one scoring formula, one diversity pass and one set of penalties
// serve both surfaces.
type Seed struct {
	PostID   uuid.UUID
	AuthorID uuid.UUID
	Topics   []string
}

// TopicAffinityKey is the Redis hash (topic token -> affinity) written by
// analytics-service's personalization warmer. The two services agree on
// this string and nothing enforces it, which is why both sides name it.
func TopicAffinityKey(viewerID uuid.UUID) string {
	return fmt.Sprintf("user:topic_affinity:%s", viewerID.String())
}

// CompletionsKey is the Redis set of content the viewer finished, written
// by the same warmer.
func CompletionsKey(viewerID uuid.UUID) string {
	return fmt.Sprintf("user:completions:%s", viewerID.String())
}

// PostTopicsKey is the Redis string holding a post's comma-separated topic
// tokens, written by feed-service's own topic projection.
func PostTopicsKey(postID uuid.UUID) string {
	return fmt.Sprintf("post:topics:%s", postID.String())
}

// ParseTopics splits a stored post:topics value. Empty tokens are dropped
// so a trailing comma or an empty projection yields no topics rather than
// one anonymous topic every post would share.
func ParseTopics(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SubscribedOwnersKey is the Redis set of channel-owner user ids the
// viewer subscribes to, written by feed-service's own subscription cache
// (service/subscriptions.go) and read here for the subscription boost. One
// key, named once, so the Subscriptions tab and the ranker read the same
// answer.
func SubscribedOwnersKey(viewerID uuid.UUID) string {
	return fmt.Sprintf("user:subscribed_owners:%s", viewerID.String())
}

// AuthorFeedbackKey is the Redis hash (author_id -> net feedback) the
// scorer reads for a viewer. Written by feed-service's feedback path.
func AuthorFeedbackKey(viewerID uuid.UUID) string {
	return fmt.Sprintf("feed:author_feedback:%s", viewerID.String())
}

// MediaPrefs stores the viewer's per-media-type dwell time percentiles.
type MediaPrefs struct {
	VideoP95Dwell float64
	ImageP95Dwell float64
	TextP95Dwell  float64
}

// SignalLoader fetches all scoring signals from Redis via pipelined calls.
// An optional ScyllaDB session provides a durable fallback for interaction
// checks when Redis data has expired.
type SignalLoader struct {
	rdb    *redis.Client
	scylla *gocql.Session
}

// NewSignalLoader creates a SignalLoader backed by Redis with an optional
// ScyllaDB session for durable interaction verification.
func NewSignalLoader(rdb *redis.Client, scyllaSession *gocql.Session) *SignalLoader {
	return &SignalLoader{rdb: rdb, scylla: scyllaSession}
}

// LoadSignals fetches viewer-specific scoring signals for the given candidates
// using Redis pipelines to batch all lookups. On any Redis error the method
// returns partial signals rather than failing the entire ranking pass.
func (sl *SignalLoader) LoadSignals(ctx context.Context, viewerID uuid.UUID, candidates []Candidate) (*ViewerSignals, error) {
	vs := &ViewerSignals{
		AuthorAffinities: make(map[string]float64, len(candidates)),
		Velocities:       make(map[string]float64, len(candidates)),
		Interactions:     make(map[string]bool, len(candidates)),
		MutualFollows:    make(map[string]bool),
		Subscribed:       make(map[string]bool),
		ContentQuality:   make(map[string]float64, len(candidates)),
		AuthorFeedback:   make(map[string]float64, len(candidates)),
		TopicAffinity:    make(map[string]float64),
		PostTopics:       make(map[string][]string, len(candidates)),
		Completions:      make(map[string]bool, len(candidates)),
	}

	if len(candidates) == 0 {
		return vs, nil
	}

	// Collect unique author IDs for the affinity lookup.
	authorSet := make(map[string]struct{}, len(candidates))
	authorFields := make([]string, 0, len(candidates))
	for _, c := range candidates {
		aid := c.AuthorID.String()
		if _, exists := authorSet[aid]; !exists {
			authorSet[aid] = struct{}{}
			authorFields = append(authorFields, aid)
		}
	}

	pipe := sl.rdb.Pipeline()

	// --- 1. Author affinities: HMGET user:affinities:{viewerID} author1 author2 ...
	affinityKey := fmt.Sprintf("user:affinities:%s", viewerID.String())
	affinityCmd := pipe.HMGet(ctx, affinityKey, authorFields...)

	// --- 1b. Author feedback: HMGET feed:author_feedback:{viewerID} author1 ...
	// Same shape as the affinity hash, same single command.
	feedbackCmd := pipe.HMGet(ctx, AuthorFeedbackKey(viewerID), authorFields...)

	// --- 2. Media preferences: HGETALL user:media_prefs:{viewerID}
	mediaKey := fmt.Sprintf("user:media_prefs:%s", viewerID.String())
	mediaCmd := pipe.HGetAll(ctx, mediaKey)

	// Collect unique post IDs (in stable order) for the per-post commands.
	// Velocity (ZMScore on one sorted set) and CQS (MGet on per-post keys)
	// both collapse into a single command each.
	postIDStrs := make([]string, 0, len(candidates))
	cqsKeys := make([]string, 0, len(candidates))
	for _, c := range candidates {
		pid := c.PostID.String()
		postIDStrs = append(postIDStrs, pid)
		cqsKeys = append(cqsKeys, fmt.Sprintf("post:cqs:%s", pid))
	}

	// --- 3. Post velocities: ZMSCORE post:velocity:ranked pid1 pid2 ...
	// Audit HF2: previously N pipelined ZSCORE calls — same RTT but
	// N Redis command dispatches. ZMSCORE collapses them into one.
	velocityCmd := pipe.ZMScore(ctx, "post:velocity:ranked", postIDStrs...)

	// --- 4. Interaction checks: per-post SISMEMBER (each post has its
	// own likers set; can't batch across sets). The pipeline still
	// sends them all in one network round-trip.
	interactionCmds := make(map[string]*redis.BoolCmd, len(candidates))
	for _, c := range candidates {
		pid := c.PostID.String()
		interactionCmds[pid] = pipe.SIsMember(ctx, fmt.Sprintf("post:likers:%s", pid), viewerID.String())
	}

	// --- 5. Mutual follows: SMEMBERS user:mutual_follows:{viewerID}
	mutualKey := fmt.Sprintf("user:mutual_follows:%s", viewerID.String())
	mutualCmd := pipe.SMembers(ctx, mutualKey)

	// --- 5b. Subscribed channel owners: SMEMBERS user:subscribed_owners:{viewerID}
	// The whole set rather than SMISMEMBER of the candidate authors: it is
	// the same shape as the mutual set above, small (a viewer subscribes
	// to tens of channels, not thousands), and one more command in a
	// pipeline that already carries a dozen.
	subscribedCmd := pipe.SMembers(ctx, SubscribedOwnersKey(viewerID))

	// --- 6. Content Quality Scores: MGET of N per-post keys (one
	// command instead of N pipelined GETs — audit HF2).
	var cqsCmd *redis.SliceCmd
	if len(cqsKeys) > 0 {
		cqsCmd = pipe.MGet(ctx, cqsKeys...)
	}

	// --- 7. Topic affinity: HGETALL user:topic_affinity:{viewerID}.
	// The whole hash rather than an HMGET of the candidates' tokens: the
	// tokens are not known until the post:topics MGET below has come back,
	// and the hash is capped at 100 fields by the writer, so fetching it
	// whole costs less than a second round trip would.
	topicAffinityCmd := pipe.HGetAll(ctx, TopicAffinityKey(viewerID))

	// --- 8. Candidate topics: MGET post:topics:{postID} ... (one command)
	topicKeys := make([]string, 0, len(postIDStrs))
	for _, pid := range postIDStrs {
		topicKeys = append(topicKeys, "post:topics:"+pid)
	}
	var postTopicsCmd *redis.SliceCmd
	if len(topicKeys) > 0 {
		postTopicsCmd = pipe.MGet(ctx, topicKeys...)
	}

	// --- 9. Completions: SMISMEMBER user:completions:{viewerID} pid ...
	// One command for the whole page, unlike the per-post likers sets
	// above which each live in their own key.
	completionsCmd := pipe.SMIsMember(ctx, CompletionsKey(viewerID), toAnySlice(postIDStrs)...)

	// Execute the pipeline.
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		log.Printf("ranking/signals: pipeline exec partial error: %v", err)
		// Continue to harvest whatever results we can.
	}

	// --- Harvest 1: affinities
	if vals, err := affinityCmd.Result(); err == nil {
		for i, v := range vals {
			if v == nil {
				continue
			}
			if s, ok := v.(string); ok {
				if f, err := strconv.ParseFloat(s, 64); err == nil {
					vs.AuthorAffinities[authorFields[i]] = f
				}
			}
		}
	} else {
		log.Printf("ranking/signals: affinities fetch error: %v", err)
	}

	// --- Harvest 1b: author feedback (absent field ⇒ 0, nothing to record)
	if vals, err := feedbackCmd.Result(); err == nil {
		for i, v := range vals {
			if s, ok := v.(string); ok {
				if f, err := strconv.ParseFloat(s, 64); err == nil {
					vs.AuthorFeedback[authorFields[i]] = f
				}
			}
		}
	} else {
		log.Printf("ranking/signals: author feedback fetch error: %v", err)
	}

	// --- Harvest 2: media preferences
	if m, err := mediaCmd.Result(); err == nil {
		if v, ok := m["video_p95_dwell"]; ok {
			vs.MediaPrefs.VideoP95Dwell, _ = strconv.ParseFloat(v, 64)
		}
		if v, ok := m["image_p95_dwell"]; ok {
			vs.MediaPrefs.ImageP95Dwell, _ = strconv.ParseFloat(v, 64)
		}
		if v, ok := m["text_p95_dwell"]; ok {
			vs.MediaPrefs.TextP95Dwell, _ = strconv.ParseFloat(v, 64)
		}
	} else {
		log.Printf("ranking/signals: media prefs fetch error: %v", err)
	}

	// --- Harvest 3: velocities (ZMSCORE returns []float64 aligned with input)
	if scores, err := velocityCmd.Result(); err == nil {
		for i, pid := range postIDStrs {
			if i < len(scores) {
				// ZMScore returns 0 for non-members; treat as 0 implicitly.
				vs.Velocities[pid] = scores[i]
			}
		}
	}

	// --- Harvest 4: interactions (Redis primary)
	for pid, cmd := range interactionCmds {
		if v, err := cmd.Result(); err == nil {
			vs.Interactions[pid] = v
		}
	}

	// --- Harvest 4a: completions. A video the viewer watched to the end
	// is "already interacted with" in every sense the scorer's penalty
	// means, so it is folded into the same map rather than given a second
	// term. Doing it here rather than as a hard exclusion is deliberate:
	// dropping finished posts outright would shrink a small feed, and the
	// existing -0.5 already pushes them to the back of the page. The
	// related-videos endpoint, where "you just watched this" is
	// unambiguous, excludes them outright instead — see ExcludeSeen.
	if seen, err := completionsCmd.Result(); err == nil {
		for i, pid := range postIDStrs {
			if i < len(seen) && seen[i] {
				vs.Completions[pid] = true
				vs.Interactions[pid] = true
			}
		}
	} else if err != redis.Nil {
		log.Printf("ranking/signals: completions fetch error: %v", err)
	}

	// --- Harvest 4b: ScyllaDB fallback for posts where Redis returned false
	if sl.scylla != nil {
		var unchecked []uuid.UUID
		for _, c := range candidates {
			pid := c.PostID.String()
			if !vs.Interactions[pid] {
				unchecked = append(unchecked, c.PostID)
			}
		}
		if len(unchecked) > 0 {
			scyllaInteractions := sl.checkScyllaInteractions(ctx, viewerID, unchecked)
			for pid, v := range scyllaInteractions {
				if v {
					vs.Interactions[pid] = true
				}
			}
		}
	}

	// --- Harvest 5: mutual follows (intersect with candidate authors)
	if members, err := mutualCmd.Result(); err == nil {
		mutualSet := make(map[string]bool, len(members))
		for _, m := range members {
			mutualSet[m] = true
		}
		for aid := range authorSet {
			if mutualSet[aid] {
				vs.MutualFollows[aid] = true
			}
		}
	} else {
		log.Printf("ranking/signals: mutual follows fetch error: %v", err)
	}

	// --- Harvest 5b: subscribed owners (intersect with candidate authors)
	if members, err := subscribedCmd.Result(); err == nil {
		subSet := make(map[string]bool, len(members))
		for _, m := range members {
			subSet[m] = true
		}
		for aid := range authorSet {
			if subSet[aid] {
				vs.Subscribed[aid] = true
			}
		}
	} else {
		log.Printf("ranking/signals: subscribed owners fetch error: %v", err)
	}

	// --- Harvest 6: content quality scores (MGET — []any aligned with input)
	if cqsCmd != nil {
		if vals, err := cqsCmd.Result(); err == nil {
			for i, pid := range postIDStrs {
				if i >= len(vals) || vals[i] == nil {
					continue
				}
				s, ok := vals[i].(string)
				if !ok {
					continue
				}
				if f, err := strconv.ParseFloat(s, 64); err == nil {
					vs.ContentQuality[pid] = f
				}
			}
		}
	}

	// --- Harvest 7: viewer topic affinity
	if m, err := topicAffinityCmd.Result(); err == nil {
		for token, raw := range m {
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				vs.TopicAffinity[token] = f
			}
		}
	} else if err != redis.Nil {
		log.Printf("ranking/signals: topic affinity fetch error: %v", err)
	}

	// --- Harvest 8: candidate topics
	if postTopicsCmd != nil {
		if vals, err := postTopicsCmd.Result(); err == nil {
			for i, pid := range postIDStrs {
				if i >= len(vals) || vals[i] == nil {
					continue
				}
				if s, ok := vals[i].(string); ok {
					if topics := ParseTopics(s); len(topics) > 0 {
						vs.PostTopics[pid] = topics
					}
				}
			}
		}
	}

	return vs, nil
}

// toAnySlice adapts a []string to the []any the variadic Redis commands
// take. go-redis models set members as `any` because a set may hold
// numbers as readily as strings.
func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// checkScyllaInteractions queries the user_post_interactions table in ScyllaDB
// for posts where Redis had no interaction data. This provides a durable
// fallback so that the interaction penalty is accurate even after Redis expiry.
func (sl *SignalLoader) checkScyllaInteractions(ctx context.Context, viewerID uuid.UUID, postIDs []uuid.UUID) map[string]bool {
	result := make(map[string]bool, len(postIDs))

	gocqlIDs := make([]interface{}, len(postIDs))
	for i, id := range postIDs {
		gocqlIDs[i] = gocql.UUID(id)
	}

	iter := sl.scylla.Query(`
		SELECT post_id FROM user_post_interactions
		WHERE user_id = ? AND post_id IN ?`,
		gocql.UUID(viewerID), gocqlIDs,
	).WithContext(ctx).Iter()

	var pid gocql.UUID
	for iter.Scan(&pid) {
		result[uuid.UUID(pid).String()] = true
	}
	if err := iter.Close(); err != nil {
		log.Printf("ranking/signals: ScyllaDB interaction check error: %v", err)
	}

	return result
}
