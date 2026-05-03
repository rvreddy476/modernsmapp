// Command rider-cron runs the periodic-job daemon for rider-service.
//
// Sprint 4: every job per spec §15 is registered via the new
// internal/cron.Runner so each invocation produces one rider_cron_runs
// row (started/finished/status/rows_processed/error_summary). Ops can
// see when each job last ran via /v1/rider/admin/reports/cron-runs.
//
// Registered jobs:
//
//	30s  : OfferExpiry            sweep rider_ride_offers past expires_at
//	1m   : StaleRideCleanup       expire stuck rides + safety incidents
//	15m  : IdempotencyPurge       drop expired rider_idempotency rows
//	1h   : SubscriptionExpiry     7d/3d/1d expiring reminders (deduped)
//	1h   : GracePeriodTransition  active->grace, grace->expired
//	1h   : SubscriptionAutoRenew  wallet-driven auto-renewal
//	1h   : PartnerMetricsRecalc   trailing-30d acceptance/cancel/completion
//	24h  : DocumentExpiry         30/14/7/3/1/expired doc reminders
//	24h  : FraudScoreRecalc       nightly fraud-score + auto-suspend
//	24h  : DailyRevenueReport     yesterday's rollup into rider_daily_revenue
//	24h  : AdminQueueSummary      pending-queue digest
//
// Idempotency: every job is designed for a re-run to be a no-op or an
// over-write. The cron framework also enforces a DB-side advisory lock
// via "is there a status='running' row within the last 2 hours?".
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/atpost/rider-service/database"
	"github.com/atpost/rider-service/internal/cron"
	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/rider-service/internal/service/jobs"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/rider-service/internal/wallet"
	"github.com/atpost/shared/o11y/logging"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "rider-cron"})

	pgDSN := os.Getenv("POSTGRES_DSN")
	if pgDSN == "" {
		slog.Error("POSTGRES_DSN required")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := database.BootstrapSchema(ctx, pool); err != nil {
		slog.Error("failed to bootstrap rider schema", "error", err)
		os.Exit(1)
	}

	st := store.New(pool)

	walletURL := os.Getenv("WALLET_SERVICE_URL")
	if walletURL == "" {
		walletURL = "http://wallet-service:8114"
	}
	walletClient := wallet.NewHTTPClient(walletURL, os.Getenv("INTERNAL_SERVICE_KEY"))
	svc := service.New(st, walletClient, service.Config{})

	// Hook the Kafka producer so jobs emit events. Falls back to the
	// no-op publisher when KAFKA_BROKERS is unset (local dev / tests).
	// We keep the publisher behind nil-typed interface vars so the
	// per-job `if pub != nil` guards correctly skip emission instead of
	// panicking on a typed-nil pointer.
	var (
		subPub      jobs.SubscriptionPublisher
		subExpPub   jobs.SubscriptionExpiringPublisher
		docPub      jobs.DocExpiryPublisher
		ridePub     jobs.RidePublisher
		fraudPub    jobs.FraudPublisher
		revenuePub  jobs.RevenuePublisher
		summaryPub  jobs.AdminSummaryPublisher
	)
	if brokers := os.Getenv("KAFKA_BROKERS"); strings.TrimSpace(brokers) != "" {
		topic := os.Getenv("KAFKA_TOPIC")
		if topic == "" {
			topic = "rider-events"
		}
		producer := events.NewProducer(strings.Split(brokers, ","), topic)
		defer func() { _ = producer.Close() }()
		svc.SetProducer(producer)
		subPub = producer
		subExpPub = producer
		docPub = producer
		ridePub = producer
		fraudPub = producer
		revenuePub = producer
		summaryPub = producer
	}

	runner := cron.NewRunner(cron.NewStoreAdapter(st), nil)

	// 30s: offer expiry. Cheap UPDATE; also wired separately so it doesn't
	// share the 2h advisory lock with longer jobs.
	runner.RegisterJob("OfferExpiry", cron.JobOptions{
		Interval: 30 * time.Second, MaxRunningAge: 5 * time.Minute,
	}, func(ctx context.Context) (int, error) {
		return jobs.RunOfferExpiry(ctx, st)
	})

	// 1m: stale ride sweeper.
	runner.RegisterJob("StaleRideCleanup", cron.JobOptions{
		Interval: 60 * time.Second, MaxRunningAge: 10 * time.Minute,
	}, func(ctx context.Context) (int, error) {
		return jobs.RunStaleRideCleanup(ctx, st, svc, ridePub)
	})

	// 15m: idempotency purge.
	runner.RegisterJob("IdempotencyPurge", cron.JobOptions{
		Interval: 15 * time.Minute, MaxRunningAge: 1 * time.Hour,
	}, func(ctx context.Context) (int, error) {
		n, err := st.PurgeExpiredIdempotency(ctx)
		return int(n), err
	})

	// 1h: subscription expiry reminders, grace transition, auto-renewal,
	// partner metrics.
	runner.RegisterJob("SubscriptionExpiry", cron.JobOptions{
		Interval: time.Hour,
	}, func(ctx context.Context) (int, error) {
		return jobs.RunSubscriptionExpiryChecker(ctx, st, subExpPub)
	})
	runner.RegisterJob("GracePeriodTransition", cron.JobOptions{
		Interval: time.Hour,
	}, func(ctx context.Context) (int, error) {
		return jobs.RunGracePeriodTransition(ctx, st, subPub)
	})
	runner.RegisterJob("SubscriptionAutoRenew", cron.JobOptions{
		Interval: time.Hour,
	}, func(ctx context.Context) (int, error) {
		return jobs.RunSubscriptionAutoRenewal(ctx, st, walletClient, subPub)
	})
	runner.RegisterJob("PartnerMetricsRecalc", cron.JobOptions{
		Interval: time.Hour,
	}, func(ctx context.Context) (int, error) {
		return jobs.RunPartnerMetricsRecalc(ctx, st)
	})

	// 24h: doc expiry reminders, fraud score, daily revenue, admin summary.
	day := 24 * time.Hour
	runner.RegisterJob("DocumentExpiry", cron.JobOptions{
		Interval: day,
	}, func(ctx context.Context) (int, error) {
		return jobs.RunDocumentExpiryReminder(ctx, st, docPub)
	})
	runner.RegisterJob("FraudScoreRecalc", cron.JobOptions{
		Interval: day,
	}, func(ctx context.Context) (int, error) {
		return jobs.RunFraudScoreRecalc(ctx, st, fraudPub)
	})
	runner.RegisterJob("DailyRevenueReport", cron.JobOptions{
		Interval: day,
	}, func(ctx context.Context) (int, error) {
		return jobs.RunDailyRevenueReport(ctx, st, revenuePub)
	})
	runner.RegisterJob("AdminQueueSummary", cron.JobOptions{
		Interval: day,
	}, func(ctx context.Context) (int, error) {
		return jobs.RunAdminQueueSummary(ctx, st, summaryPub)
	})

	slog.Info("rider-cron starting (sprint 4)", "jobs", runner.Jobs())
	runner.Run(ctx)
	slog.Info("rider-cron shut down")
}
