package restriction

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Dispatcher drains trust.restriction_commands (plan section 9.2).
//
// Every sweep claims due rows with SKIP LOCKED (the claim leases the row
// and counts the attempt BEFORE the send, so a crash mid-send leaves a row
// that becomes due again on its own), signs each at send time and records
// the outcome:
//
//	2xx (incl. replayed)          acked; the case advances
//	5xx, 429, transport failure   retry: exponential backoff with jitter,
//	                              MinBackoff (30 s) doubling to MaxBackoff (30 min)
//	409 STALE_CASE_REVISION       superseded when a newer command exists for
//	                              the case; otherwise parked and escalated
//	403, 404, 409 DECISION_CONFLICT / SUBJECT_MISMATCH / STATE_MISMATCH,
//	any other 4xx                 parked with the reason; never retried blindly
//
// A parked or superseded row is terminal for the dispatcher. A command
// older than AckDeadline that is still pending is logged at error level
// on every sweep (the plan's "not acked within 1 h → page") and exposed
// as a gauge.
type Dispatcher struct {
	store   Store
	sender  Sender
	log     *slog.Logger
	metrics *Metrics
	kick    chan struct{}
	now     func() time.Time
	rand    func() float64

	// Interval between sweeps when nothing kicks the dispatcher.
	Interval time.Duration
	// BatchSize is the most rows one sweep claims.
	BatchSize int
	// Lease is how long a claimed row is held before it is due again.
	Lease time.Duration
	// MinBackoff and MaxBackoff bound the retry delay.
	MinBackoff, MaxBackoff time.Duration
	// AckDeadline is how long a command may stay pending before it is
	// escalated in the log.
	AckDeadline time.Duration
}

// Store is the dispatcher's view of postgres.CopyrightStore.
type Store interface {
	ClaimPendingCommands(ctx context.Context, limit int, lease time.Duration) ([]postgres.RestrictionCommand, error)
	MarkCommandAcked(ctx context.Context, decisionID uuid.UUID, statusCode int, replayed bool, result json.RawMessage) error
	RecordCommandRetry(ctx context.Context, decisionID uuid.UUID, statusCode int, errorCode, cause string, retryAfter time.Duration) error
	MarkCommandSuperseded(ctx context.Context, decisionID uuid.UUID, statusCode int, errorCode, cause string) (bool, error)
	ParkCommand(ctx context.Context, decisionID uuid.UUID, reason string, statusCode int, errorCode, cause string) error
	OldestPendingCommandAge(ctx context.Context) (time.Duration, error)
	CountParkedCommands(ctx context.Context) (int, error)
}

// Sender sends one command (the Client).
type Sender interface {
	Send(ctx context.Context, cmd Command) Outcome
}

// Park reasons written to trust.restriction_commands.park_reason.
const (
	ParkReasonRefused          = "refused"            // 403 / 404 / 400 / 401 / unexpected
	ParkReasonDecisionConflict = "decision_conflict"  // 409 DECISION_CONFLICT
	ParkReasonSubjectMismatch  = "subject_mismatch"   // 409 SUBJECT_MISMATCH
	ParkReasonStateMismatch    = "state_mismatch"     // 409 STATE_MISMATCH
	ParkReasonInvalidClaims    = "invalid_claims"     // 422 INVALID_CLAIMS / signer refused
	ParkReasonStaleNoSuccessor = "stale_no_successor" // 409 STALE_CASE_REVISION with no newer command
)

// NewDispatcher builds a dispatcher with the plan's defaults: 5 s sweeps
// (or sooner on Kick), 50 rows, 60 s lease, 30 s..30 min backoff, 1 h ack
// deadline.
func NewDispatcher(store Store, sender Sender, log *slog.Logger, m *Metrics) *Dispatcher {
	if log == nil {
		log = slog.Default()
	}
	return &Dispatcher{
		store: store, sender: sender, log: log, metrics: m,
		kick: make(chan struct{}, 1), now: time.Now, rand: rand.Float64,
		Interval: 5 * time.Second, BatchSize: 50, Lease: time.Minute,
		MinBackoff: 30 * time.Second, MaxBackoff: 30 * time.Minute, AckDeadline: time.Hour,
	}
}

// Kick asks for a sweep now (a transition just committed). Never blocks.
func (d *Dispatcher) Kick() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// Run sweeps until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	d.log.Info("restriction command dispatcher started", "interval", d.Interval)
	ticker := time.NewTicker(d.Interval)
	defer ticker.Stop()
	for {
		if _, err := d.Sweep(ctx); err != nil && ctx.Err() == nil {
			d.log.Error("restriction command sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			d.log.Info("restriction command dispatcher stopped")
			return
		case <-ticker.C:
		case <-d.kick:
		}
	}
}

// Sweep claims and sends one batch. It returns how many commands were
// acked; the error is a store failure (a send failure is recorded on the
// row). It keeps going through the batch: rows are independent once the
// per-case ordering has been applied by the claim.
func (d *Dispatcher) Sweep(ctx context.Context) (int, error) {
	rows, err := d.store.ClaimPendingCommands(ctx, d.BatchSize, d.Lease)
	if err != nil {
		return 0, err
	}
	acked := 0
	for _, row := range rows {
		if ctx.Err() != nil {
			return acked, ctx.Err()
		}
		ok, err := d.dispatch(ctx, row)
		if err != nil {
			return acked, err
		}
		if ok {
			acked++
		}
	}
	d.observeQueue(ctx)
	return acked, nil
}

