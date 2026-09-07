package personalization

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atpost/analytics-service/internal/scoring"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// The personalization warmer.
//
// WHAT WAS BROKEN
//
// feed-service's ranker (internal/ranking) has always read five Redis keys
// per viewer and scored against them. Four of the five were written by
// nothing at all:
//
//	user:affinities:{viewer}      — how much this viewer likes this author
//	user:media_prefs:{viewer}     — the viewer's video dwell percentile
//	post:cqs:{post}               — the content quality score
//	user:mutual_follows:{viewer}  — mutual-follow proximity
//
// So `interest_score` was the 0.3 cold-start constant for every author,
// `media_boost` was the no-data default for every video, `quality_boost`
// was zero for every post, and the feed was ordered by recency and
// velocity. The ranker was not wrong; it was blind.
//
// post:cqs is the most galling of the four, because analytics-service has
// been COMPUTING it correctly all along. aggregation.DailyRollup writes it
// to Redis — but only from the 00:xx UTC tick, only for content that had a
// row in YESTERDAY's daily summary, and with a one-hour TTL. The key
// therefore holds a value for at most one hour in twenty-four, and holds
// nothing at all in any deployment that has not been up across a midnight.
// That is why the live count is zero.
//
// WHY THIS LIVES IN ANALYTICS-SERVICE
//
// Three of the four signals are functions of watch history, and watch
// history is analytics.events_raw — a table analytics-service owns and no
// other service reads. Computing affinity anywhere else would mean either
// copying the event stream or opening another service's schema, and
// computing CQS anywhere else would mean a second definition of quality
// living apart from scoring.ComputeCQS, free to drift from the one the
// creator fund pays against. The service that owns the events and the
// formula should be the service that publishes the conclusion.
//
// The fourth signal — mutual follows — is NOT a function of watch history.
// It is the social graph, which analytics-service has no access to and no
// business reading. It is warmed by feed-service, which already holds a
// graph-service client, and lives in feed-service/internal/service/mutuals.go.
//
// WHY A PERIODIC JOB AND NOT AN EVENT CONSUMER
//
// The tempting alternative is to update affinity incrementally from the
// Kafka event stream. It was rejected: the model is time-decayed, so every
// score changes continuously whether or not new events arrive, and an
// incremental writer would have to either re-decay the whole hash on every
// event or let scores drift upward forever. Recomputing from a bounded
// window is idempotent, self-healing after a Redis flush or an outage, and
// cheap — it is four grouped scans over an indexed window, not a row per
// event.

const (
	// AffinityKeyPrefix etc. mirror the exact key shapes
	// feed-service/internal/ranking/signals.go reads. They are a contract
	// between two services; changing one without the other silently
	// returns the feed to its blind state, which is why they are named
	// constants with this comment rather than inline format strings.
	AffinityKeyFmt      = "user:affinities:%s"
	MediaPrefsKeyFmt    = "user:media_prefs:%s"
	TopicAffinityKeyFmt = "user:topic_affinity:%s"
	CompletionsKeyFmt   = "user:completions:%s"
	CQSKeyFmt           = "post:cqs:%s"
)

// signalTTL outlives several run intervals on purpose: a warmer that dies
// leaves a feed that degrades over a day, not one that goes blind at the
// next tick. Every run rewrites the keys it produces, so a live warmer
// keeps them permanently fresh.
const signalTTL = 26 * time.Hour

// defaultInterval. The hourly aggregator runs every five minutes for
// dashboard freshness; personalization does not need to be that fresh —
// the model has a fourteen-day half-life, so a fifteen-minute lag is
// invisible — and each run is a wider scan.
const defaultInterval = 15 * time.Minute

// maxAuthorsPerViewer caps the affinity hash. Beyond a couple of hundred
// authors the tail is noise the ranker will never consult: it only ever
// asks about the authors on the page in front of the viewer, and a page is
// tens of authors drawn from the people they follow.
const maxAuthorsPerViewer = 200

// maxTopicsPerViewer caps the topic hash for the same reason. The taxonomy
// is 18 categories; the rest of the space is hashtags, whose tail is long
// and almost entirely single-use.
const maxTopicsPerViewer = 100

// maxCompletionsPerViewer caps the "already finished this" set. It exists
// to stop the related-videos endpoint offering you what you just watched,
// and a viewer's recent completions are what matter for that.
const maxCompletionsPerViewer = 2000

