package scylla

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

type WatchSession struct {
	ContentID     uuid.UUID
	SessionID     string
	ViewerID      uuid.UUID
	ContentType   string
	DurationMS    int64
	WatchedMS     int64
	PercentViewed float64
	LoopCount     int
	IsDisplayView bool
	MilestonesHit []string
	Surface       string
	Country       string
	DeviceHash    string
	IsAutoplay    bool
	EndReason     string
	TrustFactor   float64
	VQS           float64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type WatchStore struct {
	session *gocql.Session
}

func NewWatchStore(session *gocql.Session) *WatchStore {
	return &WatchStore{session: session}
}

// UpsertWatchSession creates or updates a watch session.
func (s *WatchStore) UpsertWatchSession(ctx context.Context, ws *WatchSession) error {
	return s.session.Query(`
		INSERT INTO social_analytics.watch_sessions (
			content_id, session_id, viewer_id, content_type, duration_ms,
			watched_ms, percent_viewed, loop_count, is_display_view,
			milestones_hit, surface, country, device_hash, is_autoplay,
			end_reason, trust_factor, vqs, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cql(ws.ContentID), ws.SessionID, cql(ws.ViewerID), ws.ContentType, ws.DurationMS,
		ws.WatchedMS, ws.PercentViewed, ws.LoopCount, ws.IsDisplayView,
		ws.MilestonesHit, ws.Surface, ws.Country, ws.DeviceHash, ws.IsAutoplay,
		ws.EndReason, ws.TrustFactor, ws.VQS, ws.CreatedAt, ws.UpdatedAt,
	).WithContext(ctx).Exec()
}

// GetWatchSession retrieves a watch session by content_id and session_id.
func (s *WatchStore) GetWatchSession(ctx context.Context, contentID uuid.UUID, sessionID string) (*WatchSession, error) {
	ws := &WatchSession{}
	var rowContent, rowViewer gocql.UUID
	err := s.session.Query(`
		SELECT content_id, session_id, viewer_id, content_type, duration_ms,
		       watched_ms, percent_viewed, loop_count, is_display_view,
		       milestones_hit, surface, country, device_hash, is_autoplay,
		       end_reason, trust_factor, vqs, created_at, updated_at
		FROM social_analytics.watch_sessions
		WHERE content_id = ? AND session_id = ?`,
		cql(contentID), sessionID,
	).WithContext(ctx).Scan(
		&rowContent, &ws.SessionID, &rowViewer, &ws.ContentType, &ws.DurationMS,
		&ws.WatchedMS, &ws.PercentViewed, &ws.LoopCount, &ws.IsDisplayView,
		&ws.MilestonesHit, &ws.Surface, &ws.Country, &ws.DeviceHash, &ws.IsAutoplay,
		&ws.EndReason, &ws.TrustFactor, &ws.VQS, &ws.CreatedAt, &ws.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	ws.ContentID = gouuid(rowContent)
	ws.ViewerID = gouuid(rowViewer)
	return ws, nil
}

// GetContentSessions retrieves all watch sessions for a given content item.
func (s *WatchStore) GetContentSessions(ctx context.Context, contentID uuid.UUID, limit int) ([]*WatchSession, error) {
	iter := s.session.Query(`
		SELECT content_id, session_id, viewer_id, content_type, duration_ms,
		       watched_ms, percent_viewed, loop_count, is_display_view,
		       milestones_hit, surface, country, device_hash, is_autoplay,
		       end_reason, trust_factor, vqs, created_at, updated_at
		FROM social_analytics.watch_sessions
		WHERE content_id = ?
		LIMIT ?`,
		cql(contentID), limit,
	).WithContext(ctx).Iter()

	var sessions []*WatchSession
	for {
		ws := &WatchSession{}
		var rowContent, rowViewer gocql.UUID
		if !iter.Scan(
			&rowContent, &ws.SessionID, &rowViewer, &ws.ContentType, &ws.DurationMS,
			&ws.WatchedMS, &ws.PercentViewed, &ws.LoopCount, &ws.IsDisplayView,
			&ws.MilestonesHit, &ws.Surface, &ws.Country, &ws.DeviceHash, &ws.IsAutoplay,
			&ws.EndReason, &ws.TrustFactor, &ws.VQS, &ws.CreatedAt, &ws.UpdatedAt,
		) {
			break
		}
		ws.ContentID = gouuid(rowContent)
		ws.ViewerID = gouuid(rowViewer)
		sessions = append(sessions, ws)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return sessions, nil
}

// IncrementViewerHistory records that a viewer watched content (for unique/repeat viewer counting).
func (s *WatchStore) IncrementViewerHistory(ctx context.Context, viewerID, contentID uuid.UUID) error {
	// First try to read current count
	var currentCount int
	var firstView time.Time
	err := s.session.Query(`
		SELECT view_count, first_view FROM social_analytics.viewer_history
		WHERE viewer_id = ? AND content_id = ?`,
		cql(viewerID), cql(contentID),
	).WithContext(ctx).Scan(&currentCount, &firstView)

	now := time.Now()
	if err != nil {
		// First view - insert new row
		return s.session.Query(`
			INSERT INTO social_analytics.viewer_history (viewer_id, content_id, first_view, view_count)
			VALUES (?, ?, ?, ?)`,
			cql(viewerID), cql(contentID), now, 1,
		).WithContext(ctx).Exec()
	}

	// Subsequent view - update count (preserve original first_view)
	return s.session.Query(`
		INSERT INTO social_analytics.viewer_history (viewer_id, content_id, first_view, view_count)
		VALUES (?, ?, ?, ?)`,
		cql(viewerID), cql(contentID), firstView, currentCount+1,
	).WithContext(ctx).Exec()
}

// GetViewerHistory checks if a viewer has previously watched this content.
func (s *WatchStore) GetViewerHistory(ctx context.Context, viewerID, contentID uuid.UUID) (int, error) {
	var count int
	err := s.session.Query(`
		SELECT view_count FROM social_analytics.viewer_history
		WHERE viewer_id = ? AND content_id = ?`,
		cql(viewerID), cql(contentID),
	).WithContext(ctx).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// The retention and demographics charts read one content item's
// watch_sessions partition (audit M9). A viral video keeps up to 90 days
// of sessions there (the table TTL) — millions of rows — and the studio
// drawer used to read every one of them on every open. Each chart read
// is now bounded three ways:
//
//   - chartScanLimit rows at most. watch_sessions clusters on session_id,
//     a random UUID, so the first N rows of the partition are a uniform
//     sample of its sessions, not the oldest or newest N. Shares and
//     retention ratios from the sample are unbiased estimates; counts are
//     the sample's (see Sampled). Below the limit nothing changes.
//   - chartQueryTimeout for the whole scan, so a slow replica cannot hold
//     a request (and a pod connection) open indefinitely.
//   - chartPageSize rows per page, so the driver never buffers a whole
//     large partition at once.
//
// The HTTP layer caches each result for a few minutes on top of this
// (internal/http chartCache), so a creator refreshing the drawer does not
// rescan. A time window is not used: watch_sessions has no time key, and
// filtering on created_at would read the whole partition anyway.
const (
	chartScanLimit    = 50_000
	chartPageSize     = 5_000
	chartQueryTimeout = 5 * time.Second
)

// rowScanner is the slice of *gocql.Iter the chart accumulators need, so
// they are testable without a cluster.
type rowScanner interface {
	Scan(dest ...interface{}) bool
	Close() error
}

// RetentionPoint is one bucket of the audience-retention curve:
// the share of sessions still watching at SecondOffset seconds in.
type RetentionPoint struct {
	SecondOffset  int64   `json:"second_offset"`
	RetainedRatio float64 `json:"retained_ratio"`
	SessionCount  int64   `json:"session_count"`
}

// GetRetentionCurve builds the audience-retention histogram from at most
// chartScanLimit of the content's watch sessions. Returns one point per
// `bucketSec` seconds, capped at `maxBuckets` so the response stays
// bounded for long videos; sampled is true when the partition held more
// sessions than were read.
//
// Algorithm: histogram[ floor(watched_ms / 1000 / bucketSec) ]++,
// then cumulative-from-the-tail to get "still watching" at each
// bucket. Per-session, not per-viewer — re-watches lift the curve,
// matching the way YouTube/TikTok show retention.
func (s *WatchStore) GetRetentionCurve(ctx context.Context, contentID uuid.UUID, bucketSec int64, maxBuckets int) ([]RetentionPoint, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, chartQueryTimeout)
	defer cancel()
	iter := s.session.Query(
		`SELECT watched_ms FROM social_analytics.watch_sessions WHERE content_id = ? LIMIT ?`,
		cql(contentID), chartScanLimit+1,
	).WithContext(ctx).PageSize(chartPageSize).Iter()
	return retentionFromRows(iter, bucketSec, maxBuckets, chartScanLimit)
}

// retentionFromRows reads up to limit watched_ms values (one more is
// read only to learn that there were more) and builds the curve.
func retentionFromRows(rows rowScanner, bucketSec int64, maxBuckets, limit int) ([]RetentionPoint, bool, error) {
	if bucketSec <= 0 {
		bucketSec = 1
	}
	if maxBuckets <= 0 || maxBuckets > 600 {
		maxBuckets = 600
	}

	hist := make([]int64, maxBuckets+1)
	var total int64
	var watchedMS int64
	sampled := false
	for rows.Scan(&watchedMS) {
		if total >= int64(limit) { // retention
			sampled = true // retention
			break
		}
		if watchedMS < 0 {
			watchedMS = 0
		}
		bucket := watchedMS / 1000 / bucketSec
		if bucket > int64(maxBuckets) {
			bucket = int64(maxBuckets)
		}
		hist[bucket]++
		total++
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	if total == 0 {
		return nil, false, nil
	}

	// Cumulative-from-tail: sessions with watched_ms in bucket b
	// stopped at second b*bucketSec. Anyone in bucket >= t was still
	// watching at t. So retention[t] = sum(hist[t..]) / total.
	out := make([]RetentionPoint, 0, maxBuckets+1)
	retained := total
	for b := 0; b <= maxBuckets; b++ {
		ratio := float64(retained) / float64(total)
		out = append(out, RetentionPoint{
			SecondOffset:  int64(b) * bucketSec,
			RetainedRatio: ratio,
			SessionCount:  retained,
		})
		retained -= hist[b]
		if retained <= 0 {
			break
		}
	}
	return out, sampled, nil
}

// DemographicBucket is one slice of an audience-distribution chart:
// "what fraction of viewers came from <key>". Key is "country" or
// "surface" depending on which dimension the caller asked for.
type DemographicBucket struct {
	Key   string  `json:"key"`
	Count int64   `json:"count"`
	Share float64 `json:"share"`
}

// AudienceDemographics is what the studio's "Who's watching"
// drawer renders: a top-N breakdown by country and by surface
// (Home / Profile / Posttube etc.) for one piece of content. Both
// dimensions live on watch_sessions, so the whole rollup is one
// bounded partition read.
//
// Sampled is true when the content has more than chartScanLimit sessions
// and the figures come from a uniform sample of them: the shares are
// estimates, and TotalSessions / UniqueViewers / Count are the sample's,
// i.e. lower bounds. Omitted (false) otherwise, so the response for an
// ordinary video is unchanged.
type AudienceDemographics struct {
	TotalSessions int64               `json:"total_sessions"`
	UniqueViewers int64               `json:"unique_viewers"`
	TopCountries  []DemographicBucket `json:"top_countries"`
	TopSurfaces   []DemographicBucket `json:"top_surfaces"`
	Sampled       bool                `json:"sampled,omitempty"`
}

// GetAudienceDemographics returns the country + surface distributions of
// at most chartScanLimit of the content's watch sessions. `topN` caps
// each dimension's slice (default 10, max 50). Sessions with empty
// country / surface are bucketed under "unknown" so the total adds up
// cleanly.
func (s *WatchStore) GetAudienceDemographics(ctx context.Context, contentID uuid.UUID, topN int) (*AudienceDemographics, error) {
	ctx, cancel := context.WithTimeout(ctx, chartQueryTimeout)
	defer cancel()
	iter := s.session.Query(
		`SELECT viewer_id, country, surface FROM social_analytics.watch_sessions WHERE content_id = ? LIMIT ?`,
		cql(contentID), chartScanLimit+1,
	).WithContext(ctx).PageSize(chartPageSize).Iter()
	return demographicsFromRows(iter, topN, chartScanLimit)
}

// demographicsFromRows reads up to limit (viewer, country, surface) rows
// (one more only to learn that there were more) and builds the breakdown.
func demographicsFromRows(rows rowScanner, topN, limit int) (*AudienceDemographics, error) {
	if topN <= 0 || topN > 50 {
		topN = 10
	}
	countries := map[string]int64{}
	surfaces := map[string]int64{}
	uniqueViewers := map[uuid.UUID]struct{}{}
	var total int64
	var viewerID gocql.UUID
	var country, surface string
	sampled := false
	for rows.Scan(&viewerID, &country, &surface) {
		if total >= int64(limit) { // demographics
			sampled = true // demographics
			break
		}
		if country == "" {
			country = "unknown"
		}
		if surface == "" {
			surface = "unknown"
		}
		countries[country]++
		surfaces[surface]++
		uniqueViewers[gouuid(viewerID)] = struct{}{}
		total++
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	return &AudienceDemographics{
		TotalSessions: total,
		UniqueViewers: int64(len(uniqueViewers)),
		TopCountries:  topBuckets(countries, total, topN),
		TopSurfaces:   topBuckets(surfaces, total, topN),
		Sampled:       sampled,
	}, nil
}

// topBuckets sorts a count map descending, takes the top N, and
// computes share = count / total. Tail keys roll into "other" so the
// shares always sum to 1.0.
func topBuckets(counts map[string]int64, total int64, topN int) []DemographicBucket {
	if total == 0 {
		return []DemographicBucket{}
	}
	type kv struct {
		k string
		v int64
	}
	rows := make([]kv, 0, len(counts))
	for k, v := range counts {
		rows = append(rows, kv{k, v})
	}
	// Manual descending sort by count.
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[j].v > rows[i].v {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}

	out := make([]DemographicBucket, 0, topN+1)
	var taken int64
	for i, r := range rows {
		if i >= topN {
			break
		}
		out = append(out, DemographicBucket{
			Key:   r.k,
			Count: r.v,
			Share: float64(r.v) / float64(total),
		})
		taken += r.v
	}
	if remainder := total - taken; remainder > 0 {
		out = append(out, DemographicBucket{
			Key:   "other",
			Count: remainder,
			Share: float64(remainder) / float64(total),
		})
	}
	return out
}

// CountDisplayViews counts sessions where is_display_view = true for a content item.
func (s *WatchStore) CountDisplayViews(ctx context.Context, contentID uuid.UUID) (int64, error) {
	var count int64
	// Note: This requires ALLOW FILTERING since is_display_view is not in the primary key.
	// For production, this should be done via the hourly aggregation pipeline instead.
	iter := s.session.Query(`
		SELECT is_display_view FROM social_analytics.watch_sessions
		WHERE content_id = ?`,
		cql(contentID),
	).WithContext(ctx).Iter()

	var isView bool
	for iter.Scan(&isView) {
		if isView {
			count++
		}
	}
	if err := iter.Close(); err != nil {
		return 0, err
	}
	return count, nil
}
