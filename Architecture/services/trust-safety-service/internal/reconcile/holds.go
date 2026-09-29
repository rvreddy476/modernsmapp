package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/trust-safety-service/internal/restriction"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// HoldReconciler is the plan's section 9.5 "hold reconciliation": every
// HoldInterval it compares the hold state each open case expects with the
// restriction rows post-service holds for it (GET
// /v1/posts/internal/restrictions?source=copyright&case_ids=…) and:
//
//   - a hold_active case whose last command is acked but whose row is
//     missing, released, or behind the case revision → drift
//     (missing_hold / stale_revision); the RECORDED decision is re-queued,
//     never a new one;
//   - a hold_released case whose last command is acked but whose row is
//     still active → drift (unexpected_active); the recorded release is
//     re-queued. The sweep never releases anything a case says is active,
//     and it never invents a command: a case with no command at all is
//     logged and left alone;
//   - a case whose last command is parked → drift (parked), logged; a
//     human owns it;
//   - a case whose last command is pending for longer than PendingDeadline
//     → drift (pending_overdue), logged; the dispatcher owns it.
//
// Every finding is logged and counted on the metrics; the sweep's own
// failures (post-service unreachable, the read refused) are logged and
// counted as a failed run, and the next tick tries again.
type HoldReconciler struct {
	store   HoldStore
	reader  HoldReader
	metrics *restriction.Metrics
	kick    func()
	log     *slog.Logger
	now     func() time.Time

	// Interval between sweeps (HoldInterval).
	Interval time.Duration
	// InitialDelay before the first sweep after boot.
	InitialDelay time.Duration
	// ReleasedWindow: released cases are verified for this long after the
	// release, then left alone.
	ReleasedWindow time.Duration
	// PendingDeadline: a command pending longer than this is drift.
	PendingDeadline time.Duration
	// PageSize is the number of cases per post-service read (≤ 100).
	PageSize int
}

// HoldInterval is the plan's cadence for the incremental sweep.
const HoldInterval = 10 * time.Minute

// HoldStore is the reconciler's view of postgres.CopyrightStore.
type HoldStore interface {
	ListCasesForReconcile(ctx context.Context, after postgres.ReconcilePage, limit int, releasedWindow time.Duration) ([]postgres.ReconcileCase, postgres.ReconcilePage, error)
	RequeueAckedCommand(ctx context.Context, decisionID uuid.UUID) (bool, error)
}

// HoldReader reads post-service's rows (restriction.Client).
type HoldReader interface {
	ListByCase(ctx context.Context, caseIDs []uuid.UUID) ([]restriction.Row, error)
}

// Drift kinds, as counted and logged.
const (
	DriftMissingHold      = "missing_hold"
	DriftStaleRevision    = "stale_revision"
	DriftUnexpectedActive = "unexpected_active"
	DriftMissingRow       = "missing_row"
	DriftParked           = "parked"
	DriftPendingOverdue   = "pending_overdue"
	DriftNoCommand        = "no_command"
)

// NewHoldReconciler builds the sweep with the plan's defaults.
func NewHoldReconciler(store HoldStore, reader HoldReader, m *restriction.Metrics, kick func(), log *slog.Logger) *HoldReconciler {
	if log == nil {
		log = slog.Default()
	}
	return &HoldReconciler{
		store: store, reader: reader, metrics: m, kick: kick, log: log, now: time.Now,
		Interval: HoldInterval, InitialDelay: 2 * time.Minute, ReleasedWindow: 24 * time.Hour,
		PendingDeadline: time.Hour, PageSize: 100,
	}
}

// Start runs the sweep on a ticker until ctx is cancelled.
func (r *HoldReconciler) Start(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(r.InitialDelay):
	}
	r.run(ctx)
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.run(ctx)
		}
	}
}

func (r *HoldReconciler) run(ctx context.Context) {
	rep, err := r.Reconcile(ctx)
	switch {
	case err != nil:
		r.metrics.ReconcileRun("failed")
		r.log.Error("copyright hold reconciliation failed", "error", err, "checked", rep.Checked)
	case rep.Drift() > 0:
		r.metrics.ReconcileRun("drift")
		r.log.Warn("copyright hold reconciliation found drift", "checked", rep.Checked, "findings", rep.Findings, "requeued", rep.Requeued)
	default:
		r.metrics.ReconcileRun("ok")
		r.log.Info("copyright hold reconciliation consistent", "checked", rep.Checked)
	}
}

// Report is one sweep's tally.
type Report struct {
	Checked  int
	Findings map[string]int
	Requeued int
}

// Drift is the number of findings of every kind.
func (r Report) Drift() int {
	n := 0
	for _, v := range r.Findings {
		n += v
	}
	return n
}

