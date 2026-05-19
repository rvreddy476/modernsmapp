package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/atpost/feed-service/database"
	"github.com/atpost/feed-service/internal/consumers"
	"github.com/atpost/feed-service/internal/events"
	"github.com/atpost/feed-service/internal/http"
	"github.com/atpost/feed-service/internal/pipeline"
	"github.com/atpost/feed-service/internal/ranking"
	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "feed-service"})

	// 2. Config
	port := env("HTTP_PORT", "8086")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	scyllaHosts := env("SCYLLA_HOSTS", "localhost")
	kafkaBrokers := env("KAFKA_BROKERS", "localhost:9092")

	// 3. Database (Postgres)
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

	if err := postgres.BootstrapSchema(ctx, dbPool, database.SetupSQL); err != nil {
		slog.Error("failed to bootstrap feed schema", "error", err)
		os.Exit(1)
	}
	slog.Info("feed schema ready")

	// 4. Database (ScyllaDB)
	cluster := gocql.NewCluster(scyllaHosts)
	cluster.Keyspace = "social_feed"
	cluster.Consistency = gocql.Quorum
	cluster.NumConns = 10
	cluster.MaxPreparedStmts = 1000
	scyllaSession, err := cluster.CreateSession()
	if err != nil {
		slog.Error("failed to connect to scylladb", "error", err)
		os.Exit(1)
	}
	defer scyllaSession.Close()
	slog.Info("connected to scylladb")

	// 5. Redis
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

	// 6. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("feed-service")
	dbMetrics := metrics.NewDBPoolMetrics("feed-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 7. Health checker
	checker := health.New("feed-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))
	checker.Register("scylladb", health.ScyllaCheck(func(ctx context.Context) error {
		return scyllaSession.Query("SELECT now() FROM system.local").Exec()
	}))

	// 8. Dependencies
	pgStore := postgres.New(dbPool)
	timelineStore := scylla.New(scyllaSession)
	feedSvc := service.New(timelineStore, pgStore, rdb)

	// 9. Ranking middleware (v2.0) — ScyllaDB session provides durable interaction fallback
	ranker := ranking.NewRanker(rdb, scyllaSession, 20*time.Millisecond)
	feedSvc.SetRanker(ranker)
	slog.Info("ranking middleware initialized", "circuit_breaker", "20ms")

	// 10. Data pipelines (v2.0)
	affinityPipeline := pipeline.NewAffinityPipeline(dbPool, rdb)
	velocityTracker := pipeline.NewVelocityTracker(rdb)

	// Warm affinity signals from Postgres into Redis on startup
	go func() {
		if err := affinityPipeline.Run(ctx); err != nil {
			slog.Warn("affinity warmup failed", "error", err)
		} else {
			slog.Info("affinity signals warmed into redis")
		}
	}()

	// Start velocity tracker (runs every 5 minutes)
	go velocityTracker.Start(ctx)
	slog.Info("velocity tracker started")

	feedHandler := http.New(feedSvc)

	// Audit CF2: gate every /v1/feed route behind the shared internal
	// service key. The handler supports the middleware but main.go
	// previously never wired the env var, leaving every endpoint
	// callable directly without going through the API gateway.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		feedHandler.WithInternalKey(key)
		slog.Info("feed-service: internal-service-key gate enabled")
	} else {
		slog.Warn("feed-service: INTERNAL_SERVICE_KEY not set — every /v1/feed endpoint is unauthenticated. Do not run this configuration in production.")
	}

	// 11. Kafka Consumer (now also handles PostReacted and CommentCreated)
	consumer := events.NewConsumerWithDialer(
		[]string{kafkaBrokers},
		"feed-service-group",
		"social.events.v1",
		feedSvc,
		rdb,
		timelineStore,
		kafkaDialer,
	)
	go consumer.Start(ctx)
	defer consumer.Close()

	// 11b. Channel update feed-inject consumer
	channelConsumer := consumers.NewChannelUpdateConsumerWithDialer(
		[]string{kafkaBrokers},
		timelineStore,
		rdb,
		kafkaDialer,
	)
	go channelConsumer.Start(ctx)
	defer channelConsumer.Close()

	// 11c. QA events live on a dedicated topic — reuse the main consumer
	// type so QA question fan-out hits the same processMessage switch.
	qaTopic := env("KAFKA_QA_TOPIC", "qa-events")
	qaConsumer := events.NewConsumerWithDialer(
		[]string{kafkaBrokers},
		"feed-service-qa-group",
		qaTopic,
		feedSvc,
		rdb,
		timelineStore,
		kafkaDialer,
	)
	go qaConsumer.Start(ctx)
	defer qaConsumer.Close()

	// 12. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	feedHandler.RegisterRoutes(r)

	// 13. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			consumer.Close()
			channelConsumer.Close()
			rdb.Close()
			scyllaSession.Close()
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
