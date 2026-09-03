// Package purge is the auth-side orchestrator for permanent account deletion.
//
// A DELETE /v1/auth/account marks the row pending_deletion with a purge date
// 30 days out and emits nothing irreversible. This worker is the ONLY thing
// that escalates: once the date passes it emits user.purge_requested (at most
// once per 24h per user) and waits for every required service to erase its
// slice and ack onto the purge-acks topic. Only when the ack set covers
// REQUIRED_PURGE_SERVICES does it anonymise the credential row and emit
// user.purged. It NEVER purges on partial acks — a partial purge is
// irreversible erasure of some data with the rest retained, which is the
// exact outcome this pipeline exists to prevent.
//
// Per-service purge consumers are a separate workstream. Until they exist the
// worker re-requests every 24h and warns about overdue users, and no account
// is ever marked purged. That is the safe direction.
package purge

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// Store is the narrow surface the worker needs. *store.Store satisfies it.
type Store interface {
	ListPurgeDue(ctx context.Context, limit int) ([]store.PurgeCandidate, error)
	RequestPurge(ctx context.Context, userID uuid.UUID) (bool, error)
	GetPurgeAcks(ctx context.Context, userID uuid.UUID) (map[string]struct{}, error)
	CompletePurge(ctx context.Context, userID uuid.UUID) error
}

// DefaultRequiredServices is every service that holds user data and must
// confirm erasure before auth anonymises the credential row. Names are the
// ack `service` strings each consumer emits: the identity user-service acks
// as "user", the Architecture user-service (wellbeing, pages, portfolio) as
// "user-extras", the two live services as "live" and "live-v2". Leaving one
// out means a purge can complete while that slice still holds data.
const DefaultRequiredServices = "graph,post,feed,user,user-extras,profile,message,call,notification,media,search,live,live-v2,trust-safety,dating"

const (
	// DefaultTickInterval — how often the worker scans for due accounts.
	DefaultTickInterval = 5 * time.Minute
	// DefaultAcksTopic — where services publish {"user_id","service","purged_at"}.
	DefaultAcksTopic = "platform.purge-acks.v1"
	// overdueWarnAfter — a user whose purge date is this far past and still
	// unacked gets a WARN naming the missing services.
	overdueWarnAfter = 7 * 24 * time.Hour
	batchSize        = 200
)

// Config is the env-driven tuning.
type Config struct {
	TickInterval     time.Duration
	RequiredServices []string
	AcksTopic        string
	// AcksGroupID is the Kafka consumer group for the acks consumer.
	AcksGroupID string
}

// ConfigFromEnv reads PURGE_TICK_INTERVAL, REQUIRED_PURGE_SERVICES,
// PURGE_ACKS_TOPIC and PURGE_ACKS_GROUP_ID with the documented defaults.
func ConfigFromEnv() Config {
	cfg := Config{
		TickInterval:     DefaultTickInterval,
		RequiredServices: ParseRequiredServices(os.Getenv("REQUIRED_PURGE_SERVICES")),
		AcksTopic:        DefaultAcksTopic,
		AcksGroupID:      "identity-auth.purge-acks",
	}
	if v := os.Getenv("PURGE_TICK_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.TickInterval = d
		}
	}
	if v := strings.TrimSpace(os.Getenv("PURGE_ACKS_TOPIC")); v != "" {
		cfg.AcksTopic = v
	}
	if v := strings.TrimSpace(os.Getenv("PURGE_ACKS_GROUP_ID")); v != "" {
		cfg.AcksGroupID = v
	}
	return cfg
}

// ParseRequiredServices parses a comma list; empty falls back to the default
// set. An operator cannot accidentally configure "no services required" and
// have every purge complete instantly — that would need an explicit code
// change, not an empty env var.
func ParseRequiredServices(v string) []string {
	if strings.TrimSpace(v) == "" {
		v = DefaultRequiredServices
	}
	seen := map[string]struct{}{}
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(strings.ToLower(p))
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Worker drives the purge state machine on a ticker.
type Worker struct {
	store    Store
	log      *slog.Logger
	interval time.Duration
	required []string
	now      func() time.Time
}

// NewWorker builds a worker. Required services and interval come from cfg;
// an empty required list is replaced by the default set (see
// ParseRequiredServices) so a purge can never complete with zero acks.
func NewWorker(s Store, logger *slog.Logger, cfg Config) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	required := cfg.RequiredServices
	if len(required) == 0 {
		required = ParseRequiredServices("")
	}
	interval := cfg.TickInterval
	if interval <= 0 {
		interval = DefaultTickInterval
	}
	return &Worker{
		store:    s,
		log:      logger,
		interval: interval,
		required: required,
		now:      time.Now,
	}
}

