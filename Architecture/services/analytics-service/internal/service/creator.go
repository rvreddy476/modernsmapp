package service

import (
	"context"
	"time"

	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/google/uuid"
)

// PostStat holds per-post analytics for the creator dashboard.
type PostStat struct {
	PostID   string `json:"post_id"`
	Views    int64  `json:"views"`
	Likes    int64  `json:"likes"`
	Comments int64  `json:"comments"`
}

// CreatorStats holds aggregated analytics for a creator.
type CreatorStats struct {
	UserID         string     `json:"user_id"`
	Period         string     `json:"period"`
	TotalViews     int64      `json:"total_views"`
	TotalLikes     int64      `json:"total_likes"`
	TotalComments  int64      `json:"total_comments"`
	TotalShares    int64      `json:"total_shares"`
	FollowerGrowth int64      `json:"follower_growth"`
	TopPosts       []PostStat `json:"top_posts"`
	ReachEstimate  int64      `json:"reach_estimate"`
	PeriodStart    time.Time  `json:"period_start"`
	PeriodEnd      time.Time  `json:"period_end"`
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
	}
	stats.UserID = userID.String()
	stats.Period = period
	stats.PeriodStart = since
	stats.PeriodEnd = time.Now()
	stats.ReachEstimate = stats.TotalViews * 3 // Simple estimate: views * 3
	return stats, nil
}
