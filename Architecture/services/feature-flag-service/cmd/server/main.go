package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/feature-flag-service/internal/http"
	"github.com/atpost/feature-flag-service/internal/service"
	"github.com/atpost/feature-flag-service/internal/store/postgres"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "feature-flag-service"})

	// 2. Config
	port := env("HTTP_PORT", "8095")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")

	// 3. Database
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

	// 4. Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Warn("redis not connected", "error", err)
	}
	defer rdb.Close()
	slog.Info("connected to redis")

	// 5. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("feature-flag-service")
	dbMetrics := metrics.NewDBPoolMetrics("feature-flag-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 6. Health checker
	checker := health.New("feature-flag-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 7. Kafka writer
	kafkaBrokers := env("KAFKA_BROKERS", "localhost:9092")
	kafkaWriter := &kafka.Writer{
		Addr:     kafka.TCP(strings.Split(kafkaBrokers, ",")...),
		Topic:    "social.events.v1",
		Balancer: &kafka.LeastBytes{},
	}
	defer kafkaWriter.Close()

	// 8. Dependencies
	store := postgres.New(dbPool)
	svc := service.New(store, rdb, kafkaWriter)
	handler := http.New(svc)

	// 8. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)

	// 9. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
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
