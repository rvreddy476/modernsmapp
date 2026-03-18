package scylla

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// EngagementStore provides ScyllaDB operations for reel views, view counts, and content engagement.
type EngagementStore struct {
	session *gocql.Session
}

// NewEngagementStore creates a new EngagementStore.
func NewEngagementStore(session *gocql.Session) *EngagementStore {
	return &EngagementStore{session: session}
}

// ─── Reel Views ─────────────────────────────────────────────────────

// ReelView represents a single view event for a reel/video.
type ReelView struct {
	ContentID      uuid.UUID `json:"content_id"`
	ViewerID       uuid.UUID `json:"viewer_id"`
	ViewedAt       time.Time `json:"viewed_at"`
	WatchDurationMs int      `json:"watch_duration_ms"`
	CompletionPct  float32   `json:"completion_pct"`
	Source         string    `json:"source"`
}

// RecordReelView inserts a new view event into the reel_views table.
func (s *EngagementStore) RecordReelView(ctx context.Context, v *ReelView) error {
	if v.ViewedAt.IsZero() {
		v.ViewedAt = time.Now()
	}
	return s.session.Query(`
		INSERT INTO social_analytics.reel_views (content_id, viewed_at, viewer_id, watch_duration_ms, completion_pct, source)
		VALUES (?, ?, ?, ?, ?, ?)`,
		v.ContentID, v.ViewedAt, v.ViewerID, v.WatchDurationMs, v.CompletionPct, v.Source,
	).WithContext(ctx).Exec()
}

// ─── View Counts (Counters) ─────────────────────────────────────────

// ViewCounts holds the three counter types for a piece of content.
type ViewCounts struct {
	DisplayViews      int64 `json:"display_views"`
	QualityViews      int64 `json:"quality_views"`
	MonetizationViews int64 `json:"monetization_views"`
}

// IncrementViewCount increments one of the three view counter types.
// counterType must be one of: "display_views", "quality_views", "monetization_views".
func (s *EngagementStore) IncrementViewCount(ctx context.Context, contentID uuid.UUID, counterType string) error {
	// Validate counter type to prevent CQL injection
	switch counterType {
	case "display_views", "quality_views", "monetization_views":
		// valid
	default:
		return fmt.Errorf("invalid counter type: %s", counterType)
	}
	// Counter tables require UPDATE ... SET counter = counter + 1
	query := fmt.Sprintf(`UPDATE social_analytics.reel_view_counts SET %s = %s + 1 WHERE content_id = ?`, counterType, counterType)
	return s.session.Query(query, contentID).WithContext(ctx).Exec()
}

// GetViewCount returns the view counts for a content item.
func (s *EngagementStore) GetViewCount(ctx context.Context, contentID uuid.UUID) (*ViewCounts, error) {
	var c ViewCounts
	err := s.session.Query(`
		SELECT display_views, quality_views, monetization_views
		FROM social_analytics.reel_view_counts
		WHERE content_id = ?`,
		contentID,
	).WithContext(ctx).Scan(&c.DisplayViews, &c.QualityViews, &c.MonetizationViews)
	if err != nil {
		if err == gocql.ErrNotFound {
			return &ViewCounts{}, nil
		}
		return nil, err
	}
	return &c, nil
}

// ─── Content Engagement ─────────────────────────────────────────────

// ContentEngagement represents a single engagement event (like, share, comment, save, etc.).
type ContentEngagement struct {
	ContentID      uuid.UUID `json:"content_id"`
	EngagementType string    `json:"engagement_type"`
	UserID         uuid.UUID `json:"user_id"`
	CreatedAt      time.Time `json:"created_at"`
}

// RecordEngagement inserts a new engagement event.
func (s *EngagementStore) RecordEngagement(ctx context.Context, e *ContentEngagement) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	return s.session.Query(`
		INSERT INTO social_analytics.content_engagement (content_id, engagement_type, created_at, user_id)
		VALUES (?, ?, ?, ?)`,
		e.ContentID, e.EngagementType, e.CreatedAt, e.UserID,
	).WithContext(ctx).Exec()
}

// GetEngagementCounts returns the count of each engagement type for a content item.
func (s *EngagementStore) GetEngagementCounts(ctx context.Context, contentID uuid.UUID) (map[string]int64, error) {
	// Query each engagement type partition separately since Scylla requires
	// both partition key columns. We query the known set of engagement types.
	types := []string{"like", "comment", "share", "save", "report"}
	counts := make(map[string]int64, len(types))

	for _, engType := range types {
		var count int64
		err := s.session.Query(`
			SELECT COUNT(*) FROM social_analytics.content_engagement
			WHERE content_id = ? AND engagement_type = ?`,
			contentID, engType,
		).WithContext(ctx).Scan(&count)
		if err != nil && err != gocql.ErrNotFound {
			return nil, err
		}
		counts[engType] = count
	}

	return counts, nil
}

// ─── Schema DDL ─────────────────────────────────────────────────────

// EngagementDDL contains the CQL statements to create engagement analytics tables.
var EngagementDDL = []string{
	`CREATE TABLE IF NOT EXISTS social_analytics.reel_views (
		content_id UUID,
		viewer_id UUID,
		viewed_at TIMESTAMP,
		watch_duration_ms INT,
		completion_pct FLOAT,
		source TEXT,
		PRIMARY KEY ((content_id), viewed_at, viewer_id)
	) WITH CLUSTERING ORDER BY (viewed_at DESC)`,

	`CREATE TABLE IF NOT EXISTS social_analytics.reel_view_counts (
		content_id UUID PRIMARY KEY,
		display_views COUNTER,
		quality_views COUNTER,
		monetization_views COUNTER
	)`,

	`CREATE TABLE IF NOT EXISTS social_analytics.content_engagement (
		content_id UUID,
		engagement_type TEXT,
		user_id UUID,
		created_at TIMESTAMP,
		PRIMARY KEY ((content_id, engagement_type), created_at, user_id)
	) WITH CLUSTERING ORDER BY (created_at DESC)`,
}

// EnsureEngagementSchema creates the engagement tables if they don't exist.
func EnsureEngagementSchema(session *gocql.Session) error {
	for _, ddl := range EngagementDDL {
		if err := session.Query(ddl).Exec(); err != nil {
			return fmt.Errorf("execute engagement DDL: %w\nStatement: %s", err, ddl[:min(len(ddl), 80)])
		}
	}
	return nil
}

