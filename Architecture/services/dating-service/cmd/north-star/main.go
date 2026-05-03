// north-star — Sprint 5 telemetry job. Runs nightly via cron (k8s
// CronJob or similar). Computes the spec §17 north-star KPIs, updates
// the Prometheus gauges, and emits dating.telemetry.north_star.
//
// One-shot binary; exits after a single pass.
package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/dating-service/database"
	datingevents "github.com/atpost/dating-service/internal/events"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/dating-service/internal/telemetry"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/transport"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "dating-north-star"})

	pgDSN := os.Getenv("POSTGRES_DSN")
	if pgDSN == "" {
		slog.Error("POSTGRES_DSN required")
		os.Exit(1)
	}
	kafkaBrokers := strings.Split(envOr("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := envOr("KAFKA_TOPIC", "dating-events")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(pgDSN)
	if err != nil {
		slog.Error("parse db config", "error", err)
		os.Exit(1)
	}
	cfg.MaxConns = 4
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

	dialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("kafka dialer", "error", err)
		os.Exit(1)
	}
	producer := datingevents.NewProducerWithDialer(kafkaBrokers, kafkaTopic, dialer)
	defer func() { _ = producer.Close() }()

	computer := telemetry.NewNorthStarComputer(st, producer, telemetry.Default())
	snap, err := computer.Run(ctx)
	if err != nil {
		slog.Error("north-star run failed", "error", err)
		if snap != nil {
			slog.Info("north-star partial", "snap", snap)
		}
		os.Exit(1)
	}
	slog.Info("north-star emitted",
		"window_days", snap.WindowDays,
		"off_app_meet_rate", snap.OffAppMeetRate,
		"safe_check_ins", snap.SafeCheckInsCount,
		"scheduled_meets", snap.ScheduledMeetsCount,
	)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
