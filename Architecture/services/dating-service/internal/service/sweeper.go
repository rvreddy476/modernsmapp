package service

import (
	"context"
	"log/slog"
	"time"
)

// Dating periodic sweeper — PRODUCTION_GAP_ANALYSIS.md §P1-6 cron
// equivalents. Runs every minute and processes five kinds of
// time-driven side-effects in one pass:
//
//  1. Stale matches → match.expired (ExpireStaleMatches).
//  2. Quiet matches → match.quiet (MarkQuietMatches).
//  3. Safe-meet reminders 12h ahead → safe_meet.reminder.
//  4. Safe-meet no-show after 30min grace → safe_meet.missed_check_in.
//  5. Stale account-risk rows → ComputeRisk recompute (§P0-7 Phase A).
//
// All operations are idempotent at the store layer — either a status
// transition (match), a FOR UPDATE SKIP LOCKED claim with a fired_at
// timestamp (meets), or an UPSERT (risk). Safe to run on multiple
// replicas; the risk recompute re-running on the same user just
// refreshes the row.

// SweeperConfig tunes the sweeper windows. Production defaults match
// the gap-analysis spec: 12h reminder lead, 30min missed-check-in
// grace, 1 min tick. §P0-7 Phase A adds the risk recompute knobs:
// recompute rows older than 1h, capped at 500 users per tick.
type SweeperConfig struct {
	Interval                 time.Duration
	ReminderLeadMin          time.Duration
	ReminderLeadMax          time.Duration
	MissedCheckInGracePeriod time.Duration
	BatchLimit               int
	RiskStaleAfter           time.Duration
	RiskRecomputeBatch       int
}

func defaultSweeperConfig() SweeperConfig {
	return SweeperConfig{
		Interval:                 1 * time.Minute,
		ReminderLeadMin:          11*time.Hour + 30*time.Minute,
		ReminderLeadMax:          12*time.Hour + 30*time.Minute,
		MissedCheckInGracePeriod: 30 * time.Minute,
		BatchLimit:               200,
		RiskStaleAfter:           1 * time.Hour,
		RiskRecomputeBatch:       500,
	}
}

// StartSweeper kicks off the dating-domain sweeper goroutine.
// Returns nothing; cancel via the supplied context. cmd/server/main.go
// calls this once on startup. Errors are logged but never fatal —
// the next tick retries.
func (s *Service) StartSweeper(ctx context.Context) {
	cfg := defaultSweeperConfig()
	go s.runSweeperLoop(ctx, cfg)
}

func (s *Service) runSweeperLoop(ctx context.Context, cfg SweeperConfig) {
	slog.Info("dating sweeper started",
		"interval", cfg.Interval,
		"reminder_lead_min", cfg.ReminderLeadMin,
		"reminder_lead_max", cfg.ReminderLeadMax,
		"missed_grace", cfg.MissedCheckInGracePeriod)
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	// Run once on boot so a restart doesn't wait a full minute before
	// catching up.
	s.runSweeperOnce(ctx, cfg)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runSweeperOnce(ctx, cfg)
		}
	}
}

// runSweeperOnce executes one pass of all four sweeps. Each step is
// independent — failure in one doesn't skip the others.
func (s *Service) runSweeperOnce(ctx context.Context, cfg SweeperConfig) {
	// 1. Stale matches → match.expired.
	if n, err := s.ExpireStaleMatches(ctx); err != nil {
		slog.Warn("sweeper: ExpireStaleMatches failed", "error", err)
	} else if n > 0 {
		slog.Info("sweeper: matches expired", "count", n)
	}

	// 2. Quiet matches → match.quiet.
	if n, err := s.MarkQuietMatches(ctx); err != nil {
		slog.Warn("sweeper: MarkQuietMatches failed", "error", err)
	} else if n > 0 {
		slog.Info("sweeper: matches quieted", "count", n)
	}

	// 2b. Notify both participants on each newly-quiet match
	// (dating.match.quiet_notify). The ClaimMatchesForQuietNotify
	// store call atomically claims rows where quiet_notified_at IS
	// NULL via FOR UPDATE SKIP LOCKED, so each match emits at most
	// one notify event across the cluster.
	if n, err := s.EmitQuietMatchNotifications(ctx, cfg.BatchLimit); err != nil {
		slog.Warn("sweeper: EmitQuietMatchNotifications failed", "error", err)
	} else if n > 0 {
		slog.Info("sweeper: quiet-notify events fired", "count", n)
	}

	// 3. Safe-meet reminders 12h ahead.
	if s.producer != nil {
		meets, err := s.store.ClaimMeetsDueForReminder(
			ctx, cfg.ReminderLeadMin, cfg.ReminderLeadMax, cfg.BatchLimit,
		)
		if err != nil {
			slog.Warn("sweeper: ClaimMeetsDueForReminder failed", "error", err)
		}
		for _, m := range meets {
			venue := ""
			if m.Venue != nil {
				venue = *m.Venue
			}
			// Notify both participants. The producer dedups at the
			// notification-service consumer based on (meet_id, user_id).
			_ = s.producer.PublishSafeMeetReminder(ctx, m.ID, m.UserID, m.WithUserID, m.ScheduledAt, venue)
			_ = s.producer.PublishSafeMeetReminder(ctx, m.ID, m.WithUserID, m.UserID, m.ScheduledAt, venue)
		}
		if len(meets) > 0 {
			slog.Info("sweeper: safe-meet reminders fired", "count", len(meets))
		}
	}

	// 4. Safe-meet missed-check-in after 30min.
	if s.producer != nil {
		meets, err := s.store.ClaimMeetsMissedCheckIn(
			ctx, cfg.MissedCheckInGracePeriod, cfg.BatchLimit,
		)
		if err != nil {
			slog.Warn("sweeper: ClaimMeetsMissedCheckIn failed", "error", err)
		}
		for _, m := range meets {
			expected := m.ScheduledAt.Add(cfg.MissedCheckInGracePeriod)
			// Both participants get notified — the initiator's
			// trusted contact also receives the missed-check-in alert
			// via the notification-service consumer's downstream
			// trusted-contact dispatch (handled out-of-band).
			_ = s.producer.PublishSafeMeetMissedCheckIn(ctx, m.ID, m.UserID, m.WithUserID, m.ScheduledAt, expected)
			_ = s.producer.PublishSafeMeetMissedCheckIn(ctx, m.ID, m.WithUserID, m.UserID, m.ScheduledAt, expected)
		}
		if len(meets) > 0 {
			slog.Info("sweeper: missed-check-in events fired", "count", len(meets))
		}
	}

	// 5. §P0-7 Phase A: recompute risk for users whose row is older
	// than `RiskStaleAfter`. Idempotent — re-running on the same user
	// just refreshes the row. Capped at `RiskRecomputeBatch` per tick
	// so a 100k-user backlog drains over time rather than hammering
	// Postgres in a single sweep.
	if n, err := s.RecomputeStaleRisks(ctx, cfg.RiskStaleAfter, cfg.RiskRecomputeBatch); err != nil {
		slog.Warn("sweeper: RecomputeStaleRisks failed", "error", err)
	} else if n > 0 {
		slog.Info("sweeper: account risks recomputed", "count", n)
	}
}
