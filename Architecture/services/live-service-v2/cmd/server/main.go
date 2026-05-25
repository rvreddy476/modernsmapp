package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	dbschema "github.com/atpost/live-service-v2/database"
	v2events "github.com/atpost/live-service-v2/internal/events"
	v2http "github.com/atpost/live-service-v2/internal/http"
	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/service"
	pgstore "github.com/atpost/live-service-v2/internal/store/postgres"

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
	logging.Init(logging.Config{ServiceName: "live-service-v2"})

	port := env("HTTP_PORT", "8095")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := env("REDIS_ADDR", "redis:6379")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := env("KAFKA_TOPIC", "social.events.v1")

	graphURL := env("GRAPH_SERVICE_URL", "")
	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
	egressSecret := os.Getenv("LIVEKIT_WEBHOOK_SECRET")

	lkCfg := livekit.Config{
		APIKey:      os.Getenv("LIVEKIT_API_KEY"),
		APISecret:   os.Getenv("LIVEKIT_API_SECRET"),
		URL:         env("LIVEKIT_URL", "ws://livekit:7880"),
		S3Endpoint:  env("MINIO_ENDPOINT", "http://minio:9000"),
		S3AccessKey: os.Getenv("MINIO_ACCESS_KEY"),
		S3SecretKey: os.Getenv("MINIO_SECRET_KEY"),
		S3Bucket:    env("MINIO_BUCKET_LIVE_RECORDINGS", "live-recordings"),
		S3Region:    env("MINIO_REGION", "us-east-1"),
		S3UseSSL:    envBool("MINIO_USE_SSL", false),
	}

	ctx := context.Background()

	// --- Postgres ---
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
		slog.Error("postgres connect", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()
	if err := dbPool.Ping(ctx); err != nil {
		slog.Error("postgres ping", "error", err)
		os.Exit(1)
	}
	if err := pgstore.BootstrapSchema(ctx, dbPool, dbschema.SetupSQL); err != nil {
		slog.Error("bootstrap schema", "error", err)
		os.Exit(1)
	}
	slog.Info("live-v2 schema ready")

	// --- Redis ---
	rdb, err := transport.NewRedisClientFromEnv(redisAddr)
	if err != nil {
		slog.Error("redis init", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping", "error", err)
		os.Exit(1)
	}

	// --- Kafka ---
	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("kafka dialer", "error", err)
		os.Exit(1)
	}
	producer := v2events.NewProducer(kafkaBrokers, kafkaTopic, kafkaDialer)
	defer producer.Close()

	// --- LiveKit + graph clients ---
	lk := livekit.New(lkCfg)
	graph := service.NewHTTPGraphClient(graphURL, internalKey)

	store := pgstore.New(dbPool)
	svc := service.New(store, lk, graph, producer, rdb, service.Config{
		RecordingPublicBaseURL: env("LIVE_RECORDING_PUBLIC_BASE_URL", ""),
		S3Bucket:               lkCfg.S3Bucket,
		S3Endpoint:             lkCfg.S3Endpoint,
	})

	handler := v2http.New(svc)
	if internalKey != "" {
		handler.WithInternalKey(internalKey)
		slog.Info("live-v2: internal-service-key gate enabled")
	} else {
		slog.Warn("live-v2: INTERNAL_SERVICE_KEY not set — every v1 endpoint is unauthenticated. Do not run this configuration in production.")
	}
	if egressSecret != "" {
		handler.WithEgressSecret(egressSecret)
	} else {
		slog.Warn("live-v2: LIVEKIT_WEBHOOK_SECRET not set — egress webhook will accept any payload")
	}

	// --- Prometheus + health ---
	httpMetrics := metrics.NewHTTPMetrics("live-service-v2")
	checker := health.New("live-service-v2")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)

	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			_ = producer.Close()
			dbPool.Close()
			rdb.Close()
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

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
