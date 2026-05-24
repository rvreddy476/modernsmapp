package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// PrepTimePrediction is the rolling-window prep-time signal for one
// restaurant. avg_actual_minutes is the median actual prep duration
// (CONFIRMED → READY_FOR_PICKUP) over the trailing 14 days; falls back
// to restaurant.avg_preparation_minutes if there's no history.
type PrepTimePrediction struct {
	RestaurantID       uuid.UUID `json:"restaurant_id"`
	AvgConfigMinutes   int       `json:"avg_config_minutes"`
	AvgActualMinutes   float64   `json:"avg_actual_minutes"`
	SampleSize         int       `json:"sample_size"`
	RecommendedMinutes int       `json:"recommended_minutes"`
}

// PredictPrepTime returns the prep-time signal. recommended_minutes
// blends the two sources to avoid wild swings from a small sample:
//
//   - sample < 5      → use config (avg_preparation_minutes).
//   - sample 5..29    → 50/50 blend.
//   - sample ≥ 30     → use actual.
//
// All consumers (UI ETA, kitchen queue SLA, dispatch worker) read
// recommended_minutes and ignore the components.
func (s *Store) PredictPrepTime(ctx context.Context, restaurantID uuid.UUID) (*PrepTimePrediction, error) {
	p := PrepTimePrediction{RestaurantID: restaurantID}
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(avg_preparation_minutes, 25)
		FROM food.restaurants WHERE id = $1
	`, restaurantID).Scan(&p.AvgConfigMinutes); err != nil {
		return nil, fmt.Errorf("read avg_preparation_minutes: %w", err)
	}
	// Median is more robust to outliers than mean for this signal.
	if err := s.db.QueryRow(ctx, `
		SELECT
			COALESCE(percentile_cont(0.5) WITHIN GROUP (
				ORDER BY EXTRACT(EPOCH FROM (ready.created_at - confirmed.created_at)) / 60.0
			), 0)::float8 AS median_minutes,
			COUNT(*)::int AS sample
		FROM food.orders o
		JOIN LATERAL (
			SELECT MIN(created_at) AS created_at FROM food.order_status_history
			WHERE order_id = o.id AND to_status = 'CONFIRMED'
		) confirmed ON TRUE
		JOIN LATERAL (
			SELECT MIN(created_at) AS created_at FROM food.order_status_history
			WHERE order_id = o.id AND to_status = 'READY_FOR_PICKUP'
		) ready ON TRUE
		WHERE o.restaurant_id = $1
		  AND ready.created_at IS NOT NULL
		  AND confirmed.created_at IS NOT NULL
		  AND ready.created_at > NOW() - INTERVAL '14 days'
	`, restaurantID).Scan(&p.AvgActualMinutes, &p.SampleSize); err != nil {
		return nil, fmt.Errorf("read actual prep time: %w", err)
	}
	switch {
	case p.SampleSize < 5:
		p.RecommendedMinutes = p.AvgConfigMinutes
	case p.SampleSize < 30:
		blended := (float64(p.AvgConfigMinutes) + p.AvgActualMinutes) / 2
		p.RecommendedMinutes = int(blended + 0.5)
	default:
		p.RecommendedMinutes = int(p.AvgActualMinutes + 0.5)
	}
	if p.RecommendedMinutes < 1 {
		p.RecommendedMinutes = p.AvgConfigMinutes
	}
	return &p, nil
}
