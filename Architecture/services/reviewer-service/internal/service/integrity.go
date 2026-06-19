package service

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"github.com/atpost/reviewer-service/internal/store/postgres"
)

// IntegrityConfig tunes the Phase 3 integrity layer: silent audits of approvals,
// shadow re-reviews of rejects, behavioural-anomaly + ring detection, and the
// penalties (clawback / accuracy ding / auto-suspension) they trigger.
type IntegrityConfig struct {
	Enabled                  bool
	AuditRate                float64       // P(an approval gets a silent second review)
	ShadowRate               float64       // P(a rejection gets a shadow re-review)
	SecondaryTTL             time.Duration // SLA for a second review
	PenaltyAlpha             float64       // EWMA weight when dinging accuracy on a wrong call
	SuspendThreshold         int           // flags in 30d that auto-suspend
	RubberstampRatio         float64       // watched/content below this on an approve = rubber-stamp
	HighApprovalMinDecisions int
	HighApprovalThreshold    float64
	RingWindowDays           int
	RingMinApprovals         int
	Interval                 time.Duration // anomaly/ring worker tick
}

func (c IntegrityConfig) withDefaults() IntegrityConfig {
	if c.AuditRate <= 0 {
		c.AuditRate = 0.1
	}
	if c.ShadowRate <= 0 {
		c.ShadowRate = 0.1
	}
	if c.SecondaryTTL <= 0 {
		c.SecondaryTTL = 30 * time.Minute
	}
	if c.PenaltyAlpha <= 0 || c.PenaltyAlpha > 1 {
		c.PenaltyAlpha = 0.4
	}
	if c.SuspendThreshold <= 0 {
		c.SuspendThreshold = 3
	}
	if c.RubberstampRatio <= 0 {
		c.RubberstampRatio = 0.2
	}
	if c.HighApprovalMinDecisions <= 0 {
		c.HighApprovalMinDecisions = 10
	}
	if c.HighApprovalThreshold <= 0 {
		c.HighApprovalThreshold = 0.95
	}
	if c.RingWindowDays <= 0 {
		c.RingWindowDays = 30
	}
	if c.RingMinApprovals <= 0 {
		c.RingMinApprovals = 5
	}
	if c.Interval <= 0 {
		c.Interval = 10 * time.Minute
	}
	return c
}

func (s *Service) SetIntegrity(cfg IntegrityConfig) {
	s.integrity = cfg.withDefaults()
}

// onPrimaryDecided samples a primary decision and, when selected, spins up a
// blind second review by a different, unrelated reviewer. Fire-and-forget.
func (s *Service) onPrimaryDecided(a *postgres.Assignment, decision string) {
	if !s.integrity.Enabled {
		return
	}
	var kind string
	switch decision {
	case "approve":
		if rand.Float64() >= s.integrity.AuditRate {
			return
		}
		kind = "audit"
	case "reject":
		if rand.Float64() >= s.integrity.ShadowRate {
			return
		}
		kind = "shadow"
	default:
		return // flag_unsafe is routed to trust-safety, not double-reviewed here
	}

	contentID, creatorID, primaryReviewer := a.ContentID, a.CreatorID, a.ReviewerID
	contentSeconds := a.ContentSeconds
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		cands, err := s.store.SecondaryCandidates(ctx, primaryReviewer, 10)
		if err != nil {
			slog.Warn("secondary candidates lookup failed", "content", contentID, "err", err)
			return
		}
		for _, c := range cands {
			related, relErr := s.clients.IsRelated(ctx, c.UserID, creatorID)
			if relErr != nil || related {
				continue // fail-closed + anti-collusion
			}
			if _, err := s.store.InsertSecondaryAssignment(ctx, contentID, creatorID, c.ID,
				contentSeconds, kind, s.integrity.SecondaryTTL); err != nil {
				continue
			}
			slog.Info("spawned second review", "kind", kind, "content", contentID, "reviewer", c.ID)
			return
		}
	}()
}

