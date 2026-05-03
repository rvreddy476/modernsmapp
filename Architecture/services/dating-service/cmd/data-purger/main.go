// data-purger — Sprint 5 DPDP §15.8 worker.
//
// Daily cron. For every dating profile with deleted_at < now() - 30 days:
//
//   1. Delete photos, prompts, tune, preferences, sparks, stashes, passes,
//      blocks, safety_events, meets, reports, verifications, payment_intents,
//      premium_subscriptions, data_exports.
//   2. Anonymise outstanding matches (close them so the other side stops
//      seeing the user as conversational; the row stays so their inbox
//      history is preserved with first_name="Deleted user" surfaced via
//      LookupFirstName).
//   3. Revoke outstanding vouches (status='revoked').
//   4. NULL the user_id on dating_payment_events (the audit row stays).
//   5. Hard-delete the dating_profiles row.
//   6. Emit dating.profile.purged.
//
// All of step 1–5 runs inside one Postgres transaction (store.PurgeUserData)
// so a partial failure rolls back. Step 6 is best-effort.
//
// Run modes:
//   - Daemon (default): loops every DATA_PURGER_INTERVAL (default 24h).
//   - One-shot: DATA_PURGER_ONCE=true exits after one pass.
package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/dating-service/database"
	datingevents "github.com/atpost/dating-service/internal/events"
	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/transport"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "dating-data-purger"})

	pgDSN := os.Getenv("POSTGRES_DSN")
	if pgDSN == "" {
		slog.Error("POSTGRES_DSN required")
		os.Exit(1)
	}
	graceDays := envInt("DPDP_GRACE_DAYS", 30)
	batchSize := envInt("PURGE_BATCH_SIZE", 100)
	once := os.Getenv("DATA_PURGER_ONCE") == "true"
	interval := envDuration("DATA_PURGER_INTERVAL", 24*time.Hour)
	kafkaBrokers := strings.Split(envOr("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := envOr("KAFKA_TOPIC", "dating-events")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := pgxpool.ParseConfig(pgDSN)
	if err != nil {
		slog.Error("parse db config", "error", err)
		os.Exit(1)
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		slog.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := database.BootstrapSchema(ctx, pool); err != nil {
		slog.Error("bootstrap schema", "error", err)
		os.Exit(1)
	}

	st := store.New(pool)
	svc := service.New(st, nil)

	dialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("kafka dialer", "error", err)
		os.Exit(1)
	}
	producer := datingevents.NewProducerWithDialer(kafkaBrokers, kafkaTopic, dialer)
	defer func() { _ = producer.Close() }()
	svc.SetProducer(producer)

	for {
		purged, expired, err := runOnce(ctx, st, svc, graceDays, batchSize)
		if err != nil {
			slog.Error("purge pass failed", "error", err)
		} else {
			slog.Info("purge pass complete", "purged_profiles", purged, "expired_exports", expired)
		}
		if once {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// runOnce sweeps expired soft-deletes and stale data exports.
func runOnce(ctx context.Context, st *store.Store, svc *service.Service, graceDays, batchSize int) (int, int64, error) {
	ids, err := st.ListExpiredSoftDeletes(ctx, graceDays, batchSize)
	if err != nil {
		return 0, 0, err
	}
	purged := 0
	for _, id := range ids {
		if err := svc.PurgeProfile(ctx, id); err != nil {
			slog.Warn("purge profile failed", "user_id", id, "error", err)
			continue
		}
		purged++
	}
	expired, err := st.ExpireOldExports(ctx)
	if err != nil {
		slog.Warn("expire old exports failed", "error", err)
	}
	return purged, expired, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
