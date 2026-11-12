package workers

// Momentum Premium workers (migration 027). They run when PREMIUM_ENABLED is
// on (runmode.Mode.PremiumEnabled), and NONE of them charges anyone: renewal
// is a reminder, the buyer taps Pay.
//
//   - role sync: tells identity what each buyer holds — `premium` with
//     expires_at = paid end + 3 days of grace after a pass is paid or
//     stacked, a shorter expiry after a refund, a revoke once access has
//     ended — and retries with backoff until identity agrees;
//   - reminders: premium.renewal_due 3 days before the paid end and on the
//     day, once each per end;
//   - ended: premium.ended once per access that ended;
//   - checkout sweep: a checkout unpaid for 30 minutes → abandoned.
//   - document outbox: premium.invoice.issued / premium.credit_note.issued
//     for every tax invoice or credit note whose post-commit publish failed.

import (
	"context"
	"log/slog"
	"time"
)

// Pass intervals.
const (
	premiumRolePassInterval = time.Minute
	premiumPassInterval     = 5 * time.Minute
)

// premiumRunner is the slice of *service.Premium the workers drive.
type premiumRunner interface {
	SyncRoles(ctx context.Context) (int, error)
	SendReminders(ctx context.Context) (int, error)
	SendEndedNotices(ctx context.Context) (int, error)
	SweepAbandonedCheckouts(ctx context.Context) (int64, error)
	PublishPendingDocuments(ctx context.Context) (int, error)
}

// StartPremiumWorkers launches the premium workers and blocks until ctx ends.
// Call it in a goroutine.
func StartPremiumWorkers(ctx context.Context, p premiumRunner) {
	slog.Info("starting premium workers (role sync, renewal reminders, ended notices, checkout sweep)")
	go every(ctx, premiumRolePassInterval, func(time.Time) { runPremiumRolePass(ctx, p) })
	go every(ctx, premiumPassInterval, func(time.Time) { runPremiumPass(ctx, p) })
	<-ctx.Done()
	slog.Info("premium workers stopped")
}

func runPremiumRolePass(ctx context.Context, p premiumRunner) {
	if n, err := p.SyncRoles(ctx); err != nil {
		slog.Error("premium worker: role sync pass failed", "error", err)
	} else if n > 0 {
		slog.Info("premium worker: roles synced", "count", n)
	}
	if n, err := p.PublishPendingDocuments(ctx); err != nil {
		slog.Error("premium worker: document outbox pass failed", "error", err)
	} else if n > 0 {
		slog.Info("premium worker: document events retried", "count", n)
	}
}

// runPremiumPass is one pass, in order: sweep, remind, announce endings.
func runPremiumPass(ctx context.Context, p premiumRunner) {
	if n, err := p.SweepAbandonedCheckouts(ctx); err != nil {
		slog.Error("premium worker: checkout sweep failed", "error", err)
	} else if n > 0 {
		slog.Info("premium worker: stale checkouts abandoned", "count", n)
	}
	if _, err := p.SendReminders(ctx); err != nil {
		slog.Error("premium worker: reminder pass failed", "error", err)
	}
	if _, err := p.SendEndedNotices(ctx); err != nil {
		slog.Error("premium worker: ended pass failed", "error", err)
	}
}
