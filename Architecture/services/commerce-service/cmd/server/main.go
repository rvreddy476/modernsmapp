package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/commerce-service/database"
	"github.com/atpost/commerce-service/internal/consumers"
	"github.com/atpost/commerce-service/internal/courier"
	commercehttp "github.com/atpost/commerce-service/internal/http"
	"github.com/atpost/commerce-service/internal/identity"
	"github.com/atpost/commerce-service/internal/kyc"
	"github.com/atpost/commerce-service/internal/payments"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/blob"
	pgstore "github.com/atpost/commerce-service/internal/store/postgres"
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
	logging.Init(logging.Config{ServiceName: "commerce-service"})

	// 2. Config
	port := env("HTTP_PORT", "8109")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")

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

	// 5. Kafka dialer
	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

	// 6. Bootstrap schema + run migrations
	if err := pgstore.BootstrapSchema(ctx, dbPool, database.SetupSQL); err != nil {
		slog.Error("failed to bootstrap commerce schema", "error", err)
		os.Exit(1)
	}
	if err := pgstore.RunMigrations(ctx, dbPool, database.Migrations); err != nil {
		slog.Error("failed to run commerce migrations", "error", err)
		os.Exit(1)
	}
	slog.Info("commerce schema ready")

	// 7. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("commerce-service")
	dbMetrics := metrics.NewDBPoolMetrics("commerce-service", "postgres")
	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 8. Health checker
	checker := health.New("commerce-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 9. Service (+ courier + invoice blob store)
	store := pgstore.New(dbPool)
	svc := service.NewWithDialer(store, rdb, strings.Join(kafkaBrokers, ","), kafkaDialer)
	defer svc.Close()

	// Courier provider (stub in dev, shiprocket in prod). Env COURIER_PROVIDER selects.
	svc.WithCourier(courier.New())

	// MinIO for invoice HTML storage.
	minioEndpoint := env("MINIO_ENDPOINT", "minio:9000")
	minioAccess := env("MINIO_ACCESS_KEY", "minioadmin")
	minioSecret := env("MINIO_SECRET_KEY", "minioadmin")
	minioBucket := env("COMMERCE_INVOICE_BUCKET", "commerce-invoices")
	minioSSL := env("MINIO_USE_SSL", "false") == "true"
	minioPublic := os.Getenv("MINIO_PUBLIC_ENDPOINT")
	if blobStore, err := blob.New(minioEndpoint, minioAccess, minioSecret, minioBucket, minioSSL, minioPublic); err != nil {
		slog.Warn("invoice blob store unavailable; invoices will fail until fixed", "error", err)
	} else {
		svc.WithBlob(blobStore)
		slog.Info("invoice blob store ready", "bucket", minioBucket)
	}

	// Auth-service client for resolving buyer email in commerce events.
	authURL := env("AUTH_SERVICE_URL", "http://auth-service:8081")
	svc.WithIdentity(identity.New(authURL, internalKey))
	slog.Info("identity client ready", "auth_url", authURL)

	// Payments-service client for refund initiation on return approval.
	paymentsURL := env("PAYMENTS_SERVICE_URL", "http://payments-service:8102")
	svc.WithPayments(payments.New(paymentsURL, internalKey))
	slog.Info("payments client ready", "payments_url", paymentsURL)

	// KYC validator (Phase 3.2). The stub does format-only checks and tags
	// every verdict with Source="stub" so admins know they're approving on
	// incomplete verification. Wire a vendor (Karza/Signzy/Hyperverge)
	// adapter here once the commercial integration is signed.
	svc.WithKYC(kyc.StubValidator{})
	slog.Info("kyc validator ready", "adapter", "stub")

	// 10. Kafka consumer: react to payment lifecycle events from payments-service.
	// Confirms orders on payment.succeeded; releases stock reservations on
	// payment.failed. Started in a goroutine; cancelled via consumerCtx on shutdown.
	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer consumerCancel()
	kafkaMetrics := metrics.NewKafkaConsumerMetrics("commerce-service")
	paymentsConsumer := consumers.NewPaymentsConsumer(svc, kafkaBrokers, rdb, kafkaMetrics)
	go paymentsConsumer.Start(consumerCtx)
	slog.Info("payments consumer started")

	// 11. HTTP handler
	handler := commercehttp.New(svc).WithInternalKey(internalKey)

	// 11. Gin engine
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)

	// 12. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			consumerCancel()
			_ = paymentsConsumer.Close()
			svc.Close()
			rdb.Close()
			dbPool.Close()
			slog.Info("commerce-service shutdown complete")
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
