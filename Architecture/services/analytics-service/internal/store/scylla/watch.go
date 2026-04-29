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
		ws.ContentID, ws.SessionID, ws.ViewerID, ws.ContentType, ws.DurationMS,
		ws.WatchedMS, ws.PercentViewed, ws.LoopCount, ws.IsDisplayView,
		ws.MilestonesHit, ws.Surface, ws.Country, ws.DeviceHash, ws.IsAutoplay,
		ws.EndReason, ws.TrustFactor, ws.VQS, ws.CreatedAt, ws.UpdatedAt,
	).WithContext(ctx).Exec()
}

// GetWatchSession retrieves a watch session by content_id and session_id.
func (s *WatchStore) GetWatchSession(ctx context.Context, contentID uuid.UUID, sessionID string) (*WatchSession, error) {
	ws := &WatchSession{}
	err := s.session.Query(`
		SELECT content_id, session_id, viewer_id, content_type, duration_ms,
		       watched_ms, percent_viewed, loop_count, is_display_view,
		       milestones_hit, surface, country, device_hash, is_autoplay,
		       end_reason, trust_factor, vqs, created_at, updated_at
		FROM social_analytics.watch_sessions
		WHERE content_id = ? AND session_id = ?`,
		contentID, sessionID,
	).WithContext(ctx).Scan(
		&ws.ContentID, &ws.SessionID, &ws.ViewerID, &ws.ContentType, &ws.DurationMS,
		&ws.WatchedMS, &ws.PercentViewed, &ws.LoopCount, &ws.IsDisplayView,
		&ws.MilestonesHit, &ws.Surface, &ws.Country, &ws.DeviceHash, &ws.IsAutoplay,
		&ws.EndReason, &ws.TrustFactor, &ws.VQS, &ws.CreatedAt, &ws.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
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
		contentID, limit,
	).WithContext(ctx).Iter()

	var sessions []*WatchSession
	for {
		ws := &WatchSession{}
		if !iter.Scan(
			&ws.ContentID, &ws.SessionID, &ws.ViewerID, &ws.ContentType, &ws.DurationMS,
			&ws.WatchedMS, &ws.PercentViewed, &ws.LoopCount, &ws.IsDisplayView,
			&ws.MilestonesHit, &ws.Surface, &ws.Country, &ws.DeviceHash, &ws.IsAutoplay,
			&ws.EndReason, &ws.TrustFactor, &ws.VQS, &ws.CreatedAt, &ws.UpdatedAt,
		) {
			break
		}
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
		viewerID, contentID,
	).WithContext(ctx).Scan(&currentCount, &firstView)

	now := time.Now()
	if err != nil {
		// First view - insert new row
		return s.session.Query(`
			INSERT INTO social_analytics.viewer_history (viewer_id, content_id, first_view, view_count)
			VALUES (?, ?, ?, ?)`,
			viewerID, contentID, now, 1,
		).WithContext(ctx).Exec()
	}

	// Subsequent view - update count (preserve original first_view)
	return s.session.Query(`
		INSERT INTO social_analytics.viewer_history (viewer_id, content_id, first_view, view_count)
		VALUES (?, ?, ?, ?)`,
		viewerID, contentID, firstView, currentCount+1,
	).WithContext(ctx).Exec()
}

// GetViewerHistory checks if a viewer has previously watched this content.
func (s *WatchStore) GetViewerHistory(ctx context.Context, viewerID, contentID uuid.UUID) (int, error) {
	var count int
	err := s.session.Query(`
		SELECT view_count FROM social_analytics.viewer_history
		WHERE viewer_id = ? AND content_id = ?`,
		viewerID, contentID,
	).WithContext(ctx).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// RetentionPoint is one bucket of the audience-retention curve:
// the share of sessions still watching at SecondOffset seconds in.
type RetentionPoint struct {
	SecondOffset  int64   `json:"second_offset"`
	RetainedRatio float64 `json:"retained_ratio"`
	SessionCount  int64   `json:"session_count"`
}

// GetRetentionCurve walks every watch session for contentID and
// builds the audience-retention histogram. Returns one point per
// `bucketSec` seconds, capped at `maxBuckets` so the response stays
// bounded for long videos.
//
// Algorithm: histogram[ floor(watched_ms / 1000 / bucketSec) ]++,
// then cumulative-from-the-tail to get "still watching" at each
// bucket. Per-session, not per-viewer — re-watches lift the curve,
// matching the way YouTube/TikTok show retention.
func (s *WatchStore) GetRetentionCurve(ctx context.Context, contentID uuid.UUID, bucketSec int64, maxBuckets int) ([]RetentionPoint, error) {
	if bucketSec <= 0 {
		bucketSec = 1
	}
	if maxBuckets <= 0 || maxBuckets > 600 {
		maxBuckets = 600
	}

	iter := s.session.Query(
		`SELECT watched_ms FROM social_analytics.watch_sessions WHERE content_id = ?`,
		contentID,
	).WithContext(ctx).Iter()

	hist := make([]int64, maxBuckets+1)
	var total int64
	var watchedMS int64
	for iter.Scan(&watchedMS) {
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
	if err := iter.Close(); err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, nil
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
	return out, nil
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
// partition scan.
type AudienceDemographics struct {
	TotalSessions  int64               `json:"total_sessions"`
	UniqueViewers  int64               `json:"unique_viewers"`
	TopCountries   []DemographicBucket `json:"top_countries"`
	TopSurfaces    []DemographicBucket `json:"top_surfaces"`
}

// GetAudienceDemographics walks every watch_session for one
// content_id and returns the country + surface distributions.
// `topN` caps each dimension's slice (default 10, max 50). Sessions
// with empty country / surface are bucketed under "unknown" so the
// total adds up cleanly.
func (s *WatchStore) GetAudienceDemographics(ctx context.Context, contentID uuid.UUID, topN int) (*AudienceDemographics, error) {
	if topN <= 0 || topN > 50 {
		topN = 10
	}
	iter := s.session.Query(
		`SELECT viewer_id, country, surface FROM social_analytics.watch_sessions WHERE content_id = ?`,
		contentID,
	).WithContext(ctx).Iter()

	countries := map[string]int64{}
	surfaces := map[string]int64{}
	uniqueViewers := map[uuid.UUID]struct{}{}
	var total int64
	var viewerID uuid.UUID
	var country, surface string
	for iter.Scan(&viewerID, &country, &surface) {
		if country == "" {
			country = "unknown"
		}
		if surface == "" {
			surface = "unknown"
		}
		countries[country]++
		surfaces[surface]++
		uniqueViewers[viewerID] = struct{}{}
		total++
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}

	return &AudienceDemographics{
		TotalSessions: total,
		UniqueViewers: int64(len(uniqueViewers)),
		TopCountries:  topBuckets(countries, total, topN),
		TopSurfaces:   topBuckets(surfaces, total, topN),
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
		contentID,
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
