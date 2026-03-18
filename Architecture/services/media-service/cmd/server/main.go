package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	mediaEvents "github.com/atpost/media-service/internal/events"
	mediaHttp "github.com/atpost/media-service/internal/http"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "media-service"})

	// 2. Config
	port := env("HTTP_PORT", "8087")
	pgDSN := os.Getenv("POSTGRES_DSN")
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		slog.Error("JWT_SECRET environment variable is required")
		os.Exit(1)
	}

	minioEndpoint := os.Getenv("MINIO_ENDPOINT")
	minioAccessKey := os.Getenv("MINIO_ACCESS_KEY")
	minioSecretKey := os.Getenv("MINIO_SECRET_KEY")
	minioBucket := os.Getenv("MINIO_BUCKET")
	minioUseSSL := os.Getenv("MINIO_USE_SSL") == "true"
	minioPublicEndpoint := os.Getenv("MINIO_PUBLIC_ENDPOINT") // e.g. http://localhost:9000
	kafkaBrokers := env("KAFKA_BROKERS", "kafka:9092")

	if minioEndpoint == "" {
		minioEndpoint = "minio:9000"
		minioAccessKey = "minioadmin"
		minioSecretKey = "minioadmin"
		minioBucket = "media"
	}

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

	// 4. Blob Store (MinIO)
	blobStore, err := blob.NewWithPublicEndpoint(minioEndpoint, minioAccessKey, minioSecretKey, minioBucket, minioUseSSL, minioPublicEndpoint)
	if err != nil {
		slog.Error("failed to connect to minio", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to minio")

	// 4b. Redis (for upload rate limiting)
	redisAddr := env("REDIS_ADDR", "redis:6379")
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Warn("redis not available, upload rate limiting disabled", "error", err)
		rdb = nil
	} else {
		slog.Info("connected to redis", "addr", redisAddr)
	}

	// 5. Dependencies
	pgStore := postgres.New(dbPool)
	mediaSvc := service.New(pgStore, blobStore)
	if rdb != nil {
		mediaSvc.SetRedis(rdb)
	}

	// 6. Kafka producer for video transcode events
	brokers := strings.Split(kafkaBrokers, ",")
	producer := mediaEvents.NewProducer(brokers, "media.events")
	defer producer.Close()
	mediaSvc.SetProducer(producer)
	slog.Info("kafka producer initialized")

	// 7. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("media-service")
	dbMetrics := metrics.NewDBPoolMetrics("media-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 8. Health checker
	checker := health.New("media-service")
	checker.Register("postgres", health.PingCheck(dbPool))

	// 9. HTTP Server with middleware
	authMW := mediaHttp.AuthMiddleware(jwtSecret)
	optionalAuthMW := mediaHttp.OptionalAuthMiddleware(jwtSecret)
	mediaHandler := mediaHttp.New(mediaSvc)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	mediaHandler.RegisterRoutes(r, authMW, optionalAuthMW)
	mediaHandler.RegisterAudioRoutes(r, authMW)
	mediaHandler.RegisterClipsRoutes(r, authMW)
	mediaHandler.RegisterRenditionRoutes(r, authMW)
	mediaHandler.RegisterResumableRoutes(r, authMW)
	mediaHandler.RegisterSlotRoutes(r, authMW)
	mediaHandler.RegisterStudioRoutes(r, authMW)

	// 10. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			producer.Close()
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