// onSecondaryDecided compares an audit/shadow verdict to the primary and, on a
// mismatch, penalises the PRIMARY reviewer (flag + clawback + accuracy ding +
// possible suspension). Fire-and-forget.
func (s *Service) onSecondaryDecided(a *postgres.Assignment, secondaryDecision string) {
	contentID := a.ContentID
	kind := a.Kind
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		prim, err := s.store.PrimaryDecisionForContent(ctx, contentID)
		if err != nil {
			return
		}
		var flagType string
		switch kind {
		case "audit": // primary approved → mismatch if the auditor would block
			if secondaryDecision == "reject" || secondaryDecision == "flag_unsafe" {
				flagType = "audit_mismatch"
			}
		case "shadow": // primary rejected → mismatch if the shadow would approve
			if secondaryDecision == "approve" {
				flagType = "shadow_mismatch"
			}
		}
		if flagType == "" {
			return // the two reviewers agreed — nothing to do
		}
		suspended, err := s.store.ApplyPenalty(ctx, postgres.PenaltyParams{
			ReviewerID:       prim.ReviewerID,
			AssignmentID:     &prim.AssignmentID,
			FlagType:         flagType,
			Severity:         2,
			Details:          "second_review=" + secondaryDecision,
			Clawback:         true,
			PenaltyScore:     0,
			EWMAAlpha:        s.integrity.PenaltyAlpha,
			SuspendThreshold: s.integrity.SuspendThreshold,
		})
		if err != nil {
			slog.Warn("apply mismatch penalty failed", "primary_assignment", prim.AssignmentID, "err", err)
			return
		}
		slog.Info("second-review mismatch penalised", "type", flagType,
			"primary_reviewer", prim.ReviewerID, "suspended", suspended)
	}()
}

// RunIntegrityWorker periodically scans for behavioural anomalies and collusion
// rings and applies penalties/flags.
func (s *Service) RunIntegrityWorker(ctx context.Context) {
	if !s.integrity.Enabled {
		slog.Info("reviewer integrity worker disabled")
		return
	}
	ic := s.integrity
	ticker := time.NewTicker(ic.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.integrityScanOnce(ctx)
		}
	}
}

func (s *Service) integrityScanOnce(ctx context.Context) {
	ic := s.integrity

	// 1. Rubber-stamping: approvals with near-zero active watch time.
	stamps, err := s.store.RubberstampTargets(ctx, 7*24*time.Hour, ic.RubberstampRatio, 100)
	if err != nil {
		slog.Warn("rubberstamp scan failed", "err", err)
	}
	for _, t := range stamps {
		id := t.AssignmentID
		if _, err := s.store.ApplyPenalty(ctx, postgres.PenaltyParams{
			ReviewerID:       t.ReviewerID,
			AssignmentID:     &id,
			FlagType:         "anomaly_rubberstamp",
			Severity:         1,
			Details:          "approved with watch < ratio",
			EWMAAlpha:        s.integrity.PenaltyAlpha,
			PenaltyScore:     0,
			SuspendThreshold: s.integrity.SuspendThreshold,
		}); err != nil {
			slog.Warn("rubberstamp penalty failed", "assignment", id, "err", err)
		}
	}

	// 2. Approval-rate-at-scale: soft flag only (no accuracy ding, no suspend).
	ids, err := s.store.HighApprovalReviewers(ctx, ic.HighApprovalMinDecisions, ic.HighApprovalThreshold)
	if err != nil {
		slog.Warn("approval-rate scan failed", "err", err)
	}
	for _, id := range ids {
		if _, err := s.store.ApplyPenalty(ctx, postgres.PenaltyParams{
			ReviewerID: id,
			FlagType:   "anomaly_approval_rate",
			Severity:   1,
			Details:    "approval rate over threshold",
		}); err != nil {
			slog.Warn("approval-rate flag failed", "reviewer", id, "err", err)
		}
	}

	// 3. Ring detection: soft flag for investigation (no accuracy ding, no suspend).
	pairs, err := s.store.RingSuspects(ctx, ic.RingWindowDays, ic.RingMinApprovals)
	if err != nil {
		slog.Warn("ring scan failed", "err", err)
	}
	for _, p := range pairs {
		if _, err := s.store.ApplyPenalty(ctx, postgres.PenaltyParams{
			ReviewerID: p.ReviewerID,
			FlagType:   "ring_suspect",
			Severity:   1,
			Details:    p.CreatorID.String(),
		}); err != nil {
			slog.Warn("ring flag failed", "reviewer", p.ReviewerID, "err", err)
		}
	}
}