// dispatch sends one row and records its outcome. It returns true when the
// row was acked; the error is a store failure.
func (d *Dispatcher) dispatch(ctx context.Context, row postgres.RestrictionCommand) (bool, error) {
	cmd := Command{
		DecisionID: row.DecisionID, CaseID: row.CaseID, CaseRevision: row.CaseRevision,
		Action: row.Action, Source: row.Source, SubjectPostID: row.SubjectPostID, SubjectAuthorID: row.SubjectAuthorID,
		ExpectedState: row.ExpectedState, ReasonCode: row.ReasonCode, PolicyVersion: row.PolicyVersion, ActorID: row.ActorID,
	}
	out := d.sender.Send(ctx, cmd)
	cause := ""
	if out.Err != nil {
		cause = out.Err.Error()
	}
	attrs := []any{"decision_id", row.DecisionID, "case_id", row.CaseID, "case_revision", row.CaseRevision,
		"action", row.Action, "attempt", row.Attempts, "status", out.StatusCode, "code", out.ErrorCode}
	d.metrics.commandOutcome(out.Disposition)
	switch out.Disposition {
	case DispositionAcked:
		if err := d.store.MarkCommandAcked(ctx, row.DecisionID, out.StatusCode, out.Replayed, out.Body); err != nil {
			if errors.Is(err, postgres.ErrRestrictionCommandNotPending) {
				// Another dispatcher recorded the outcome first (the lease
				// had lapsed). post-service replayed; nothing is lost.
				d.log.Warn("restriction command: acked by another dispatcher", attrs...)
				return false, nil
			}
			return false, err
		}
		d.log.Info("restriction command acked", append(attrs, "replayed", out.Replayed)...)
		return true, nil
	case DispositionRetry:
		wait := d.backoff(row.Attempts)
		if err := d.store.RecordCommandRetry(ctx, row.DecisionID, out.StatusCode, out.ErrorCode, cause, wait); err != nil && !errors.Is(err, postgres.ErrRestrictionCommandNotPending) {
			return false, err
		}
		d.log.Warn("restriction command: transient failure; will retry", append(attrs, "retry_after", wait, "error", cause)...)
		return false, nil
	case DispositionSuperseded:
		superseded, err := d.store.MarkCommandSuperseded(ctx, row.DecisionID, out.StatusCode, out.ErrorCode, cause)
		if err != nil {
			return false, err
		}
		if superseded {
			d.log.Info("restriction command superseded by a newer command", attrs...)
			return false, nil
		}
		// post-service holds a revision this side does not know about.
		// Nothing here can be retried; a human must look.
		d.metrics.escalation("stale_no_successor")
		d.log.Error("restriction command: STALE_CASE_REVISION with no newer command; parked (escalate)", attrs...)
		return false, d.park(ctx, row.DecisionID, ParkReasonStaleNoSuccessor, out, cause)
	default:
		reason := parkReason(out)
		d.metrics.escalation(reason)
		d.log.Error("restriction command refused; parked (escalate)", append(attrs, "park_reason", reason, "error", cause)...)
		return false, d.park(ctx, row.DecisionID, reason, out, cause)
	}
}

func (d *Dispatcher) park(ctx context.Context, id uuid.UUID, reason string, out Outcome, cause string) error {
	err := d.store.ParkCommand(ctx, id, reason, out.StatusCode, out.ErrorCode, cause)
	if errors.Is(err, postgres.ErrRestrictionCommandNotPending) {
		return nil
	}
	return err
}

func parkReason(out Outcome) string {
	switch out.ErrorCode {
	case CodeDecisionConflict:
		return ParkReasonDecisionConflict
	case CodeSubjectMismatch:
		return ParkReasonSubjectMismatch
	case CodeStateMismatch:
		return ParkReasonStateMismatch
	case CodeInvalidClaims, "SIGNER_REFUSED":
		return ParkReasonInvalidClaims
	}
	return ParkReasonRefused
}

// backoff is MinBackoff·2^(attempts-1) with ±20 % jitter, capped at
// MaxBackoff. attempts counts the attempt that just failed (≥ 1).
func (d *Dispatcher) backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	exp := attempts - 1
	if exp > 16 {
		exp = 16
	}
	wait := d.MinBackoff << uint(exp)
	if wait > d.MaxBackoff || wait <= 0 {
		wait = d.MaxBackoff
	}
	jitter := time.Duration(float64(wait) * 0.2 * (2*d.rand() - 1))
	wait += jitter
	if wait < d.MinBackoff/2 {
		wait = d.MinBackoff / 2
	}
	if wait > d.MaxBackoff {
		wait = d.MaxBackoff
	}
	return wait
}

// observeQueue updates the queue gauges and escalates an overdue queue.
func (d *Dispatcher) observeQueue(ctx context.Context) {
	if age, err := d.store.OldestPendingCommandAge(ctx); err == nil {
		d.metrics.oldestPending(age)
		if d.AckDeadline > 0 && age > d.AckDeadline {
			d.log.Error("restriction command not acked within the deadline (page)", "oldest_pending", age, "deadline", d.AckDeadline)
		}
	}
	if n, err := d.store.CountParkedCommands(ctx); err == nil {
		d.metrics.parked(n)
	}
}
