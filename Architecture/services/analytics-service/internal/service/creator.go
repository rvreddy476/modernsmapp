package service

import (
	"context"
	"time"

	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/google/uuid"
)

// PostStat holds per-post analytics for the creator dashboard.
type PostStat struct {
	PostID      string `json:"post_id"`
	ContentType string `json:"content_type"`
	Views       int64  `json:"views"`
	Likes       int64  `json:"likes"`
	Shares      int64  `json:"shares"`
}

type DailyStat struct {
	Date  time.Time `json:"date"`
	Views int64     `json:"views"`
}

// RealtimeStats is the last 48 hours of display views, for the "right now"
// strip on the creator dashboard (MTube, 2026-09-27).
type RealtimeStats struct {
	Views48h  int64   `json:"views_48h"`
	Series48h []int64 `json:"series_48h"`
}

// CreatorStats holds aggregated analytics for a creator.
//
// The keys up to period_end predate MTube and are unchanged in meaning
// except `views`, which is now the display-view figure the creator is paid
// on (daily summaries plus today's open hours — the same sum the post's
// view counter shows) rather than a mirror of `total_views`, the hourly
// aggregate sum. The MTube keys follow.
type CreatorStats struct {
	UserID          string      `json:"user_id"`
	Period          string      `json:"period"`
	TotalViews      int64       `json:"total_views"`
	TotalLikes      int64       `json:"total_likes"`
	TotalComments   int64       `json:"total_comments"`
	TotalShares     int64       `json:"total_shares"`
	FollowerGrowth  int64       `json:"follower_growth"`
	TopPosts        []PostStat  `json:"top_posts"`
	DailyStats      []DailyStat `json:"daily_stats"`
	Views           int64       `json:"views"`
	Likes           int64       `json:"likes"`
	Comments        int64       `json:"comments"`
	Shares          int64       `json:"shares"`
	FollowersGained int64       `json:"followers_gained"`
	DataStatus      string      `json:"data_status"`
	AsOf            time.Time   `json:"as_of"`
	PeriodStart     time.Time   `json:"period_start"`
	PeriodEnd       time.Time   `json:"period_end"`

	// MTube (2026-09-27).
	WatchTimeMS   int64 `json:"watch_time_ms"`
	UniqueViewers int64 `json:"unique_viewers"`
	// FollowersDelta is null until graph-service publishes follower counts
	// into analytics; follows_from_content (follower_growth above) is a
	// different number and is not substituted for it.
	FollowersDelta *int64                       `json:"followers_delta"`
	TopContent     []postgres.ContentPeriodStat `json:"top_content"`
	Realtime       RealtimeStats                `json:"realtime"`
}

// CreatorStatsStore is the slice of the aggregate store GetStats reads; an
// interface so the response shape can be pinned without PostgreSQL.
type CreatorStatsStore interface {
	GetCreatorAggStats(ctx context.Context, creatorID uuid.UUID, since time.Time) (*postgres.CreatorAggStats, error)
	GetCreatorDailyTrend(ctx context.Context, creatorID uuid.UUID, since time.Time) ([]postgres.DailySummaryRow, error)
	GetContentList(ctx context.Context, creatorID uuid.UUID, limit int, cursor time.Time, sortBy string) ([]postgres.ContentSummary, error)
	GetCreatorPeriodTotals(ctx context.Context, creatorID uuid.UUID, since, now time.Time) (*postgres.CreatorPeriodTotals, error)
	GetCreatorTopContent(ctx context.Context, creatorID uuid.UUID, since, now time.Time, limit int) ([]postgres.ContentPeriodStat, error)
	GetCreatorHourlyViews(ctx context.Context, creatorID uuid.UUID, now time.Time, hours int) ([]int64, error)
}

// CreatorService provides analytics aggregations for creators.
type CreatorService struct {
	store CreatorStatsStore
	now   func() time.Time
}

func NewCreatorService(store *postgres.AggregateStore) *CreatorService {
	return NewCreatorServiceFrom(store)
}

