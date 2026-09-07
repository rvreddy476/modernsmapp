package pipeline

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/atpost/feed-service/internal/ranking"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// The post-topic projection: post:topics:{postID}.
//
// The ranker needs to know what a candidate is ABOUT in order to weigh it
// against what the viewer watches. It cannot ask post-service: on every
// surface except the category pages, ranking happens BEFORE hydration —
// the candidates at that point are Scylla timeline rows carrying a post
// id, an author id, a timestamp and a content type, and nothing else. A
// per-candidate HTTP call to fetch a category would put post-service
// inside the 20 ms ranking budget, which is not a trade anyone should
// make for a 0.20-weight term.
//
// So the subject metadata is projected into Redis ahead of time, keyed by
// post, and read by the same pipelined MGET that already fetches the
// quality scores. One extra Redis command for a whole page.
//
// WHY FEED-SERVICE OWNS THIS AND ANALYTICS DOES NOT
//
// The other four signals are conclusions drawn from watch history, and
// analytics-service owns watch history. This one is not a conclusion about
// anybody — it is a restatement of what the post already says about
// itself, reshaped for the reader. The reader is the ranker, the ranker
// lives here, and the key's format is the ranker's business. Putting it in
// analytics-service would mean analytics owning a cache whose contents it
// never consults and whose shape it does not control.
//
// WHAT A TOKEN IS
//
// `cat:<taxonomy id>` from posts.category, and `tag:<tag>` from
// posts.hashtags and posts.tags, all lowercased. The same vocabulary
// analytics-service's topic-affinity scan builds its hash from — the two
// sides have to agree on the spelling or nothing ever matches, which is
// why both derive it from the same three columns by the same rule.

// TopicProjector maintains post:topics:{postID} for recent posts.
type TopicProjector struct {
	db  *pgxpool.Pool
	rdb *redis.Client

	interval time.Duration
	window   time.Duration
}

// NewTopicProjector builds the projector. FEED_TOPIC_PROJECTION_INTERVAL
// overrides the refresh interval; an unparseable value is logged and
// ignored rather than failing boot, as with the other feed env overrides.
func NewTopicProjector(db *pgxpool.Pool, rdb *redis.Client) *TopicProjector {
	interval := defaultTopicInterval
	if raw := strings.TrimSpace(os.Getenv("FEED_TOPIC_PROJECTION_INTERVAL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			interval = d
		} else {
			log.Printf("ignoring invalid FEED_TOPIC_PROJECTION_INTERVAL=%q", raw)
		}
	}
	return &TopicProjector{db: db, rdb: rdb, interval: interval, window: topicWindow}
}

const (
	// defaultTopicInterval. A post's category and hashtags are set at
	// publish time and rarely edited, so this is about picking up NEW
	// posts, not about tracking changes to old ones. Ten minutes means a
	// freshly published video is topically rankable within one page-load
	// of most people noticing it exists.
	defaultTopicInterval = 10 * time.Minute

	// topicWindow bounds the projection to posts recent enough to still
	// appear as candidates. The ranker's recency factor has an
	// exponential decay of 0.05/hour, so a ninety-day-old post scores
	// effectively zero on recency and its topics cannot rescue it —
	// projecting the whole catalogue would be paying for keys nothing
	// will ever read.
	topicWindow = 90 * 24 * time.Hour

	// topicKeyTTL outlives several intervals, so a projector that stops
	// leaves the ranker degrading over a day rather than going topic-blind
	// at the next tick. Every pass rewrites what it finds.
	topicKeyTTL = 26 * time.Hour

	// maxTokensPerPost bounds one pathological post. A caption stuffed
	// with a hundred hashtags would otherwise both bloat the key and,
	// through TopicInterest's strongest-token rule, give itself a hundred
	// chances to match something the viewer likes.
	maxTokensPerPost = 12
)