// Reconcile runs one full pass over the open cases.
func (r *HoldReconciler) Reconcile(ctx context.Context) (Report, error) {
	rep := Report{Findings: map[string]int{}}
	var page postgres.ReconcilePage
	for {
		cases, next, err := r.store.ListCasesForReconcile(ctx, page, r.PageSize, r.ReleasedWindow)
		if err != nil {
			return rep, err
		}
		if len(cases) == 0 {
			return rep, nil
		}
		ids := make([]uuid.UUID, 0, len(cases))
		for _, c := range cases {
			ids = append(ids, c.Case.ID)
		}
		rows, err := r.reader.ListByCase(ctx, ids)
		if err != nil {
			return rep, err
		}
		byCase := map[uuid.UUID]restriction.Row{}
		for _, row := range rows {
			// One row per (post, case): the row is keyed by case here
			// because a case names exactly one post.
			if row.PostID == uuid.Nil {
				continue
			}
			byCase[row.CaseID] = row
		}
		for _, c := range cases {
			rep.Checked++
			row, found := byCase[c.Case.ID]
			if found && row.PostID != c.Case.SubjectPostID {
				// post-service's row names another post for this case: that
				// is not drift this side can repair.
				r.finding(&rep, DriftMissingRow, c, "row names another post", "row_post_id", row.PostID)
				continue
			}
			r.compare(ctx, &rep, c, row, found)
		}
		page = next
	}
}

func (r *HoldReconciler) finding(rep *Report, kind string, c postgres.ReconcileCase, msg string, extra ...any) {
	rep.Findings[kind]++
	r.metrics.Drift(kind)
	attrs := append([]any{"kind", kind, "case_id", c.Case.ID, "post_id", c.Case.SubjectPostID,
		"case_state", c.Case.State, "case_revision", c.Case.CaseRevision}, extra...)
	if c.Command != nil {
		attrs = append(attrs, "decision_id", c.Command.DecisionID, "command_status", c.Command.Status, "command_revision", c.Command.CaseRevision)
	}
	r.log.Error("copyright hold drift: "+msg, attrs...)
}

// compare judges one case against the row post-service holds for it.
func (r *HoldReconciler) compare(ctx context.Context, rep *Report, c postgres.ReconcileCase, row restriction.Row, found bool) {
	k := c.Command
	if k == nil {
		r.finding(rep, DriftNoCommand, c, "case has no command")
		return
	}
	switch k.Status {
	case postgres.RestrictionCommandParked:
		r.finding(rep, DriftParked, c, "last command is parked; a human must resolve it", "park_reason", deref(k.ParkReason))
		return
	case postgres.RestrictionCommandPending:
		if r.now().Sub(k.CreatedAt) > r.PendingDeadline {
			r.finding(rep, DriftPendingOverdue, c, "last command pending past the deadline", "pending_for", r.now().Sub(k.CreatedAt))
		}
		return
	case postgres.RestrictionCommandSuperseded:
		// The newest command is never superseded (only an older one is);
		// treat like no command rather than guess.
		r.finding(rep, DriftNoCommand, c, "latest command is superseded")
		return
	}
	// Acked: post-service should reflect the command.
	switch c.Case.State {
	case postgres.CopyrightCaseHoldActive:
		switch {
		case !found:
			r.finding(rep, DriftMissingHold, c, "case is hold_active but post-service has no row")
			r.requeue(ctx, rep, k.DecisionID)
		case row.State != "active":
			r.finding(rep, DriftMissingHold, c, "case is hold_active but post-service's row is not active", "row_state", row.State, "row_revision", row.CaseRevision)
			r.requeue(ctx, rep, k.DecisionID)
		case row.CaseRevision < k.CaseRevision:
			r.finding(rep, DriftStaleRevision, c, "post-service's row is behind the enforced revision", "row_revision", row.CaseRevision)
			r.requeue(ctx, rep, k.DecisionID)
		}
	case postgres.CopyrightCaseHoldReleased:
		switch {
		case !found:
			// A release with no row: the place never landed either. Nothing
			// to send; the record is enough.
			r.finding(rep, DriftMissingRow, c, "case is hold_released but post-service has no row")
		case row.State == "active":
			r.finding(rep, DriftUnexpectedActive, c, "case is hold_released but post-service still holds the post", "row_revision", row.CaseRevision)
			r.requeue(ctx, rep, k.DecisionID)
		case row.CaseRevision < k.CaseRevision:
			r.finding(rep, DriftStaleRevision, c, "post-service's released row is behind the enforced revision", "row_revision", row.CaseRevision)
			r.requeue(ctx, rep, k.DecisionID)
		}
	}
}

// requeue re-enqueues the recorded decision (only an acked command moves).
func (r *HoldReconciler) requeue(ctx context.Context, rep *Report, id uuid.UUID) {
	ok, err := r.store.RequeueAckedCommand(ctx, id)
	if err != nil {
		r.log.Error("copyright hold drift: requeue failed", "decision_id", id, "error", err)
		return
	}
	if ok {
		rep.Requeued++
		if r.kick != nil {
			r.kick()
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