// Warmer recomputes the viewer-signal Redis keys the feed ranker consumes.
type Warmer struct {
	pg  *pgxpool.Pool
	rdb *redis.Client
	log *slog.Logger

	interval time.Duration

	// mu serialises runs so the on-demand trigger cannot overlap the
	// ticker. Each run fully rewrites its keys, so an overlap would not
	// corrupt anything — it would just double the database load for no
	// gain, and make the "run it and look at Redis" demonstration
	// non-deterministic.
	mu sync.Mutex

	// lastRun / lastStats are for the internal trigger's response.
	statsMu   sync.RWMutex
	lastRun   time.Time
	lastStats Stats
}

// Stats is what one pass wrote. Returned by the internal trigger so the
// run can be verified without reading the logs.
type Stats struct {
	CQSKeys        int           `json:"cqs_keys"`
	AffinityHashes int           `json:"affinity_hashes"`
	AffinityFields int           `json:"affinity_fields"`
	MediaPrefKeys  int           `json:"media_pref_keys"`
	TopicHashes    int           `json:"topic_hashes"`
	TopicFields    int           `json:"topic_fields"`
	CompletionSets int           `json:"completion_sets"`
	Completions    int           `json:"completions"`
	Duration       time.Duration `json:"-"`
	DurationMS     int64         `json:"duration_ms"`
}

// NewWarmer builds the warmer. The interval may be overridden with
// ANALYTICS_PERSONALIZATION_INTERVAL (a Go duration); an unparseable or
// non-positive value is ignored with a log line rather than failing boot,
// matching how feed-service treats its own env overrides.
func NewWarmer(pg *pgxpool.Pool, rdb *redis.Client) *Warmer {
	interval := defaultInterval
	if raw := strings.TrimSpace(os.Getenv("ANALYTICS_PERSONALIZATION_INTERVAL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			interval = d
		} else {
			slog.Warn("ignoring invalid ANALYTICS_PERSONALIZATION_INTERVAL", "value", raw)
		}
	}
	return &Warmer{pg: pg, rdb: rdb, log: slog.Default(), interval: interval}
}

// Start runs one pass immediately, then on the interval, until ctx ends.
func (w *Warmer) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	w.log.Info("personalization warmer started", "interval", w.interval.String())

	if _, err := w.Run(ctx); err != nil {
		w.log.Warn("personalization warmup failed", "error", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.Run(ctx); err != nil {
				w.log.Warn("personalization pass failed", "error", err)
			}
		}
	}
}

// LastRun reports when a pass last completed and what it wrote.
func (w *Warmer) LastRun() (time.Time, Stats) {
	w.statsMu.RLock()
	defer w.statsMu.RUnlock()
	return w.lastRun, w.lastStats
}

