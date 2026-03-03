package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/analytics-service/internal/aggregation"
	"github.com/atpost/analytics-service/internal/consumers"
	httpHandler "github.com/atpost/analytics-service/internal/http"
	"github.com/atpost/analytics-service/internal/reconcile"
	"github.com/atpost/analytics-service/internal/scoring"
	"github.com/atpost/analytics-service/internal/service"
	"github.com/atpost/analytics-service/internal/store/postgres"
	scyllaStore "github.com/atpost/analytics-service/internal/store/scylla"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "analytics-service"})

	// 2. Config
	port := env("HTTP_PORT", "8094")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	scyllaHosts := os.Getenv("SCYLLA_HOSTS")
	kafkaBrokers := os.Getenv("KAFKA_BROKERS")
	kafkaTopic := "social.events.v1"

	ctx := context.Background()

	// 3. PostgreSQL
	dbPool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	ensureSchema(ctx, dbPool)

	// 4. Redis
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Warn("redis not reachable (continuing without real-time counters)", "error", err)
	}
	defer rdb.Close()

	// 5. ScyllaDB
	var watchStore *scyllaStore.WatchStore
	var scyllaSession *gocql.Session
	if scyllaHosts != "" {
		cluster := gocql.NewCluster(strings.Split(scyllaHosts, ",")...)
		cluster.Keyspace = "social_analytics"
		cluster.Consistency = gocql.LocalQuorum
		cluster.ConnectTimeout = 10_000_000_000 // 10s
		cluster.Timeout = 5_000_000_000         // 5s
		sess, err := cluster.CreateSession()
		if err != nil {
			slog.Warn("scylladb not reachable (continuing without watch sessions)", "error", err)
		} else {
			scyllaSession = sess
			defer scyllaSession.Close()
			watchStore = scyllaStore.NewWatchStore(scyllaSession)
		}
	}

	// 6. Kafka producer (for publishing video events to downstream consumers)
	var kafkaWriter *kafka.Writer
	if kafkaBrokers != "" {
		kafkaWriter = &kafka.Writer{
			Addr:     kafka.TCP(strings.Split(kafkaBrokers, ",")...),
			Topic:    kafkaTopic,
			Balancer: &kafka.LeastBytes{},
		}
		defer kafkaWriter.Close()
	}

	// 7. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("analytics-service")
	dbMetrics := metrics.NewDBPoolMetrics("analytics-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 8. Health checker
	checker := health.New("analytics-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))
	if scyllaSession != nil {
		checker.Register("scylladb", health.ScyllaCheck(func(ctx context.Context) error {
			return scyllaSession.Query("SELECT now() FROM system.local").Exec()
		}))
	}

	// 9. Dependencies
	store := postgres.New(dbPool)
	aggStore := postgres.NewAggregateStore(dbPool)
	svc := service.New(store, kafkaWriter)
	handler := httpHandler.New(svc, rdb)
	dashHandler := httpHandler.NewDashboardHandler(aggStore)

	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	// 10. Start Kafka consumer for real-time video view counting
	if kafkaBrokers != "" && watchStore != nil {
		videoConsumer := consumers.NewVideoViewConsumer(watchStore, rdb)
		go videoConsumer.Start(workerCtx, strings.Split(kafkaBrokers, ","), kafkaTopic)
		slog.Info("video view consumer started")
	}

	// 10b. Start engagement consumer for CQS recalculation on likes/comments
	if kafkaBrokers != "" {
		engagementConsumer := consumers.NewEngagementConsumer(dbPool, rdb)
		go engagementConsumer.Start(workerCtx, strings.Split(kafkaBrokers, ","), kafkaTopic)
		slog.Info("engagement analytics consumer started")
	}

	// 11. Start TrustFactor worker (10-min interval)
	trustWorker := scoring.NewTrustFactorWorker(rdb, scyllaSession)
	go trustWorker.Start(workerCtx)
	slog.Info("trust factor worker started")

	// 12. Start hourly aggregator
	hourlyAgg := aggregation.NewHourlyAggregator(dbPool, rdb)
	go hourlyAgg.Start(workerCtx)
	slog.Info("hourly aggregator started")

	// 13. Start daily rollup
	dailyRollup := aggregation.NewDailyRollup(dbPool, rdb)
	go dailyRollup.Start(workerCtx)
	slog.Info("daily rollup started")

	// 14. Start view reconciler (5-min interval)
	if scyllaSession != nil {
		viewReconciler := reconcile.NewViewReconciler(rdb, scyllaSession)
		go viewReconciler.Start(workerCtx)
		slog.Info("view reconciler started")
	}

	// 15. HTTP Server
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)
	dashHandler.RegisterRoutes(r.Group("/v1/analytics"))

	// 16. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			workerCancel()
			if kafkaWriter != nil {
				kafkaWriter.Close()
			}
			rdb.Close()
			if scyllaSession != nil {
				scyllaSession.Close()
			}
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

func ensureSchema(ctx context.Context, db *pgxpool.Pool) {
	ddl := `
		CREATE SCHEMA IF NOT EXISTS analytics;

		CREATE TABLE IF NOT EXISTS analytics.events_raw (
			id UUID,
			user_id UUID,
			session_id UUID,
			type TEXT NOT NULL,
			payload JSONB,
			ts TIMESTAMPTZ NOT NULL,
			received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		) PARTITION BY RANGE (ts);

		CREATE TABLE IF NOT EXISTS analytics.events_raw_default
			PARTITION OF analytics.events_raw DEFAULT;

		CREATE INDEX IF NOT EXISTS idx_events_type_ts
			ON analytics.events_raw (type, ts DESC);

		CREATE INDEX IF NOT EXISTS idx_events_user_ts
			ON analytics.events_raw (user_id, ts DESC);

		-- Video analytics: content-based index for aggregation queries
		CREATE INDEX IF NOT EXISTS idx_events_raw_content
			ON analytics.events_raw ((payload->>'content_id'), ts DESC)
			WHERE type IN ('play_start','milestone','play_end','watch_heartbeat','impression');

		-- Hourly aggregation table
		CREATE TABLE IF NOT EXISTS analytics.content_hourly_agg (
			content_id          UUID NOT NULL,
			hour_bucket         TIMESTAMPTZ NOT NULL,
			creator_id          UUID NOT NULL,
			content_type        TEXT NOT NULL,
			impressions         BIGINT NOT NULL DEFAULT 0,
			plays               BIGINT NOT NULL DEFAULT 0,
			views_display       BIGINT NOT NULL DEFAULT 0,
			views_1s            BIGINT NOT NULL DEFAULT 0,
			views_3s            BIGINT NOT NULL DEFAULT 0,
			views_10s           BIGINT NOT NULL DEFAULT 0,
			views_30s           BIGINT NOT NULL DEFAULT 0,
			views_60s           BIGINT NOT NULL DEFAULT 0,
			unique_viewers      BIGINT NOT NULL DEFAULT 0,
			repeat_viewers      BIGINT NOT NULL DEFAULT 0,
			watch_time_total_ms BIGINT NOT NULL DEFAULT 0,
			avg_watch_time_ms   DOUBLE PRECISION NOT NULL DEFAULT 0,
			avg_percent_viewed  DOUBLE PRECISION NOT NULL DEFAULT 0,
			completion_rate     DOUBLE PRECISION NOT NULL DEFAULT 0,
			rewatch_rate        DOUBLE PRECISION NOT NULL DEFAULT 0,
			skip_rate           DOUBLE PRECISION NOT NULL DEFAULT 0,
			early_swipe_rate    DOUBLE PRECISION NOT NULL DEFAULT 0,
			likes               BIGINT NOT NULL DEFAULT 0,
			comments            BIGINT NOT NULL DEFAULT 0,
			shares              BIGINT NOT NULL DEFAULT 0,
			saves               BIGINT NOT NULL DEFAULT 0,
			follows_from_content BIGINT NOT NULL DEFAULT 0,
			not_interested      BIGINT NOT NULL DEFAULT 0,
			reports             BIGINT NOT NULL DEFAULT 0,
			blocks              BIGINT NOT NULL DEFAULT 0,
			view_score_total    DOUBLE PRECISION NOT NULL DEFAULT 0,
			vqs_avg             DOUBLE PRECISION NOT NULL DEFAULT 0,
			content_quality_score DOUBLE PRECISION NOT NULL DEFAULT 0,
			created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (content_id, hour_bucket)
		) PARTITION BY RANGE (hour_bucket);

		CREATE TABLE IF NOT EXISTS analytics.content_hourly_agg_default
			PARTITION OF analytics.content_hourly_agg DEFAULT;

		CREATE INDEX IF NOT EXISTS idx_hourly_agg_creator
			ON analytics.content_hourly_agg (creator_id, hour_bucket DESC);

		-- Daily summary table
		CREATE TABLE IF NOT EXISTS analytics.content_daily_summary (
			content_id          UUID NOT NULL,
			day_bucket          DATE NOT NULL,
			creator_id          UUID NOT NULL,
			content_type        TEXT NOT NULL,
			impressions         BIGINT NOT NULL DEFAULT 0,
			plays               BIGINT NOT NULL DEFAULT 0,
			views_display       BIGINT NOT NULL DEFAULT 0,
			unique_viewers      BIGINT NOT NULL DEFAULT 0,
			watch_time_total_ms BIGINT NOT NULL DEFAULT 0,
			avg_watch_time_ms   DOUBLE PRECISION NOT NULL DEFAULT 0,
			avg_percent_viewed  DOUBLE PRECISION NOT NULL DEFAULT 0,
			completion_rate     DOUBLE PRECISION NOT NULL DEFAULT 0,
			likes               BIGINT NOT NULL DEFAULT 0,
			comments            BIGINT NOT NULL DEFAULT 0,
			shares              BIGINT NOT NULL DEFAULT 0,
			saves               BIGINT NOT NULL DEFAULT 0,
			view_score_total    DOUBLE PRECISION NOT NULL DEFAULT 0,
			content_quality_score DOUBLE PRECISION NOT NULL DEFAULT 0,
			created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (content_id, day_bucket)
		);

		CREATE INDEX IF NOT EXISTS idx_daily_summary_creator
			ON analytics.content_daily_summary (creator_id, day_bucket DESC);
	`
	if _, err := db.Exec(ctx, ddl); err != nil {
		slog.Warn("schema ensure error (may be partial)", "error", err)
	}
}
