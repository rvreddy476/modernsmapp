package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/reviewer-service/internal/clients"
	"github.com/atpost/reviewer-service/internal/prefilter"
	"github.com/atpost/reviewer-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	ErrNotReviewer  = errors.New("not a reviewer")
	ErrSuspended    = errors.New("reviewer suspended")
	ErrAtCapacity   = errors.New("at max concurrent assignments")
	ErrNoWork       = errors.New("no eligible content")
	ErrInvalidInput = errors.New("invalid input")
)

type Service struct {
	store         *postgres.Store
	clients       *clients.Clients
	basePayPaise  int64
	rotationCapK  int
	assignmentTTL time.Duration
	// creditLedger gates the best-effort monetization credit. OFF in Phase 1:
	// base pay is accrued durably in reviewer.reviewer_ledger (paise) and
	// settled into the monetization ledger in Phase 2, once that service's
	// rupee/paise unit convention is verified (avoids a money bug).
	creditLedger bool

	// grading holds the Phase 2 engagement-backtest config (see grading.go).
	grading GradingConfig
	// integrity holds the Phase 3 audit/anomaly/ring config (see integrity.go).
	integrity IntegrityConfig
	// prefilter is the Phase 4 ML pre-filter; nil disables it (all flagged
	// content goes to humans). See SetPrefilter.
	prefilter prefilter.Classifier
	// promotion is the Phase 4b staged→public promotion config (see integrity.go).
	promotion PromotionConfig
}

// SetPrefilter installs the Phase 4 pre-filter classifier. Nil keeps the
// pre-Phase-4 behaviour (every flagged item is queued for a human).
func (s *Service) SetPrefilter(c prefilter.Classifier) {
	s.prefilter = c
}

