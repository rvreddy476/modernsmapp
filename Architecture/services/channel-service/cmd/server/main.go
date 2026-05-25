package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/channel-service/database"
	channelevents "github.com/atpost/channel-service/internal/events"
	"github.com/atpost/channel-service/internal/http"
	"github.com/atpost/channel-service/internal/service"
	"github.com/atpost/channel-service/internal/store"
	pgstore "github.com/atpost/channel-service/internal/store/postgres"
	"github.com/atpost/channel-service/internal/workers"
	"github.com/atpost/shared/counters"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "channel-service"})

	// 2. Config
	port := env("HTTP_PORT", "8106")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := env("KAFKA_TOPIC", "channel-events")

	// 3. Database
	ctx := context.Background()
	poolCfg, err := pgxpool.ParseConfig(pgDSN)
	if err != nil {
		slog.Error("parse db config", "error", err)
		os.Exit(1)
	}
	poolCfg.MaxConns = 25
	poolCfg.MinConns = 5
	poolCfg.MaxConnLifetime = 15 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	dbPool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		slog.Error("postgres ping failed", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to postgres")

	// 4. Redis
	rdb, err := transport.NewRedisClientFromEnv(redisAddr)
	if err != nil {
		slog.Error("failed to configure redis client", "error", err)
		os.Exit(1)
	}
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping failed", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()
	slog.Info("connected to redis")

	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

	if err := pgstore.BootstrapSchema(ctx, dbPool, database.SetupSQL); err != nil {
		slog.Error("failed to bootstrap channel schema", "error", err)
		os.Exit(1)
	}
	slog.Info("channel schema ready")

	// 5. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("channel-service")
	dbMetrics := metrics.NewDBPoolMetrics("channel-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 6. Health checker
	checker := health.New("channel-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 7. Schema migrations (idempotent)
	if err := runMigrations(ctx, dbPool); err != nil {
		slog.Warn("schema migration warning", "error", err)
	}

	// 8. Dependencies
	channelStore := store.New(dbPool)
	channelSvc := service.New(channelStore, rdb)

	// 8. Kafka producer
	producer := channelevents.NewProducerWithDialer(kafkaBrokers, kafkaTopic, rdb, kafkaDialer)
	channelSvc.SetProducer(producer)
	slog.Info("kafka producer initialized", "topic", kafkaTopic)

	// 9. Kafka consumer (GDPR)
	consumer := channelevents.NewConsumerWithDialer(kafkaBrokers, "channel-service-consumer", channelStore, rdb, kafkaDialer)
	consumerCtx, cancelConsumer := context.WithCancel(ctx)
	go consumer.Start(consumerCtx)
	slog.Info("kafka consumer started")

	// 10. Schedule worker (legacy — publishes via service producer)
	workerCtx, cancelWorker := context.WithCancel(ctx)
	go channelSvc.RunScheduleWorker(workerCtx)

	// 11. Fanout worker + scheduled update publisher
	fanoutWorker := workers.NewFanoutWorkerWithDialer(dbPool, kafkaBrokers, slog.Default(), kafkaDialer)
	fanoutCtx, cancelFanout := context.WithCancel(ctx)
	go fanoutWorker.Start(fanoutCtx)
	go fanoutWorker.StartScheduler(fanoutCtx)
	slog.Info("fanout worker and scheduler started")

	// Sharded-counter flush worker: drains Redis subscriber-count deltas
	// every 10s and materializes the sum into broadcast_channels.subscriber_count.
	// Removes per-subscribe contention on the singleton channel row at
	// celebrity-channel scale. Matches the community member-count pattern.
	if sc := channelSvc.SubscriberCounter(); sc != nil {
		flush := func(ctx context.Context, channelID string, total int64) error {
			id, err := uuid.Parse(channelID)
			if err != nil {
				return err
			}
			return channelStore.SetSubscriberCount(ctx, id, total)
		}
		go counters.NewWorker(sc, flush, counters.WorkerOptions{}).Start(consumerCtx)
		slog.Info("channel subscriber-count sharded flush worker started")
	}

	channelHandler := http.New(channelSvc)

	// Audit CCh5: gate every /v1/channels/* endpoint behind the shared
	// internal service key. The handler supports the middleware but
	// main.go previously never wired the env var, so the gate was a
	// no-op and every endpoint was directly reachable.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		channelHandler.WithInternalKey(key)
		slog.Info("channel-service: internal-service-key gate enabled")
	} else {
		slog.Warn("channel-service: INTERNAL_SERVICE_KEY not set — every endpoint is unauthenticated. Do not run this configuration in production.")
	}

	// 12. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	channelHandler.RegisterRoutes(r)

	// 13. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			cancelConsumer()
			cancelWorker()
			cancelFanout()
			if err := fanoutWorker.Close(); err != nil {
				slog.Warn("failed to close fanout worker", "error", err)
			}
			if err := producer.Close(); err != nil {
				slog.Warn("failed to close kafka producer", "error", err)
			}
			rdb.Close()
			dbPool.Close()
			slog.Info("cleanup completed")
		},
	}); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func runMigrations(ctx context.Context, db *pgxpool.Pool) error {
	migrations := []string{
		// Add stash_count column to channel_updates if it doesn't exist
		`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM information_schema.columns
				WHERE table_name='channel_updates' AND column_name='stash_count') THEN
				ALTER TABLE channel_updates ADD COLUMN stash_count BIGINT NOT NULL DEFAULT 0;
			END IF;
		END $$`,
		`CREATE TABLE IF NOT EXISTS update_sparks (
			update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
			user_id UUID NOT NULL,
			is_supernova BOOLEAN NOT NULL DEFAULT false,
			weight INT NOT NULL DEFAULT 1,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (update_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_us_user ON update_sparks(user_id)`,
		`CREATE TABLE IF NOT EXISTS update_views (
			update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
			user_id UUID NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (update_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS update_stashes (
			update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
			user_id UUID NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (update_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ust_user ON update_stashes(user_id)`,
		`CREATE TABLE IF NOT EXISTS update_echoes (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
			user_id UUID NOT NULL,
			echo_type TEXT NOT NULL DEFAULT 'share',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ue_update ON update_echoes(update_id)`,
		`CREATE TABLE IF NOT EXISTS update_comments (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
			author_id UUID NOT NULL,
			body TEXT NOT NULL,
			parent_id UUID REFERENCES update_comments(id) ON DELETE SET NULL,
			is_pinned BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_uc_update ON update_comments(update_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS poll_votes (
			update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
			user_id UUID NOT NULL,
			option_index INT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (update_id, user_id, option_index)
		)`,
		`CREATE TABLE IF NOT EXISTS event_rsvps (
			update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
			user_id UUID NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('going','interested','not_going')),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (update_id, user_id)
		)`,
	}
	for _, m := range migrations {
		if _, err := db.Exec(ctx, m); err != nil {
			return err
		}
	}
	slog.Info("channel-service schema migrations applied")
	return nil
}

func collectDBPoolStats(ctx context.Context, pool *pgxpool.Pool, m *metrics.DBPoolMetrics) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stat := pool.Stat()
			m.Update(metrics.PgxPoolStat{
				AcquireCount:  stat.AcquireCount(),
				AcquiredConns: stat.AcquiredConns(),
				IdleConns:     stat.IdleConns(),
				TotalConns:    stat.TotalConns(),
				MaxConns:      stat.MaxConns(),
			})
		}
	}
}
