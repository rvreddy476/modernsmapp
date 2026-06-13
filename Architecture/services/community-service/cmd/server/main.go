package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/community-service/database"
	communityevents "github.com/atpost/community-service/internal/events"
	"github.com/atpost/community-service/internal/http"
	"github.com/atpost/community-service/internal/reconcile"
	"github.com/atpost/community-service/internal/service"
	"github.com/atpost/community-service/internal/store"
	pgstore "github.com/atpost/community-service/internal/store/postgres"
	"github.com/atpost/shared/counters"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/google/uuid"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "community-service"})

	// 2. Config
	port := env("HTTP_PORT", "8107")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := env("KAFKA_TOPIC", "community-events")

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

	if err := pgstore.BootstrapSchema(ctx, dbPool, database.SetupSQL, database.Migrations); err != nil {
		slog.Error("failed to bootstrap community schema", "error", err)
		os.Exit(1)
	}
	slog.Info("community schema ready")

	// 5. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("community-service")
	dbMetrics := metrics.NewDBPoolMetrics("community-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 6. Health checker
	checker := health.New("community-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 7. Dependencies
	communityStore := store.New(dbPool)
	communitySvc := service.New(communityStore, rdb)

	// 8. Kafka producer
	producer := communityevents.NewProducerWithDialer(kafkaBrokers, kafkaTopic, kafkaDialer)
	communitySvc.SetProducer(producer)
	slog.Info("kafka producer initialized", "topic", kafkaTopic)

	// 9. Kafka consumer (GDPR)
	consumer := communityevents.NewConsumerWithDialer(kafkaBrokers, "community-service-consumer", communityStore, rdb, kafkaDialer)
	consumerCtx, cancelConsumer := context.WithCancel(ctx)
	go consumer.Start(consumerCtx)
	slog.Info("kafka consumer started")

	// Reconcile drift in communities.member_count every hour. Without
	// this, any IncrementMemberCount event lost to a deploy bounce
	// leaves the cached count off-by-N forever.
	go reconcile.NewMemberCountReconciler(dbPool).Start(consumerCtx)

	// Sharded-counter flush worker: drains Redis member-count deltas
	// every 10s and materializes the sum into communities.member_count.
	// Removes per-join contention on the singleton communities row —
	// the membership table writes are already distributed by primary
	// key, and this collapses N joins/sec into 1 row UPDATE/10s per
	// community.
	if mc := communitySvc.MemberCounter(); mc != nil {
		flush := func(ctx context.Context, communityID string, total int64) error {
			id, err := uuid.Parse(communityID)
			if err != nil {
				return err
			}
			return communityStore.SetMemberCount(ctx, id, total)
		}
		go counters.NewWorker(mc, flush, counters.WorkerOptions{}).Start(consumerCtx)
		slog.Info("community member-count sharded flush worker started")
	}

	communityHandler := http.New(communitySvc)

	// Audit CC1: gate every /v1/communities/* endpoint behind the
	// shared internal service key. The handler supports the middleware
	// but main.go previously never wired the env var, leaving the
	// entire surface — including wiki edits and moderation actions —
	// callable directly without going through the API gateway.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		communityHandler.WithInternalKey(key)
		slog.Info("community-service: internal-service-key gate enabled")
	} else {
		slog.Warn("community-service: INTERNAL_SERVICE_KEY not set — every endpoint is unauthenticated. Do not run this configuration in production.")
	}

	// 10. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	communityHandler.RegisterRoutes(r)

	// 11. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			cancelConsumer()
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
