package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Security sweep #23 (21 Oct 2026): the one-active-report index stops a
// reporter repeating themselves, but nothing bounded how MANY distinct
// reports one account could file — hundreds a minute would flood the
// moderation queue. A reporter may file ReporterReportLimit reports per
// ReporterReportWindow through the user-facing POST /v1/reports; the next
// one is refused with ErrReportRateLimited (429) and nothing is written.
//
// The count is a preflight read, so two replicas racing can each admit the
// last slot: the cap can overshoot by the number of concurrent requests,
// which is fine for a volume bound (the duplicate rule, which must be
// exact, stays the database's partial unique index).
const (
	ReporterReportLimit  = 20
	ReporterReportWindow = time.Hour
)

// ErrReportRateLimited: the reporter has used their quota for the window.
var ErrReportRateLimited = errors.New("report rate limit exceeded")

// reporterVolume counts a reporter's recent reports (*postgres.ReportStore
// in production; a fake in tests).
type reporterVolume interface {
	CountReportsByReporterSince(ctx context.Context, reporterID uuid.UUID, since time.Time) (int, error)
}

// WithReporterVolume overrides the counter the quota reads (tests).
func (s *Service) WithReporterVolume(v reporterVolume) *Service {
	s.reporterVolume = v
	return s
}

// checkReporterQuota refuses when reporterID filed ReporterReportLimit or
// more reports in the last ReporterReportWindow. A failed count refuses too:
// an unanswered "how many?" is not a "few".
func (s *Service) checkReporterQuota(ctx context.Context, reporterID uuid.UUID) error {
	var counter reporterVolume = s.store
	if s.reporterVolume != nil {
		counter = s.reporterVolume
	}
	n, err := counter.CountReportsByReporterSince(ctx, reporterID, time.Now().Add(-ReporterReportWindow))
	if err != nil {
		return fmt.Errorf("report quota: %w", err)
	}
	if n >= ReporterReportLimit {
		return ErrReportRateLimited
	}
	return nil
}
