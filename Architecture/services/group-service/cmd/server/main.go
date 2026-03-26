package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/group-service/database"
	groupevents "github.com/atpost/group-service/internal/events"
	"github.com/atpost/group-service/internal/http"
	"github.com/atpost/group-service/internal/service"
	"github.com/atpost/group-service/internal/store"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "group-service"})

	// 2. Config
	port := env("HTTP_PORT", "8090")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	msgURL := env("MESSAGE_SERVICE_URL", "http://chat-message-service:8092")
	postURL := env("POST_SERVICE_URL", "http://post-service:8084")
	userURL := env("USER_SERVICE_URL", "http://user-service:8082")
	jwtSecret := env("JWT_SECRET", "dev_secret_change_me")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := env("KAFKA_TOPIC", "group-events")

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

	if err := store.BootstrapSchema(ctx, dbPool, database.SetupSQL); err != nil {
		slog.Error("failed to bootstrap group schema", "error", err)
		os.Exit(1)
	}
	slog.Info("group schema ready")

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

	// 5. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("group-service")
	dbMetrics := metrics.NewDBPoolMetrics("group-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 6. Health checker
	checker := health.New("group-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 7. Dependencies
	groupStore := store.New(dbPool)
	groupSvc := service.New(groupStore, rdb, msgURL, postURL, userURL, jwtSecret)

	// 8. Kafka producer
	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

	producer := groupevents.NewProducerWithDialer(kafkaBrokers, kafkaTopic, rdb, kafkaDialer)
	groupSvc.SetProducer(producer)
	slog.Info("kafka producer initialized", "topic", kafkaTopic)

	// 9. Kafka consumer (GDPR + cache invalidation)
	consumer := groupevents.NewConsumerWithDialer(kafkaBrokers, "group-service-consumer", groupStore, rdb, kafkaDialer)
	consumerCtx, cancelConsumer := context.WithCancel(ctx)
	go consumer.Start(consumerCtx)
	slog.Info("kafka consumer started")

	groupHandler := http.New(groupSvc)

	// 10. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	groupHandler.RegisterRoutes(r)

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
