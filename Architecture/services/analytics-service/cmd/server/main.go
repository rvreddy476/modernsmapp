package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/facebook-like/analytics-service/internal/aggregation"
	"github.com/facebook-like/analytics-service/internal/consumers"
	httpHandler "github.com/facebook-like/analytics-service/internal/http"
	"github.com/facebook-like/analytics-service/internal/reconcile"
	"github.com/facebook-like/analytics-service/internal/scoring"
	"github.com/facebook-like/analytics-service/internal/service"
	"github.com/facebook-like/analytics-service/internal/store/postgres"
	scyllaStore "github.com/facebook-like/analytics-service/internal/store/scylla"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

func main() {
	// 1. Config
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8094"
	}
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	scyllaHosts := os.Getenv("SCYLLA_HOSTS")
	kafkaBrokers := os.Getenv("KAFKA_BROKERS")
	kafkaTopic := "social.events.v1"

	ctx := context.Background()

	// 2. PostgreSQL
	dbPool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		log.Fatalf("Unable to connect to Postgres: %v", err)
	}
	defer dbPool.Close()

	ensureSchema(ctx, dbPool)

	// 3. Redis
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Printf("Warning: Redis not reachable: %v (continuing without real-time counters)", err)
	}
	defer rdb.Close()

	// 4. ScyllaDB
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
			log.Printf("Warning: ScyllaDB not reachable: %v (continuing without watch sessions)", err)
		} else {
			scyllaSession = sess
			defer scyllaSession.Close()
			watchStore = scyllaStore.NewWatchStore(scyllaSession)
		}
	}

	// 5. Kafka producer (for publishing video events to downstream consumers)
	var kafkaWriter *kafka.Writer
	if kafkaBrokers != "" {
		kafkaWriter = &kafka.Writer{
			Addr:     kafka.TCP(strings.Split(kafkaBrokers, ",")...),
			Topic:    kafkaTopic,
			Balancer: &kafka.LeastBytes{},
		}
		defer kafkaWriter.Close()
	}

	// 6. Dependencies
	store := postgres.New(dbPool)
	aggStore := postgres.NewAggregateStore(dbPool)
	svc := service.New(store, kafkaWriter)
	handler := httpHandler.New(svc, rdb)
	dashHandler := httpHandler.NewDashboardHandler(aggStore)

	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	// 7. Start Kafka consumer for real-time video view counting
	if kafkaBrokers != "" && watchStore != nil {
		videoConsumer := consumers.NewVideoViewConsumer(watchStore, rdb)
		go videoConsumer.Start(workerCtx, strings.Split(kafkaBrokers, ","), kafkaTopic)
		log.Println("Video view consumer started")
	}

	// 8. Start TrustFactor worker (10-min interval)
	trustWorker := scoring.NewTrustFactorWorker(rdb, scyllaSession)
	go trustWorker.Start(workerCtx)
	log.Println("TrustFactor worker started")

	// 9. Start hourly aggregator
	hourlyAgg := aggregation.NewHourlyAggregator(dbPool, rdb)
	go hourlyAgg.Start(workerCtx)
	log.Println("Hourly aggregator started")

	// 10. Start daily rollup
	dailyRollup := aggregation.NewDailyRollup(dbPool, rdb)
	go dailyRollup.Start(workerCtx)
	log.Println("Daily rollup started")

	// 11. Start view reconciler (5-min interval)
	if scyllaSession != nil {
		viewReconciler := reconcile.NewViewReconciler(rdb, scyllaSession)
		go viewReconciler.Start(workerCtx)
		log.Println("View reconciler started")
	}

	// 12. HTTP Server
	r := gin.Default()
	handler.RegisterRoutes(r)
	dashHandler.RegisterRoutes(r.Group("/v1/analytics"))

	log.Printf("Starting analytics-service on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
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
		log.Printf("Warning: Schema ensure error (may be partial): %v", err)
	}
}
