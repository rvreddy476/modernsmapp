package service

import (
	"context"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// exclusiveRunner is the store's single-replica runner (postgres.Store
// RunExclusive). A store without it (unit-test fakes) runs the work directly.
type exclusiveRunner interface {
	RunExclusive(ctx context.Context, name string, fn func(context.Context) error) (bool, error)
}

// runExclusive runs fn on one replica at a time and reports whether it ran
// here (audit 2026-10-30 L3, L4, L6).
func (s *Service) runExclusive(ctx context.Context, name string, fn func(context.Context) error) (bool, error) {
	if r, ok := s.store.(exclusiveRunner); ok {
		return r.RunExclusive(ctx, name, fn)
	}
	return true, fn(ctx)
}

// fraudPassRecorder reports when the fraud worker last wrote a score.
type fraudPassRecorder interface {
	LatestFraudScoreAt(ctx context.Context) (*time.Time, error)
}

// Worker lock names.
const (
	workerDispatch = "delivery-dispatch"
	workerFraud    = "fraud-score"
)

// fraudBootPassDue reports whether a booting replica should run the fraud
// pass now: only when no pass wrote a score within one interval (an empty
// table on a fresh environment, or every replica down for a while). An
// ordinary scale-out or restart leaves the pass to the ticker.
func fraudBootPassDue(last *time.Time, now time.Time, interval time.Duration) bool {
	return last == nil || now.Sub(*last) >= interval
}

// silentRiderReleaser is the store's silent-rider release (audit L4).
type silentRiderReleaser interface {
	ReleaseSilentRiderJobs(ctx context.Context, silence time.Duration, limit int) ([]uuid.UUID, error)
}

// ReleaseSilentRiderJobs returns to dispatch the pre-pickup jobs of riders
// silent for postgres.SilentRiderJobTimeout; the dispatch worker then offers
// them again. It returns how many were released.
func (s *Service) ReleaseSilentRiderJobs(ctx context.Context) (int, error) {
	r, ok := s.store.(silentRiderReleaser)
	if !ok {
		return 0, nil
	}
	ids, err := r.ReleaseSilentRiderJobs(ctx, postgres.SilentRiderJobTimeout, 50)
	return len(ids), err
}