// Start runs the loop until ctx is cancelled. One tick fires immediately so
// a restart does not wait a full interval to resume overdue work.
func (w *Worker) Start(ctx context.Context) {
	w.log.Info("starting account purge worker",
		"interval", w.interval, "required_services", w.required)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	w.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			w.log.Info("account purge worker stopped")
			return
		case <-ticker.C:
			w.Tick(ctx)
		}
	}
}

// TickReport summarises one pass. Exported so tests can assert on it.
type TickReport struct {
	Due       int
	Requested []uuid.UUID // user.purge_requested emitted this tick
	Purged    []uuid.UUID // completed (all acks present)
	Overdue   []uuid.UUID // past overdueWarnAfter and still incomplete
}

// Tick processes every due account once. Exported for deterministic tests.
func (w *Worker) Tick(ctx context.Context) TickReport {
	var rep TickReport
	due, err := w.store.ListPurgeDue(ctx, batchSize)
	if err != nil {
		w.log.Error("purge worker: list due failed", "event", "purge_list_failed", "err", err)
		return rep
	}
	rep.Due = len(due)

	for _, c := range due {
		if ctx.Err() != nil {
			return rep
		}
		acks, err := w.store.GetPurgeAcks(ctx, c.UserID)
		if err != nil {
			w.log.Error("purge worker: read acks failed", "event", "purge_acks_read_failed",
				"user_id", c.UserID, "err", err)
			continue
		}
		missing := w.missingServices(acks)

		if len(missing) == 0 {
			// Every required service has confirmed erasure. Now — and only
			// now — the credential row goes.
			if err := w.store.CompletePurge(ctx, c.UserID); err != nil {
				if err == store.ErrLifecycleConflict {
					w.log.Debug("purge worker: nothing to complete (already purged or rescued)",
						"user_id", c.UserID)
				} else {
					w.log.Error("purge worker: complete failed", "event", "purge_complete_failed",
						"user_id", c.UserID, "err", err)
				}
				continue
			}
			rep.Purged = append(rep.Purged, c.UserID)
			w.log.Info("account purged", "event", "account_purged", "user_id", c.UserID,
				"acked_services", len(acks))
			continue
		}

		// Not all acks yet: (re-)request. RequestPurge throttles to once per
		// 24h itself, atomically with the outbox insert.
		emitted, err := w.store.RequestPurge(ctx, c.UserID)
		if err != nil {
			w.log.Error("purge worker: request failed", "event", "purge_request_failed",
				"user_id", c.UserID, "err", err)
			continue
		}
		if emitted {
			rep.Requested = append(rep.Requested, c.UserID)
			w.log.Info("purge requested", "event", "purge_requested",
				"user_id", c.UserID, "missing_services", missing,
				"first_request", c.PurgeRequestedAt == nil)
		}

		if w.now().Sub(c.ScheduledPurgeDate) > overdueWarnAfter {
			rep.Overdue = append(rep.Overdue, c.UserID)
			w.log.Warn("account purge overdue — services have not acked",
				"event", "purge_overdue",
				"user_id", c.UserID,
				"scheduled_purge_date", c.ScheduledPurgeDate.UTC().Format(time.RFC3339),
				"overdue_days", int(w.now().Sub(c.ScheduledPurgeDate).Hours()/24),
				"missing_services", missing)
		}
	}
	return rep
}

// missingServices returns required − acked, sorted.
func (w *Worker) missingServices(acks map[string]struct{}) []string {
	var missing []string
	for _, r := range w.required {
		if _, ok := acks[r]; !ok {
			missing = append(missing, r)
		}
	}
	return missing
}
