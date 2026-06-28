// Package prefilter is the Phase 4 ML pre-filter seam: an automated classifier
// that decides whether flagged content needs a human at all. Clearly-bad content
// is auto-rejected and clearly-OK content is auto-approved, so humans only see
// the ambiguous middle — cutting reviewer hours sharply. The Classifier
// interface lets a real model (ai-service) replace the heuristic later.
package prefilter

import (
	"context"

	"github.com/google/uuid"
)

type Decision string

const (
	AutoApprove Decision = "auto_approve"
	AutoReject  Decision = "auto_reject"
	NeedsHuman  Decision = "needs_human"
)

// Input is the signal set available when content is flagged for review.
type Input struct {
	ContentID      uuid.UUID
	CreatorID      uuid.UUID
	ContentType    string
	SpamScore      float64 // 0..1 from post-service's spam detector
	ContentSeconds int
}

type Result struct {
	Decision   Decision
	Confidence float64
	Reason     string
}

// Classifier turns content signals into a routing decision.
type Classifier interface {
	Classify(ctx context.Context, in Input) (Result, error)
}

// HeuristicClassifier is the always-available baseline. It bands the spam score:
// very high → auto-reject, low end of the flagged range → auto-approve, middle →
// human. Conservative by design: the band defaults keep most content with humans.
type HeuristicClassifier struct {
	RejectAtOrAbove  float64 // spam score that auto-rejects (clearly bad)
	ApproveAtOrBelow float64 // spam score that auto-approves (low risk)
}

func NewHeuristic(rejectAt, approveBelow float64) HeuristicClassifier {
	if rejectAt <= 0 || rejectAt > 1 {
		rejectAt = 0.9
	}
	if approveBelow <= 0 || approveBelow >= rejectAt {
		approveBelow = 0.72
	}
	return HeuristicClassifier{RejectAtOrAbove: rejectAt, ApproveAtOrBelow: approveBelow}
}

func (h HeuristicClassifier) Classify(_ context.Context, in Input) (Result, error) {
	switch {
	case in.SpamScore >= h.RejectAtOrAbove:
		return Result{AutoReject, in.SpamScore, "spam score above reject band"}, nil
	case in.SpamScore > 0 && in.SpamScore <= h.ApproveAtOrBelow:
		return Result{AutoApprove, 1 - in.SpamScore, "spam score in low-risk band"}, nil
	default:
		return Result{NeedsHuman, 0.5, "ambiguous — routed to human"}, nil
	}
}
