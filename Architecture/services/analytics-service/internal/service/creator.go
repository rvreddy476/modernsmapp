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

// CreatorStats holds aggregated analytics for a creator.
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
}

// CreatorService provides analytics aggregations for creators.
type CreatorService struct {
	store *postgres.AggregateStore
}

func NewCreatorService(store *postgres.AggregateStore) *CreatorService {
	return &CreatorService{store: store}
}

func (s *CreatorService) GetStats(ctx context.Context, userID uuid.UUID, period string) (*CreatorStats, error) {
	var since time.Time
	switch period {
	case "7d":
		since = time.Now().AddDate(0, 0, -7)
	case "30d":
		since = time.Now().AddDate(0, 0, -30)
	case "90d":
		since = time.Now().AddDate(0, 0, -90)
	default:
		since = time.Now().AddDate(0, 0, -30)
		period = "30d"
	}

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
	content, err := s.store.GetContentList(ctx, userID, 10, time.Now().Add(time.Second), "views_display")
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
	stats.PeriodEnd = time.Now()
	stats.Views = stats.TotalViews
	stats.Likes = stats.TotalLikes
	stats.Comments = stats.TotalComments
	stats.Shares = stats.TotalShares
	stats.FollowersGained = stats.FollowerGrowth
	stats.DataStatus = "recorded"
	stats.AsOf = stats.PeriodEnd
	return stats, nil
}
