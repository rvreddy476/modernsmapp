package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
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
	ErrKYCRequired  = errors.New("identity verification required")
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

// RefreshKYC syncs the reviewer's kyc_verified flag from wallet-service
// (identity verification). Returns the verified state. Idempotent.
func (s *Service) RefreshKYC(ctx context.Context, userID uuid.UUID) (bool, error) {
	r, err := s.store.GetReviewerByUser(ctx, userID)
	if err != nil {
		return false, ErrNotReviewer
	}
	verified, err := s.clients.IsKYCVerified(ctx, userID)
	if err != nil {
		return r.KYCVerified, err // keep last-known on transient errors
	}
	if verified != r.KYCVerified {
		_ = s.store.SetKYCVerified(ctx, r.ID, verified)
	}
	return verified, nil
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
func (s *Service) NextAssignment(ctx context.Context, userID uuid.UUID, targetContentID uuid.UUID) (*postgres.Assignment, error) {
	r, err := s.store.GetReviewerByUser(ctx, userID)
	if err != nil {
		return nil, ErrNotReviewer
	}
	if r.Status == "suspended" {
		return nil, ErrSuspended
	}
	// Identity gate: no assignments (hence no pay) until KYC is verified. Cached
	// on the reviewer row; re-checked against wallet-service while unverified.
	if !r.KYCVerified {
		verified, _ := s.RefreshKYC(ctx, userID)
		if !verified {
			return nil, ErrKYCRequired
		}
	}
	_ = s.store.SetOnline(ctx, r.ID, true)

	// Resume an in-flight assignment instead of erroring at capacity, so a
	// reviewer who reloads the console gets their current video back.
	if cur, err := s.store.ActiveAssignmentForReviewer(ctx, r.ID); err == nil && cur != nil {
		cur.CreatorID = uuid.Nil
		return cur, nil
	}

	active, err := s.store.ActiveAssignmentCount(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	if active >= r.MaxConcurrent {
		return nil, ErrAtCapacity
	}

	candidates, err := s.store.CandidateQueue(ctx, r.ID, r.Languages, s.rotationCapK, 50)
	if err != nil {
		return nil, err
	}

	var chosen *postgres.QueueItem
	if targetContentID != uuid.Nil {
		for _, cand := range candidates {
			if cand.ContentID == targetContentID {
				chosen = &cand
				break
			}
		}
		if chosen == nil {
			return nil, ErrNoWork
		}
	}

	for _, cand := range candidates {
		if chosen != nil && cand.ContentID != chosen.ContentID {
			continue
		}
		related, relErr := s.clients.IsRelated(ctx, r.UserID, cand.CreatorID)
		if relErr != nil {
			slog.Warn("graph relationship check failed; skipping candidate (fail-closed)",
				"content_id", cand.ContentID, "err", relErr)
			continue
		}
		if related {
			if chosen != nil {
				return nil, ErrNoWork
			}
			continue // anti-collusion: never assign a connected pair
		}
		a, claimErr := s.store.ClaimAndAssign(ctx, cand, r.ID, s.assignmentTTL)
		if errors.Is(claimErr, postgres.ErrAlreadyClaimed) {
			if chosen != nil {
				return nil, ErrNoWork
			}
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

// Decide records the reviewer's verdict. A reviewer either APPROVEs (the post is
// published — review_status flipped to approved) or ESCALATEs with comments to a
// super-admin. Either way the assignment completes and capped base pay accrues.
func (s *Service) Decide(ctx context.Context, userID, assignmentID uuid.UUID, decision, comments string) (*postgres.Assignment, error) {
	switch decision {
	case "approve", "escalate":
	default:
		return nil, ErrInvalidInput
	}
	if decision == "escalate" && strings.TrimSpace(comments) == "" {
		return nil, ErrInvalidInput // escalation must explain why
	}
	r, err := s.store.GetReviewerByUser(ctx, userID)
	if err != nil {
		return nil, ErrNotReviewer
	}
	a, err := s.store.Decide(ctx, assignmentID, r.ID, decision, comments)
	if err != nil {
		return nil, err
	}

	// Base pay accrual is durable + idempotent (local ledger).
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

	switch decision {
	case "approve":
		// Publish: flip flagged → approved. (Secondary/audit assignments don't
		// publish — they only cross-check the primary, handled below.)
		if a.Kind == "primary" {
			if err := s.clients.SetPostReviewStatus(ctx, a.ContentID, "approved"); err != nil {
				slog.Warn("publish-on-approve failed", "content", a.ContentID, "err", err)
			}
			s.onPrimaryDecided(a, decision)
		} else {
			s.onSecondaryDecided(a, decision)
		}
	case "escalate":
		// Hand off to the super-admin queue; the post stays flagged (hidden).
		if err := s.store.CreateEscalation(ctx, a.ContentID, a.CreatorID, r.ID, a.ID, comments); err != nil {
			slog.Error("create escalation failed", "content", a.ContentID, "err", err)
		}
		if a.Kind != "primary" {
			s.onSecondaryDecided(a, decision)
		}
	}

	a.CreatorID = uuid.Nil // keep creator hidden in the response
	return a, nil
}

// DashboardStats is the reviewer's console summary (reviewer may be nil if the
// user hasn't opted in yet).
type DashboardStats struct {
	Reviewer            *postgres.Reviewer `json:"reviewer"`
	ReviewsCompleted    int                `json:"reviews_completed"`
	Escalated           int                `json:"escalated"`
	LifetimeEarnedPaise int64              `json:"lifetime_earned_paise"`
	PendingQueue        int                `json:"pending_queue"`
}

// MyDashboard returns the reviewer's dashboard stats for the given user.
func (s *Service) MyDashboard(ctx context.Context, userID uuid.UUID) (*DashboardStats, error) {
	queue, _ := s.store.QueueDepth(ctx)
	out := &DashboardStats{PendingQueue: queue}
	r, err := s.store.GetReviewerByUser(ctx, userID)
	if err != nil {
		return out, nil // not a reviewer yet — reviewer stays nil
	}
	out.Reviewer = r
	if st, err := s.store.StatsForReviewer(ctx, r.ID); err == nil {
		out.ReviewsCompleted = st.ReviewsCompleted
		out.Escalated = st.Escalated
		out.LifetimeEarnedPaise = st.LifetimeEarnedPaise
	}
	return out, nil
}

// AdminDashboard returns the super-admin overview (open escalations + queue depth).
func (s *Service) AdminDashboard(ctx context.Context) (open, queue int) {
	open, _ = s.store.OpenEscalationCount(ctx)
	queue, _ = s.store.QueueDepth(ctx)
	return open, queue
}

// ListEscalations returns the open super-admin queue.
func (s *Service) ListEscalations(ctx context.Context, limit int) ([]postgres.Escalation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.store.ListOpenEscalations(ctx, limit)
}

// ResolveEscalation applies the super-admin decision and flips the post's
// review_status: reject→rejected, request_edits→needs_changes, approve→approved.
func (s *Service) ResolveEscalation(ctx context.Context, escalationID, adminID uuid.UUID, decision, notes string) (*postgres.Escalation, error) {
	target, ok := map[string]string{
		"reject":        "rejected",
		"request_edits": "needs_changes",
		"approve":       "approved",
	}[decision]
	if !ok {
		return nil, ErrInvalidInput
	}
	esc, err := s.store.ResolveEscalation(ctx, escalationID, adminID, decision, notes)
	if err != nil {
		return nil, err
	}
	if err := s.clients.SetPostReviewStatus(ctx, esc.ContentID, target); err != nil {
		slog.Warn("escalation post-status flip failed", "content", esc.ContentID, "target", target, "err", err)
	}
	return esc, nil
}

// CreatorFeedback returns the latest escalation outcome for a creator's own
// content (the "needs changes" comments the creator must act on).
func (s *Service) CreatorFeedback(ctx context.Context, creatorID, contentID uuid.UUID) (*postgres.Escalation, error) {
	return s.store.LatestEscalationForContent(ctx, creatorID, contentID)
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

func (s *Service) GetReviewerQueue(ctx context.Context, userID uuid.UUID) ([]postgres.QueueItem, error) {
	r, err := s.store.GetReviewerByUser(ctx, userID)
	if err != nil {
		return nil, ErrNotReviewer
	}
	if r.Status == "suspended" {
		return nil, ErrSuspended
	}
	return s.store.CandidateQueue(ctx, r.ID, r.Languages, s.rotationCapK, 50)
}