func New(store *postgres.Store, c *clients.Clients, basePayPaise int64, rotationCapK int, ttl time.Duration, creditLedger bool) *Service {
	if rotationCapK <= 0 {
		rotationCapK = 3
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &Service{store: store, clients: c, basePayPaise: basePayPaise, rotationCapK: rotationCapK, assignmentTTL: ttl, creditLedger: creditLedger}
}

func (s *Service) OptIn(ctx context.Context, userID uuid.UUID, languages []string, region string) (*postgres.Reviewer, error) {
	return s.store.OptIn(ctx, userID, languages, region)
}

func (s *Service) Me(ctx context.Context, userID uuid.UUID) (*postgres.Reviewer, error) {
	return s.store.GetReviewerByUser(ctx, userID)
}

func (s *Service) SetOnline(ctx context.Context, userID uuid.UUID, online bool) error {
	r, err := s.store.GetReviewerByUser(ctx, userID)
	if err != nil {
		return ErrNotReviewer
	}
	return s.store.SetOnline(ctx, r.ID, online)
}

func (s *Service) Enqueue(ctx context.Context, q postgres.QueueItem) error {
	if q.ContentID == uuid.Nil || q.CreatorID == uuid.Nil {
		return ErrInvalidInput
	}

	// Phase 4 ML pre-filter: only the ambiguous middle reaches a human.
	if s.prefilter != nil {
		res, err := s.prefilter.Classify(ctx, prefilter.Input{
			ContentID:      q.ContentID,
			CreatorID:      q.CreatorID,
			ContentType:    q.ContentType,
			SpamScore:      q.SpamScore,
			ContentSeconds: q.ContentSeconds,
		})
		if err == nil {
			switch res.Decision {
			case prefilter.AutoReject:
				// Leave the post flagged (already hidden); make it terminal.
				if err := s.clients.SetPostReviewStatus(ctx, q.ContentID, "rejected"); err != nil {
					slog.Warn("prefilter auto-reject flip failed (left flagged)", "content", q.ContentID, "err", err)
				}
				slog.Info("prefilter auto-rejected", "content", q.ContentID, "conf", res.Confidence)
				return nil
			case prefilter.AutoApprove:
				// Approving REQUIRES flipping review_status, else good content
				// stays hidden. If the flip fails, fall through to a human.
				if err := s.clients.SetPostReviewStatus(ctx, q.ContentID, "approved"); err != nil {
					slog.Warn("prefilter auto-approve flip failed; routing to human", "content", q.ContentID, "err", err)
					break
				}
				slog.Info("prefilter auto-approved", "content", q.ContentID, "conf", res.Confidence)
				return nil
			}
		} else {
			slog.Warn("prefilter classify failed; routing to human", "content", q.ContentID, "err", err)
		}
	}

	return s.store.Enqueue(ctx, q)
}

// NextAssignment is the pull-based matcher: hard-filters candidate content by
// language + rotation cap (in SQL) and graph relationship (anti-collusion),
// then atomically claims the first eligible item. Blind: creator identity is
// stripped from the returned assignment.
func (s *Service) NextAssignment(ctx context.Context, userID uuid.UUID) (*postgres.Assignment, error) {
	r, err := s.store.GetReviewerByUser(ctx, userID)
	if err != nil {
		return nil, ErrNotReviewer
	}
	if r.Status == "suspended" {
		return nil, ErrSuspended
	}
	active, err := s.store.ActiveAssignmentCount(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	if active >= r.MaxConcurrent {
		return nil, ErrAtCapacity
	}
	_ = s.store.SetOnline(ctx, r.ID, true)

	candidates, err := s.store.CandidateQueue(ctx, r.ID, r.Languages, s.rotationCapK, 15)
	if err != nil {
		return nil, err
	}
	for _, cand := range candidates {
		related, relErr := s.clients.IsRelated(ctx, r.UserID, cand.CreatorID)
		if relErr != nil {
			slog.Warn("graph relationship check failed; skipping candidate (fail-closed)",
				"content_id", cand.ContentID, "err", relErr)
			continue
		}
		if related {
			continue // anti-collusion: never assign a connected pair
		}
		a, claimErr := s.store.ClaimAndAssign(ctx, cand, r.ID, s.assignmentTTL)
		if errors.Is(claimErr, postgres.ErrAlreadyClaimed) {
			continue // lost the race; try next
		}
		if claimErr != nil {
			return nil, claimErr
		}
		a.CreatorID = uuid.Nil // blind review
		return a, nil
	}
	return nil, ErrNoWork
}

func (s *Service) Heartbeat(ctx context.Context, userID, assignmentID uuid.UUID, addSeconds int) (int, error) {
	if addSeconds <= 0 || addSeconds > 120 {
		return 0, ErrInvalidInput // a heartbeat covers a short interval
	}
	r, err := s.store.GetReviewerByUser(ctx, userID)
	if err != nil {
		return 0, ErrNotReviewer
	}
	return s.store.Heartbeat(ctx, assignmentID, r.ID, addSeconds)
}

// Decide records the verdict, completes the assignment, accrues capped base pay
// (durable, local), and best-effort credits the monetization ledger. flag_unsafe
// is recorded here; routing to trust-safety's pipeline is wired in Phase 3.
func (s *Service) Decide(ctx context.Context, userID, assignmentID uuid.UUID, decision, reason string) (*postgres.Assignment, error) {
	switch decision {
	case "approve", "reject", "flag_unsafe":
	default:
		return nil, ErrInvalidInput
	}
	r, err := s.store.GetReviewerByUser(ctx, userID)
	if err != nil {
		return nil, ErrNotReviewer
	}
	a, err := s.store.Decide(ctx, assignmentID, r.ID, decision, reason)
	if err != nil {
		return nil, err
	}

	// Base pay accrual is durable + idempotent (local ledger). The monetization
	// credit is best-effort now; Phase 2 settlement reconciles accruals → payout.
	if s.basePayPaise > 0 {
		if err := s.store.MarkBasePaid(ctx, a.ID, r.ID, s.basePayPaise); err != nil {
			slog.Error("accrue base pay failed", "assignment", a.ID, "err", err)
		} else if s.creditLedger {
			if err := s.clients.CreditReviewer(ctx, r.UserID, s.basePayPaise,
				a.ID.String(), "reviewer base pay"); err != nil {
				slog.Warn("monetization credit deferred (accrued locally)", "assignment", a.ID, "err", err)
			}
		}
	}

	// Phase 3 integrity hooks (run before the creator id is blinded below).
	if a.Kind == "primary" {
		s.onPrimaryDecided(a, decision)
	} else {
		s.onSecondaryDecided(a, decision)
	}

	a.CreatorID = uuid.Nil // keep creator hidden in the response
	return a, nil
}

// RunExpirySweeper periodically expires overdue assignments and re-queues them.
func (s *Service) RunExpirySweeper(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := s.store.ExpireOverdue(ctx)
			if err != nil {
				slog.Warn("expiry sweep failed", "err", err)
			} else if n > 0 {
				slog.Info("expired overdue assignments", "count", n)
			}
		}
	}
}
