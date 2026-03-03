package main

import (
	"context"
	"log/slog"
	"os"
	"time"

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
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
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
	dbPool, err := pgxpool.New(ctx, pgDSN)
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

	// 4. Database (ScyllaDB)
	cluster := gocql.NewCluster(scyllaHosts)
	cluster.Keyspace = "social_feed"
	cluster.Consistency = gocql.Quorum
	scyllaSession, err := cluster.CreateSession()
	if err != nil {
		slog.Error("failed to connect to scylladb", "error", err)
		os.Exit(1)
	}
	defer scyllaSession.Close()
	slog.Info("connected to scylladb")

	// 5. Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping failed", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()
	slog.Info("connected to redis")

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

	// 11. Kafka Consumer (now also handles PostReacted and CommentCreated)
	consumer := events.NewConsumer(
		[]string{kafkaBrokers},
		"feed-service-group",
		"social.events.v1",
		feedSvc,
		rdb,
		timelineStore,
	)
	go consumer.Start(ctx)
	defer consumer.Close()

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
