package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/search-service/internal/events"
	"github.com/atpost/search-service/internal/http"
	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "search-service"})

	// 2. Config
	port := env("HTTP_PORT", "8089")
	opensearchURL := env("OPENSEARCH_URL", "http://opensearch:9200")
	kafkaBrokers := env("KAFKA_BROKERS", "redpanda:9092")
	redisAddr := env("REDIS_ADDR", "redis:6379")

	// 3. OpenSearch Store
	searchStore, err := search.New(opensearchURL)
	if err != nil {
		slog.Error("failed to initialize opensearch store", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to opensearch")

	// 4. Redis
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping failed", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()
	slog.Info("connected to redis")

	// 5. Kafka Consumer
	consumer := events.NewConsumer(
		strings.Split(kafkaBrokers, ","),
		"search-service-group",
		"social.events.v1",
		searchStore,
	)
	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer consumerCancel()
	go consumer.Start(consumerCtx)
	slog.Info("started kafka consumer")

	// 6. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("search-service")

	// 7. Health checker
	checker := health.New("search-service")
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 8. HTTP Handlers
	handler := http.New(searchStore, rdb)

	// 9. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)

	// 10. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			consumerCancel()
			rdb.Close()
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