// NewCreatorServiceFrom builds the service on any store slice (tests).
func NewCreatorServiceFrom(store CreatorStatsStore) *CreatorService {
	return &CreatorService{store: store, now: time.Now}
}

// WithClock pins "now" (tests).
func (s *CreatorService) WithClock(now func() time.Time) *CreatorService {
	s.now = now
	return s
}

// realtimeHours is the length of the real-time strip.
const realtimeHours = 48

// creatorPeriodDays resolves the period query. 7d/28d/90d/365d are the
// MTube set; 30d predates it and stays for the callers that send it;
// anything else is the old default, 30d.
func creatorPeriodDays(period string) (string, int) {
	switch period {
	case "7d":
		return period, 7
	case "28d":
		return period, 28
	case "30d":
		return period, 30
	case "90d":
		return period, 90
	case "365d":
		return period, 365
	}
	return "30d", 30
}

func (s *CreatorService) GetStats(ctx context.Context, userID uuid.UUID, period string) (*CreatorStats, error) {
	now := s.now()
	period, days := creatorPeriodDays(period)
	since := now.AddDate(0, 0, -days)

	agg, err := s.store.GetCreatorAggStats(ctx, userID, since)
	if err != nil {
		return nil, err
	}

	stats := &CreatorStats{}
	if agg != nil {
		stats.TotalViews = agg.TotalViews
		stats.TotalLikes = agg.TotalLikes
		stats.TotalComments = agg.TotalComments
		stats.TotalShares = agg.TotalShares
		// follower_growth was previously always zero because nothing
		// populated it. follows_from_content — a viewer following the
		// creator directly off a video — is exactly the number this
		// field is supposed to report.
		stats.FollowerGrowth = agg.TotalFollows
	}
	daily, err := s.store.GetCreatorDailyTrend(ctx, userID, since)
	if err != nil {
		return nil, err
	}
	content, err := s.store.GetContentList(ctx, userID, 10, now.Add(time.Second), "views_display")
	if err != nil {
		return nil, err
	}
	totals, err := s.store.GetCreatorPeriodTotals(ctx, userID, since, now)
	if err != nil {
		return nil, err
	}
	topContent, err := s.store.GetCreatorTopContent(ctx, userID, since, now, 10)
	if err != nil {
		return nil, err
	}
	series, err := s.store.GetCreatorHourlyViews(ctx, userID, now, realtimeHours)
	if err != nil {
		return nil, err
	}
	stats.DailyStats = make([]DailyStat, 0, len(daily))
	for _, row := range daily {
		stats.DailyStats = append(stats.DailyStats, DailyStat{Date: row.Date, Views: row.Views})
	}
	stats.TopPosts = make([]PostStat, 0, len(content))
	for _, row := range content {
		stats.TopPosts = append(stats.TopPosts, PostStat{
			PostID: row.ContentID.String(), ContentType: row.ContentType,
			Views: row.ViewsDisplay, Likes: row.Likes, Shares: row.Shares,
		})
	}
	stats.UserID = userID.String()
	stats.Period = period
	stats.PeriodStart = since
	stats.PeriodEnd = now
	stats.Views = totals.Views
	stats.Likes = stats.TotalLikes
	stats.Comments = stats.TotalComments
	stats.Shares = stats.TotalShares
	stats.FollowersGained = stats.FollowerGrowth
	stats.DataStatus = "recorded"
	stats.AsOf = stats.PeriodEnd

	stats.WatchTimeMS = totals.WatchTimeMS
	stats.UniqueViewers = totals.UniqueViewers
	stats.FollowersDelta = nil
	stats.TopContent = topContent
	if stats.TopContent == nil {
		stats.TopContent = []postgres.ContentPeriodStat{}
	}
	if len(series) != realtimeHours {
		padded := make([]int64, realtimeHours)
		copy(padded[realtimeHours-min(len(series), realtimeHours):], series[max(0, len(series)-realtimeHours):])
		series = padded
	}
	var views48h int64
	for _, v := range series {
		views48h += v
	}
	stats.Realtime = RealtimeStats{Views48h: views48h, Series48h: series}
	return stats, nil
}