// Run executes one full pass.
//
// Failure policy: each of the five projections is independent, so one
// failing scan must not cost the other four. Errors are collected and
// returned together after every projection has had its turn — a viewer
// whose media preferences could not be computed still gets their author
// affinities refreshed.
func (w *Warmer) Run(ctx context.Context) (Stats, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	started := time.Now()
	var stats Stats
	var errs []string

	since := started.Add(-AffinityWindowDays * 24 * time.Hour)

	if n, err := w.warmCQS(ctx); err != nil {
		errs = append(errs, "cqs: "+err.Error())
	} else {
		stats.CQSKeys = n
	}
	if hashes, fields, err := w.warmAuthorAffinities(ctx, since); err != nil {
		errs = append(errs, "affinities: "+err.Error())
	} else {
		stats.AffinityHashes, stats.AffinityFields = hashes, fields
	}
	if n, err := w.warmMediaPrefs(ctx, since); err != nil {
		errs = append(errs, "media_prefs: "+err.Error())
	} else {
		stats.MediaPrefKeys = n
	}
	if hashes, fields, err := w.warmTopicAffinities(ctx, since); err != nil {
		errs = append(errs, "topic_affinity: "+err.Error())
	} else {
		stats.TopicHashes, stats.TopicFields = hashes, fields
	}
	if sets, members, err := w.warmCompletions(ctx, since); err != nil {
		errs = append(errs, "completions: "+err.Error())
	} else {
		stats.CompletionSets, stats.Completions = sets, members
	}

	stats.Duration = time.Since(started)
	stats.DurationMS = stats.Duration.Milliseconds()

	w.statsMu.Lock()
	w.lastRun, w.lastStats = time.Now(), stats
	w.statsMu.Unlock()

	w.log.Info("personalization pass complete",
		"cqs", stats.CQSKeys,
		"affinity_viewers", stats.AffinityHashes, "affinity_fields", stats.AffinityFields,
		"media_pref_viewers", stats.MediaPrefKeys,
		"topic_viewers", stats.TopicHashes, "topic_fields", stats.TopicFields,
		"completion_viewers", stats.CompletionSets,
		"duration", stats.Duration.String())

	if len(errs) > 0 {
		return stats, fmt.Errorf("personalization pass had %d failure(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return stats, nil
}

// ---------------------------------------------------------------- CQS ----

// cqsSQL rebuilds each content item's LIFETIME quality score from the
// hourly aggregates.
//
// Lifetime, not last-hour: CQS divides engagement by impressions, so a
// single hour with three impressions produces a wild number, and the
// ranker would then see a good video's score swing hour to hour on
// nothing. Summing every bucket is the same arithmetic ComputeCQS does on
// one hour, applied to the whole life of the item, and is stable.
//
// avg_percent_viewed is itself a per-hour average, so it is re-averaged
// weighted by that hour's display views rather than naively — an hour with
// two views must not count as much as an hour with two thousand.
const cqsSQL = `
	SELECT
		content_id::text,
		COALESCE(SUM(impressions), 0),
		COALESCE(SUM(likes), 0),
		COALESCE(SUM(comments), 0),
		COALESCE(SUM(shares), 0),
		COALESCE(SUM(saves), 0),
		COALESCE(SUM(follows_from_content), 0),
		COALESCE(SUM(reports), 0),
		COALESCE(SUM(not_interested), 0),
		CASE WHEN SUM(views_display) > 0
			THEN SUM(avg_percent_viewed * views_display) / SUM(views_display)
			ELSE 0 END
	FROM analytics.content_hourly_agg
	GROUP BY content_id
	HAVING SUM(impressions) > 0`

func (w *Warmer) warmCQS(ctx context.Context) (int, error) {
	rows, err := w.pg.Query(ctx, cqsSQL)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	pipe := w.rdb.Pipeline()
	written := 0
	for rows.Next() {
		var contentID string
		var m scoring.AggregateMetrics
		if err := rows.Scan(&contentID, &m.Impressions, &m.Likes, &m.Comments, &m.Shares,
			&m.Saves, &m.FollowsFromContent, &m.Reports, &m.NotInterested, &m.AvgPercentViewed); err != nil {
			w.log.Warn("cqs scan error", "error", err)
			continue
		}
		cqs := scoring.ComputeCQS(&m)
		pipe.Set(ctx, fmt.Sprintf(CQSKeyFmt, contentID), strconv.FormatFloat(cqs, 'f', 6, 64), signalTTL)
		written++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if written == 0 {
		return 0, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return written, nil
}

// ----------------------------------------------------------- affinity ----

// affinitySQL groups the event window by (viewer, author, event type) and
// returns two decayed totals per group: an occurrence count in which older
// events count less, and — for play_end — the sum of the watched fraction.
//
// The decay is applied per event INSIDE the aggregate, not to the group
// total, so an author watched once yesterday scores above one watched once
// six weeks ago even though both have a count of one. That is the whole
// point of decaying at all.
//
// The weights are deliberately NOT here. They live in weights.go, in Go,
// where they are documented and unit-testable without a database; this
// query's job is to reduce a large table to a small number of rows.
//
// creator_id is left as text rather than cast to uuid: a single malformed
// value in a JSON payload would abort the whole scan on a cast, and the
// caller drops unparseable ids one at a time instead.
const affinitySQL = `
	SELECT
		e.user_id::text                                          AS viewer_id,
		e.payload->>'creator_id'                                 AS author_id,
		e.type,
		SUM(exp(-ln(2) * EXTRACT(EPOCH FROM (now() - e.ts)) / $2))                       AS decayed_count,
		SUM(exp(-ln(2) * EXTRACT(EPOCH FROM (now() - e.ts)) / $2)
			* LEAST(GREATEST(COALESCE((e.payload->>'percent_viewed')::double precision, 0), 0), 100) / 100.0
		)                                                                                 AS decayed_watch
	FROM analytics.events_raw e
	WHERE e.ts >= $1
	  AND e.user_id IS NOT NULL
	  AND e.payload->>'creator_id' IS NOT NULL
	  AND e.payload->>'creator_id' <> e.user_id::text
	  AND e.type IN ('impression','play_end','like','comment_create','share','save',
	                 'follow_from_content','not_interested','report','block_creator')
	GROUP BY 1, 2, 3`

func (w *Warmer) warmAuthorAffinities(ctx context.Context, since time.Time) (int, int, error) {
	// The viewer's own content is excluded in SQL above: a creator
	// re-watching their own upload must not teach the ranker to show
	// them more of themselves, and "never recommend a viewer their own
	// content" is easier to keep if the signal never learns it either.
	byViewer, err := w.scanBundles(ctx, affinitySQL, since)
	if err != nil {
		return 0, 0, err
	}

	pipe := w.rdb.Pipeline()
	hashes, fields := 0, 0
	for viewerID, subjects := range byViewer {
		scores := make(map[string]float64, len(subjects))
		for authorID, bundles := range subjects {
			if _, err := uuid.Parse(authorID); err != nil {
				continue // a payload with a malformed creator_id teaches nothing
			}
			scores[authorID] = SquashAffinity(RawScore(bundles))
		}
		kept := topN(scores, maxAuthorsPerViewer, ColdStartInterest)
		if len(kept) == 0 {
			continue
		}
		key := fmt.Sprintf(AffinityKeyFmt, viewerID)
		// DEL then HSET, not HSET alone: an author the viewer has since
		// stopped watching must fall out of the hash entirely rather than
		// keep the score they had when they were last seen. Without the
		// delete the hash only ever grows and never forgets.
		pipe.Del(ctx, key)
		pipe.HSet(ctx, key, kept)
		pipe.Expire(ctx, key, signalTTL)
		hashes++
		fields += len(kept) / 2
	}
	if hashes == 0 {
		return 0, 0, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, 0, err
	}
	return hashes, fields, nil
}

// -------------------------------------------------------------- topics ----

// topicSQL is the same fold as affinitySQL with the subject changed from
// "the author" to "a topic the post carries", so the viewer's taste can
// answer "more videos like this one" for a creator they have never seen.
//
// A topic token is `cat:<category>` or `tag:<hashtag or tag>`, lowercased.
// The join is to public.posts, the catalogue: analytics-service already
// shares the `app` database with post-service and feed-service, and this
// is a read of columns every service reads.
//
// LIMIT 12 inside the lateral bounds a single over-tagged post: without it
// a post with a hundred hashtags would contribute a hundred rows per event
// and dominate the scan.
const topicSQL = `
	SELECT
		e.user_id::text  AS viewer_id,
		t.token,
		e.type,
		SUM(exp(-ln(2) * EXTRACT(EPOCH FROM (now() - e.ts)) / $2))                       AS decayed_count,
		SUM(exp(-ln(2) * EXTRACT(EPOCH FROM (now() - e.ts)) / $2)
			* LEAST(GREATEST(COALESCE((e.payload->>'percent_viewed')::double precision, 0), 0), 100) / 100.0
		)                                                                                 AS decayed_watch
	FROM analytics.events_raw e
	JOIN public.posts p
	  ON p.id::text = e.payload->>'content_id'
	CROSS JOIN LATERAL (
		SELECT token FROM (
			SELECT 'cat:' || lower(p.category) AS token WHERE p.category <> ''
			UNION ALL
			SELECT 'tag:' || lower(x) FROM unnest(p.hashtags || p.tags) AS x WHERE x <> ''
		) tk
		LIMIT 12
	) t
	WHERE e.ts >= $1
	  AND e.user_id IS NOT NULL
	  AND e.payload->>'content_id' IS NOT NULL
	  AND p.deleted_at IS NULL
	  AND p.author_id <> e.user_id
	  AND e.type IN ('impression','play_end','like','comment_create','share','save',
	                 'follow_from_content','not_interested','report','block_creator')
	GROUP BY 1, 2, 3`

func (w *Warmer) warmTopicAffinities(ctx context.Context, since time.Time) (int, int, error) {
	byViewer, err := w.scanBundles(ctx, topicSQL, since)
	if err != nil {
		return 0, 0, err
	}

	pipe := w.rdb.Pipeline()
	hashes, fields := 0, 0
	for viewerID, subjects := range byViewer {
		scores := make(map[string]float64, len(subjects))
		for token, bundles := range subjects {
			if token == "" {
				continue
			}
			scores[token] = SquashAffinity(RawScore(bundles))
		}
		kept := topN(scores, maxTopicsPerViewer, ColdStartInterest)
		if len(kept) == 0 {
			continue
		}
		key := fmt.Sprintf(TopicAffinityKeyFmt, viewerID)
		pipe.Del(ctx, key)
		pipe.HSet(ctx, key, kept)
		pipe.Expire(ctx, key, signalTTL)
		hashes++
		fields += len(kept) / 2
	}
	if hashes == 0 {
		return 0, 0, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, 0, err
	}
	return hashes, fields, nil
}

// --------------------------------------------------------- media prefs ----

// mediaPrefsSQL is the viewer's 95th-percentile dwell on LONG-FORM video,
// in seconds.
//
// Seconds because that is what reads it: ranking/scorer.go compares
// VideoP95Dwell against 60 and 30 to pick a media boost of 1.5 / 1.3 / 1.1,
// and those thresholds are only sane as seconds.
//
// Long-form only, because that is the branch it feeds. Reels are capped
// short by construction, so mixing them in would drag every viewer's
// percentile below the 30-second threshold and the boost would never fire
// for anyone. percentile_disc rather than percentile_cont: it returns a
// dwell that actually happened rather than an interpolation between two.
const mediaPrefsSQL = `
	SELECT
		e.user_id::text,
		percentile_disc(0.95) WITHIN GROUP (
			ORDER BY COALESCE((e.payload->>'watched_ms_total')::bigint, 0) / 1000.0
		) AS video_p95_dwell_seconds,
		COUNT(*) AS playbacks
	FROM analytics.events_raw e
	WHERE e.ts >= $1
	  AND e.type = 'play_end'
	  AND e.user_id IS NOT NULL
	  AND COALESCE(e.payload->>'content_type', '') IN ('long_video', 'video')
	GROUP BY 1`

// minPlaybacksForMediaPref: a percentile over two data points is not a
// percentile, it is the larger of two numbers. Below this the key is left
// unwritten and the ranker keeps its no-data default, which is the honest
// answer for a viewer we have barely seen.
const minPlaybacksForMediaPref = 3

func (w *Warmer) warmMediaPrefs(ctx context.Context, since time.Time) (int, error) {
	rows, err := w.pg.Query(ctx, mediaPrefsSQL, since)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	pipe := w.rdb.Pipeline()
	written := 0
	for rows.Next() {
		var viewerID string
		var p95 float64
		var playbacks int64
		if err := rows.Scan(&viewerID, &p95, &playbacks); err != nil {
			w.log.Warn("media prefs scan error", "error", err)
			continue
		}
		if playbacks < minPlaybacksForMediaPref || p95 <= 0 {
			continue
		}
		key := fmt.Sprintf(MediaPrefsKeyFmt, viewerID)
		// Only video_p95_dwell is written. image_p95_dwell and
		// text_p95_dwell are fields on the ranker's MediaPrefs struct
		// with no event behind them: nothing in the thirteen ingested
		// event types measures how long an image or a caption held
		// someone's eye. Writing a fabricated number would be worse than
		// leaving them absent, and absent already means zero to the
		// loader, which is what the ranker's default branch expects.
		pipe.HSet(ctx, key, "video_p95_dwell", strconv.FormatFloat(p95, 'f', 2, 64))
		pipe.Expire(ctx, key, signalTTL)
		written++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if written == 0 {
		return 0, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return written, nil
}

// --------------------------------------------------------- completions ----

// completionsSQL is every content item a viewer has watched essentially to
// the end. 95 % is the same threshold the hourly aggregator uses for its
// completion_rate, so "completed" means one thing across the service.
//
// Its consumer is the guard rail: the related-videos endpoint must never
// offer someone the video they just finished, and the ranker folds it into
// the already-interacted penalty on the main feeds.
const completionsSQL = `
	SELECT e.user_id::text, e.payload->>'content_id', MAX(e.ts)
	FROM analytics.events_raw e
	WHERE e.ts >= $1
	  AND e.type = 'play_end'
	  AND e.user_id IS NOT NULL
	  AND e.payload->>'content_id' IS NOT NULL
	  AND COALESCE((e.payload->>'percent_viewed')::double precision, 0) >= 95
	GROUP BY 1, 2
	ORDER BY 1, 3 DESC`

func (w *Warmer) warmCompletions(ctx context.Context, since time.Time) (int, int, error) {
	rows, err := w.pg.Query(ctx, completionsSQL, since)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	byViewer := make(map[string][]string)
	for rows.Next() {
		var viewerID, contentID string
		var last time.Time
		if err := rows.Scan(&viewerID, &contentID, &last); err != nil {
			w.log.Warn("completions scan error", "error", err)
			continue
		}
		if _, err := uuid.Parse(contentID); err != nil {
			continue
		}
		// Rows arrive newest-first per viewer, so truncating at the cap
		// keeps the most recent completions — the ones that matter for
		// "don't show me what I just watched".
		if len(byViewer[viewerID]) >= maxCompletionsPerViewer {
			continue
		}
		byViewer[viewerID] = append(byViewer[viewerID], contentID)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	pipe := w.rdb.Pipeline()
	sets, members := 0, 0
	for viewerID, ids := range byViewer {
		key := fmt.Sprintf(CompletionsKeyFmt, viewerID)
		pipe.Del(ctx, key)
		pipe.SAdd(ctx, key, toAny(ids)...)
		pipe.Expire(ctx, key, signalTTL)
		sets++
		members += len(ids)
	}
	if sets == 0 {
		return 0, 0, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, 0, err
	}
	return sets, members, nil
}

// ------------------------------------------------------------- helpers ----

// scanBundles runs one of the two grouped scans and folds it into
// viewer → subject → per-type decayed totals. Both queries return the same
// five columns in the same order, which is why they share this.
func (w *Warmer) scanBundles(ctx context.Context, query string, since time.Time) (map[string]map[string][]TypeBundle, error) {
	halfLifeSeconds := AffinityHalfLifeDays * 24 * 3600
	rows, err := w.pg.Query(ctx, query, since, halfLifeSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]map[string][]TypeBundle)
	for rows.Next() {
		var viewerID, subject, evType string
		var decayedCount, decayedWatch float64
		if err := rows.Scan(&viewerID, &subject, &evType, &decayedCount, &decayedWatch); err != nil {
			w.log.Warn("bundle scan error", "error", err)
			continue
		}
		if out[viewerID] == nil {
			out[viewerID] = make(map[string][]TypeBundle)
		}
		out[viewerID][subject] = append(out[viewerID][subject], TypeBundle{
			Type:                 evType,
			DecayedCount:         decayedCount,
			DecayedWatchFraction: decayedWatch,
		})
	}
	return out, rows.Err()
}

// topN reduces a score map to the `limit` strongest-signal entries and
// flattens it into the alternating field/value slice go-redis HSET wants.
//
// "Strongest signal" is distance from neutral, not highest score: an
// author the viewer has actively rejected is at least as important to keep
// as one they like, and sorting by value alone would drop every negative
// first. Entries that landed exactly on neutral carry no information and
// are dropped outright — writing them would mean the ranker reading a value
// identical to the default it already uses.
func topN(scores map[string]float64, limit int, neutral float64) []any {
	type kv struct {
		key   string
		value float64
	}
	ranked := make([]kv, 0, len(scores))
	for k, v := range scores {
		if math.Abs(v-neutral) < 1e-9 {
			continue
		}
		ranked = append(ranked, kv{k, v})
	}
	// A full sort rather than a partial selection: these slices are small
	// (bounded by how many authors or topics one person can engage with
	// in sixty days) and the tie-break makes two runs over unchanged data
	// write byte-identical hashes, which is worth more here than the
	// asymptotics.
	sort.Slice(ranked, func(i, j int) bool {
		di := math.Abs(ranked[i].value - neutral)
		dj := math.Abs(ranked[j].value - neutral)
		if di != dj {
			return di > dj
		}
		return ranked[i].key < ranked[j].key
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]any, 0, len(ranked)*2)
	for _, e := range ranked {
		out = append(out, e.key, strconv.FormatFloat(e.value, 'f', 6, 64))
	}
	return out
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