// topicScanSQL reads the subject columns for recent, live posts that
// actually have a subject.
//
// The WHERE clause skipping topicless posts is the point of the whole
// query: the overwhelming majority of posts carry neither a category nor a
// tag, and writing an empty key for each of them would be a key per post
// in the catalogue in exchange for nothing. An absent key and an empty key
// mean the same thing to the reader, so the absent one is free.
//
// deleted_at IS NULL keeps the projection from resurrecting a removed
// post's topics into a viewer's affinity match. It is not a visibility
// gate and is not pretending to be one: this key holds no private
// information — a category and a hashtag list — and nothing is ever served
// from it. It only ever adjusts the ORDER of candidates that have already
// passed every visibility, block and mute filter.
const topicScanSQL = `
	SELECT id::text, category, hashtags, tags
	FROM posts
	WHERE deleted_at IS NULL
	  AND created_at >= $1
	  AND (category <> '' OR cardinality(hashtags) > 0 OR cardinality(tags) > 0)`

// Start runs one pass immediately, then on the interval, until ctx ends.
func (p *TopicProjector) Start(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	log.Printf("topic projector started (%s interval, %s window)", p.interval, p.window)

	if n, err := p.Run(ctx); err != nil {
		log.Printf("topic projection warmup failed: %v", err)
	} else {
		log.Printf("topic projector: warmed %d post(s)", n)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := p.Run(ctx); err != nil {
				log.Printf("topic projection failed: %v", err)
			} else if n > 0 {
				log.Printf("topic projector: refreshed %d post(s)", n)
			}
		}
	}
}

// Run performs one projection pass and returns how many posts it wrote.
func (p *TopicProjector) Run(ctx context.Context) (int, error) {
	rows, err := p.db.Query(ctx, topicScanSQL, time.Now().Add(-p.window))
	if err != nil {
		return 0, fmt.Errorf("scan post topics: %w", err)
	}
	defer rows.Close()

	pipe := p.rdb.Pipeline()
	written := 0
	for rows.Next() {
		var idStr, category string
		var hashtags, tags []string
		if err := rows.Scan(&idStr, &category, &hashtags, &tags); err != nil {
			log.Printf("topic projection scan error: %v", err)
			continue
		}
		postID, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		tokens := TopicTokens(category, hashtags, tags)
		if len(tokens) == 0 {
			continue
		}
		pipe.Set(ctx, ranking.PostTopicsKey(postID), strings.Join(tokens, ","), topicKeyTTL)
		written++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate post topics: %w", err)
	}
	if written == 0 {
		return 0, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("write post topics: %w", err)
	}
	return written, nil
}

// TopicTokens builds a post's canonical token list from its three subject
// columns. Exported because the related-videos path derives the seed
// video's tokens from the hydrated post rather than from Redis — the seed
// is one known post, so there is no reason to make it depend on the
// projection having run — and both must produce the same strings.
//
// Order is category first, then hashtags, then tags, deduplicated,
// lowercased and trimmed. Deterministic so an unchanged post produces a
// byte-identical value on every pass.
func TopicTokens(category string, hashtags, tags []string) []string {
	out := make([]string, 0, maxTokensPerPost)
	seen := make(map[string]struct{}, maxTokensPerPost)

	add := func(tok string) {
		if len(out) >= maxTokensPerPost {
			return
		}
		if _, dup := seen[tok]; dup {
			return
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}

	if c := normalizeToken(category); c != "" {
		add("cat:" + c)
	}
	for _, group := range [][]string{hashtags, tags} {
		for _, raw := range group {
			// A leading '#' is how a hashtag is written and not part of
			// its identity; post-service stores both spellings depending
			// on which composer field it came from.
			if t := normalizeToken(strings.TrimPrefix(strings.TrimSpace(raw), "#")); t != "" {
				add("tag:" + t)
			}
		}
	}
	return out
}

// normalizeToken lowercases, trims, and drops anything containing a comma.
// The comma is the projection's own separator: a tag containing one would
// split into two bogus tokens on read, so it is discarded rather than
// escaped — there is no legitimate hashtag with a comma in it, and a
// silently-mangled token is worse than a missing one.
func normalizeToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || strings.Contains(s, ",") {
		return ""
	}
	return s
}
