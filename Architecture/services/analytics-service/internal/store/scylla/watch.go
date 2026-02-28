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
