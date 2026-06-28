package service

import (
	"context"
	"log/slog"
	"time"
)

// GradingConfig tunes the Phase 2 engagement backtest. The engagement
// percentile is the "answer key": an approval that landed in a high engagement
// percentile (vs cohort peers) is judged correct; a rejection of low-percentile
// content is judged correct. Correctness drives EWMA accuracy, tier, and bonus.
type GradingConfig struct {
	Enabled          bool
	Maturity         time.Duration // how long after a decision before grading (engagement needs time)
	WindowDays       int           // cohort percentile lookback
	ApproveThreshold float64       // pctile boundary for "should have been approved"
	EWMAAlpha        float64       // weight of the newest decision in the accuracy EWMA
	BonusPaise       int64         // flat bonus for a high-confidence correct call
	Interval         time.Duration // worker tick
}

func (c GradingConfig) withDefaults() GradingConfig {
	if c.Maturity <= 0 {
		c.Maturity = time.Hour
	}
	if c.WindowDays <= 0 {
		c.WindowDays = 7
	}
	if c.ApproveThreshold <= 0 {
		c.ApproveThreshold = 0.5
	}
	if c.EWMAAlpha <= 0 || c.EWMAAlpha > 1 {
		c.EWMAAlpha = 0.3
	}
	if c.BonusPaise <= 0 {
		c.BonusPaise = 1000
	}
	if c.Interval <= 0 {
		c.Interval = 5 * time.Minute
	}
	return c
}

func (s *Service) SetGrading(cfg GradingConfig) {
	s.grading = cfg.withDefaults()
}

// RunGradingWorker periodically backtests completed reviews against engagement.
func (s *Service) RunGradingWorker(ctx context.Context) {
	if !s.grading.Enabled {
		slog.Info("reviewer grading worker disabled")
		return
	}
	g := s.grading
	ticker := time.NewTicker(g.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := s.GradeOnce(ctx); err != nil {
				slog.Warn("grading pass failed", "err", err)
			} else if n > 0 {
				slog.Info("graded reviews", "count", n)
			}
		}
	}
}

// GradeOnce processes a batch of mature, ungraded decisions. Content without
// analytics data yet is skipped (graded on a later pass).
func (s *Service) GradeOnce(ctx context.Context) (int, error) {
	g := s.grading
	targets, err := s.store.AssignmentsToGrade(ctx, g.Maturity, 50)
	if err != nil {
		return 0, err
	}
	graded := 0
	for _, t := range targets {
		pctile, ok, err := s.store.EngagementPercentile(ctx, t.ContentID, g.WindowDays)
		if err != nil {
			slog.Warn("percentile lookup failed", "content", t.ContentID, "err", err)
			continue
		}
		if !ok {
			continue // not enough exposure data yet
		}

		// Correctness: approve rewarded by high percentile; reject rewarded by low.
		var score float64
		var correct bool
		switch t.Decision {
		case "approve":
			score = pctile
			correct = pctile >= g.ApproveThreshold
		case "reject":
			score = 1 - pctile
			correct = pctile < g.ApproveThreshold
		default:
			continue
		}

		var bonus int64
		if correct && score >= 0.6 {
			bonus = g.BonusPaise
		}

		acc, tier, err := s.store.RecordGrade(ctx, t, pctile, score, g.EWMAAlpha, bonus)
		if err != nil {
			slog.Warn("record grade failed", "assignment", t.AssignmentID, "err", err)
			continue
		}
		slog.Info("graded review", "assignment", t.AssignmentID, "decision", t.Decision,
			"pctile", pctile, "score", score, "bonus_paise", bonus, "accuracy", acc, "tier", tier)
		graded++
	}
	return graded, nil
}
