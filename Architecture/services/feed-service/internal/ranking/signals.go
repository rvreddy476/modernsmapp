package ranking

import (
	"context"
	"fmt"
	"log"
	"strconv"

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
	ContentQuality   map[string]float64 // post_id -> CQS (Content Quality Score)
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
		ContentQuality:   make(map[string]float64, len(candidates)),
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

	// --- 2. Media preferences: HGETALL user:media_prefs:{viewerID}
	mediaKey := fmt.Sprintf("user:media_prefs:%s", viewerID.String())
	mediaCmd := pipe.HGetAll(ctx, mediaKey)

	// --- 3. Post velocities: pipelined ZSCORE post:velocity:ranked postID
	velocityCmds := make(map[string]*redis.FloatCmd, len(candidates))
	for _, c := range candidates {
		pid := c.PostID.String()
		velocityCmds[pid] = pipe.ZScore(ctx, "post:velocity:ranked", pid)
	}

	// --- 4. Interaction checks: pipelined SISMEMBER post:likers:{postID} viewerID
	interactionCmds := make(map[string]*redis.BoolCmd, len(candidates))
	for _, c := range candidates {
		pid := c.PostID.String()
		interactionCmds[pid] = pipe.SIsMember(ctx, fmt.Sprintf("post:likers:%s", pid), viewerID.String())
	}

	// --- 5. Mutual follows: SMEMBERS user:mutual_follows:{viewerID}
	mutualKey := fmt.Sprintf("user:mutual_follows:%s", viewerID.String())
	mutualCmd := pipe.SMembers(ctx, mutualKey)

	// --- 6. Content Quality Scores: pipelined GET post:cqs:{postID}
	cqsCmds := make(map[string]*redis.StringCmd, len(candidates))
	for _, c := range candidates {
		pid := c.PostID.String()
		cqsCmds[pid] = pipe.Get(ctx, fmt.Sprintf("post:cqs:%s", pid))
	}

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

	// --- Harvest 3: velocities
	for pid, cmd := range velocityCmds {
		if v, err := cmd.Result(); err == nil {
			vs.Velocities[pid] = v
		}
		// redis.Nil means the member does not exist in the sorted set; treat as 0.
	}

	// --- Harvest 4: interactions (Redis primary)
	for pid, cmd := range interactionCmds {
		if v, err := cmd.Result(); err == nil {
			vs.Interactions[pid] = v
		}
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

	// --- Harvest 6: content quality scores
	for pid, cmd := range cqsCmds {
		if v, err := cmd.Float64(); err == nil {
			vs.ContentQuality[pid] = v
		}
		// redis.Nil means no CQS cached; treat as 0.
	}

	return vs, nil
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
