package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	dbschema "github.com/atpost/live-service/database"
	"github.com/atpost/live-service/internal/http"
	"github.com/atpost/live-service/internal/service"
	"github.com/atpost/live-service/internal/store/postgres"
	"github.com/atpost/live-service/internal/workers"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "live-service"})

	// 2. Config
	port := env("HTTP_PORT", "8099")
	pgDSN := os.Getenv("POSTGRES_DSN")
	kafkaBrokers := env("KAFKA_BROKERS", "redpanda:9092")
	kafkaBrokerList := splitAndClean(kafkaBrokers)
	redisAddr := env("REDIS_ADDR", "redis:6379")
	mediaConfig := service.StreamMediaConfig{
		PlaybackURLTemplate:     env("LIVE_PLAYBACK_URL_TEMPLATE", ""),
		PlaybackBaseURL:         env("LIVE_PLAYBACK_BASE_URL", ""),
		PlaybackInternalBaseURL: env("LIVE_PLAYBACK_INTERNAL_BASE_URL", ""),
		PlaybackProtocol:        env("LIVE_PLAYBACK_PROTOCOL", "hls"),
		PublishURLTemplate:      env("LIVE_PUBLISH_URL_TEMPLATE", ""),
		PublishInternalBaseURL:  env("LIVE_PUBLISH_INTERNAL_BASE_URL", ""),
		PublishProtocol:         env("LIVE_PUBLISH_PROTOCOL", "whip"),
		IngestURL:               env("LIVE_INGEST_URL", ""),
		IngestProtocol:          env("LIVE_INGEST_PROTOCOL", "rtmp"),
	}

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
	if err := postgres.BootstrapSchema(ctx, dbPool, dbschema.SetupSQL); err != nil {
		slog.Error("failed to bootstrap live schema", "error", err)
		os.Exit(1)
	}
	slog.Info("live schema ready")

	rdb, err := transport.NewRedisClientFromEnv(redisAddr)
	if err != nil {
		slog.Error("failed to configure redis client", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping failed", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to redis")

	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

	liveWriter := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  kafkaBrokerList,
		Topic:    "social.events.v1",
		Balancer: &kafka.LeastBytes{},
		Dialer:   kafkaDialer,
	})

	// 4. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("live-service")
	dbMetrics := metrics.NewDBPoolMetrics("live-service", "postgres")
	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 5. Health checker
	checker := health.New("live-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 6. Dependencies
	store := postgres.New(dbPool)
	svc := service.New(store, liveWriter, rdb, mediaConfig)
	defer svc.Close()
	handler := http.New(svc)

	// 6b. Background workers (shared Kafka writer for event publishing)
	workerKafkaWriter := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  kafkaBrokerList,
		Topic:    "social.events.v1",
		Balancer: &kafka.LeastBytes{},
		Dialer:   kafkaDialer,
	})
	defer workerKafkaWriter.Close()
	workers.StartAll(ctx, store, workerKafkaWriter)

	// 7. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)

	// 8. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			svc.Close()
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

func splitAndClean(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
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
