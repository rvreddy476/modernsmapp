package identityroles

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Worker drains the intent queue into identity.
//
// # WHY DELIVERY IS ALWAYS ASYNCHRONOUS
//
// The obvious design is "call identity inline, and fall back to the queue if
// the call fails". It was rejected. It gives you two delivery paths to reason
// about, two places a bug can hide, and an ordering hazard: an inline revoke
// that succeeds can overtake a queued grant for the same user and leave
// identity holding the opposite of the truth. One path, always the queue,
// means the queue's `ORDER BY id` is the only ordering that exists, and a
// service's approval endpoint never waits on identity at all — its latency and
// its success no longer depend on another service being up.
//
// The cost is that a grant is eventually consistent. In the normal case
// "eventually" is one poll interval: a second. The user's token picks the role
// up on its next mint anyway (refresh, or a 15-minute access-token expiry), so
// a sub-second queue hop is far inside the noise of the existing propagation
// delay.
//
// # FAILURE MODES, STATED PLAINLY
//
//  1. identity is down. Intents accumulate; every one is retried with
//     exponential backoff up to MaxAttempts. Roles arrive late. Nothing is
//     lost, because the intent committed with the domain write.
//  2. identity is down for longer than MaxAttempts of backoff (~17 minutes at
//     the defaults). The intent is DEAD-LETTERED, not dropped: the row stays,
//     with last_error and dead_lettered_at set, and Stats().DeadLetter is
//     non-zero. It will never be retried automatically. Someone must look, and
//     tools/identityrolebackfill re-enqueues it once the cause is fixed. This
//     is the failure mode that needs an alert on it, and there is not one yet
//     — see the handover note in the report.
//  3. The internal service key is wrong. Every intent dead-letters
//     immediately with ErrUnauthorized. Loud and fast, by design: a slow
//     retry loop would hide a total outage of role granting behind a backlog
//     graph.
//  4. The process crashes between a non-transactional domain write and
//     EnqueuePool. The intent never existed. Only the reconciliation pass
//     finds this, which is why the backfill tool is also a reconciler.
type Worker struct {
	client  *Client
	outbox  *Outbox
	db      *pgxpool.Pool
	log     *slog.Logger
	poll    time.Duration
	batch   int
	maxTry  int
	baseOff time.Duration
	maxOff  time.Duration
}

// WorkerConfig tunes the Worker. Zero values take the documented defaults.
type WorkerConfig struct {
	// PollInterval between sweeps. Default 1s.
	PollInterval time.Duration
	// BatchSize is the max intents claimed per sweep. Default 50.
	BatchSize int
	// MaxAttempts before dead-lettering. Default 10, which with the default
	// backoff is roughly 17 minutes of trying.
	MaxAttempts int
	// BaseBackoff is the first retry delay. Default 2s, doubling.
	BaseBackoff time.Duration
	// MaxBackoff caps the delay. Default 5m.
	MaxBackoff time.Duration
	// Logger. Defaults to slog.Default().
}

// NewWorker builds a Worker. A nil client, outbox or db yields a nil Worker,
// so a service that has not configured identity can call Start unconditionally
// and get a no-op rather than a panic — the same degrade-quietly posture the
// three services already use for their optional dependencies.
func NewWorker(client *Client, ob *Outbox, db *pgxpool.Pool, log *slog.Logger, cfg WorkerConfig) *Worker {
	if client == nil || ob == nil || db == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 10
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 2 * time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 5 * time.Minute
	}
	return &Worker{
		client: client, outbox: ob, db: db, log: log,
		poll: cfg.PollInterval, batch: cfg.BatchSize, maxTry: cfg.MaxAttempts,
		baseOff: cfg.BaseBackoff, maxOff: cfg.MaxBackoff,
	}
}

// Run sweeps until ctx is cancelled. Call it in a goroutine.
func (w *Worker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	w.log.Info("identity role worker started", "poll", w.poll, "batch", w.batch, "max_attempts", w.maxTry)
	t := time.NewTicker(w.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.log.Info("identity role worker stopped")
			return
		case <-t.C:
			if n, err := w.Sweep(ctx); err != nil {
				w.log.Warn("identity role sweep failed", "err", err)
			} else if n > 0 {
				w.log.Debug("identity role sweep", "handled", n)
			}
		}
	}
}

// Sweep processes one batch and returns how many intents it settled. Exported
// so a test — or a one-shot command — can drain the queue without a ticker.
func (w *Worker) Sweep(ctx context.Context) (int, error) {
	if w == nil {
		return 0, nil
	}
	tx, intents, err := w.outbox.Claim(ctx, w.db, w.batch)
	if err != nil {
		return 0, err
	}
	// The claim transaction is held for the duration of the HTTP calls. That
	// is intentional and bounded: BatchSize * client timeout is the worst
	// case, 50 * 5s at the defaults, and SKIP LOCKED means no other worker is
	// blocked meanwhile. Committing per-intent instead would need a
	// transaction per row and would lose the SKIP LOCKED lease.
	defer func() { _ = tx.Rollback(ctx) }()

	for _, in := range intents {
		err := w.deliver(ctx, in)
		switch {
		case err == nil:
			if e := w.outbox.MarkDelivered(ctx, tx, in.ID); e != nil {
				return 0, e
			}
			w.log.Info("identity role delivered",
				"op", in.Op, "user_id", in.UserID, "role", in.Role, "attempts", in.Attempts)

		case IsPermanent(err):
			if e := w.outbox.MarkDead(ctx, tx, in.ID, err.Error()); e != nil {
				return 0, e
			}
			// ERROR, not warn: a permanent failure means this person will
			// never get the role and no retry will change that.
			w.log.Error("identity role intent dead-lettered (permanent)",
				"op", in.Op, "user_id", in.UserID, "role", in.Role, "err", err)

		case in.Attempts+1 >= w.maxTry:
			if e := w.outbox.MarkDead(ctx, tx, in.ID, err.Error()); e != nil {
				return 0, e
			}
			w.log.Error("identity role intent dead-lettered (attempts exhausted)",
				"op", in.Op, "user_id", in.UserID, "role", in.Role,
				"attempts", in.Attempts+1, "err", err)

		default:
			d := w.backoff(in.Attempts)
			if e := w.outbox.MarkRetry(ctx, tx, in.ID, d, err.Error()); e != nil {
				return 0, e
			}
			w.log.Warn("identity role intent deferred",
				"op", in.Op, "user_id", in.UserID, "role", in.Role,
				"attempts", in.Attempts+1, "retry_in", d, "err", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(intents), nil
}

func (w *Worker) deliver(ctx context.Context, in Intent) error {
	switch in.Op {
	case OpGrant:
		return w.client.Grant(ctx, in.UserID, in.Role, in.Reason)
	case OpRevoke:
		return w.client.Revoke(ctx, in.UserID, in.Role, in.Reason)
	default:
		// Cannot happen — the CHECK constraint and Intent.validate both
		// forbid it — but a bad row must dead-letter rather than loop.
		return errors.New("identityroles: unknown op " + string(in.Op))
	}
}

// backoff is base * 2^attempts, capped. No jitter: the queue is per-service
// and small, and the SKIP LOCKED lease already staggers replicas, so the
// thundering-herd this would protect against does not arise.
func (w *Worker) backoff(attempts int) time.Duration {
	d := w.baseOff
	for i := 0; i < attempts && d < w.maxOff; i++ {
		d *= 2
	}
	if d > w.maxOff {
		d = w.maxOff
	}
	return d
}
