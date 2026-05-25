package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
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
	"github.com/atpost/commerce-service/internal/workers"
	"github.com/atpost/shared/counters"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	tracepkg "github.com/atpost/shared/o11y/trace"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "commerce-service"})

	// 1a. Tracing (Phase F3.5) — OTLP/gRPC exporter to Jaeger. Falls
	// back to a no-op provider if the collector is unreachable so the
	// service still boots in environments without observability infra.
	tracerProvider, _ := tracepkg.InitTracer("commerce-service", env("OTEL_EXPORTER_OTLP_ENDPOINT", "http://jaeger:4317"))
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tracerProvider.Shutdown(shutdownCtx)
	}()

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
	// HS1: pool sizing is env-tunable so prod can scale beyond the
	// 25/5 dev defaults without a code change. Tracks the same pattern
	// notification-service uses; both services contend on app_db.
	poolCfg.MaxConns = int32(envInt("POSTGRES_MAX_CONNS", 25))
	poolCfg.MinConns = int32(envInt("POSTGRES_MIN_CONNS", 5))
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

	// Payout fee schedule (Phase 4.1) — env-overridable so finance can
	// change commission / platform fee / TDS without a code change. Bad
	// values are clamped to the historical defaults inside WithPayoutConfig.
	svc.WithPayoutConfig(service.PayoutConfig{
		CommissionPct:  envFloat("COMMERCE_COMMISSION_PCT", 5.0),
		PlatformFeePct: envFloat("COMMERCE_PLATFORM_FEE_PCT", 2.0),
		TDSPct:         envFloat("COMMERCE_TDS_PCT", 1.0),
	})
	slog.Info("payout config ready",
		"commission_pct", env("COMMERCE_COMMISSION_PCT", "5.0"),
		"platform_fee_pct", env("COMMERCE_PLATFORM_FEE_PCT", "2.0"),
		"tds_pct", env("COMMERCE_TDS_PCT", "1.0"))

	// 10. Kafka consumer: react to payment lifecycle events from payments-service.
	// Confirms orders on payment.succeeded; releases stock reservations on
	// payment.failed. Started in a goroutine; cancelled via consumerCtx on shutdown.
	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer consumerCancel()
	kafkaMetrics := metrics.NewKafkaConsumerMetrics("commerce-service")
	paymentsConsumer := consumers.NewPaymentsConsumer(svc, kafkaBrokers, rdb, kafkaMetrics)
	go paymentsConsumer.Start(consumerCtx)
	slog.Info("payments consumer started")

	// Phase 6.1 — durable fulfillment worker. Replaces the old
	// `go s.fulfillPaidOrder()` goroutines that disappeared on restart.
	fulfillmentWorker := workers.NewFulfillmentWorker(store, svc)
	go fulfillmentWorker.Run(consumerCtx)
	slog.Info("fulfillment worker started")

	// Phase 6.2 — inventory reservation expiry sweeper. Runs every minute
	// and frees cart reservations whose 30-minute hold has elapsed so
	// inventory doesn't stay locked behind abandoned checkouts.
	go runInventoryExpiry(consumerCtx, store)
	slog.Info("inventory expiry worker started")

	// Sharded product-view counter flush. At trending-product scale
	// (100k+ views/hour) every UPDATE products SET view_count=… on the
	// same row was a contention point; the counter spreads across 32
	// Redis shards and materialises back to PG every ~10s.
	if pvc := svc.ProductViewCounter(); pvc != nil {
		flush := func(ctx context.Context, productIDStr string, total int64) error {
			id, err := uuid.Parse(productIDStr)
			if err != nil {
				return err
			}
			return store.SetProductViewCount(ctx, id, total)
		}
		go counters.NewWorker(pvc, flush, counters.WorkerOptions{}).Start(consumerCtx)
		slog.Info("commerce product view-count sharded flush worker started")
	}

	// 11. HTTP handler
	handler := commercehttp.New(svc).WithInternalKey(internalKey)

	// 11. Gin engine
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	// Phase F3 — tracing middleware runs FIRST so the span context is
	// available to RequestID + Logger for correlation. Order matters:
	// otel → request-id → logger so logs carry trace_id + span_id.
	r.Use(middleware.OtelTracing("commerce-service"))
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

// runInventoryExpiry sweeps expired cart reservations every minute so
// abandoned checkouts free their hold for other shoppers. Phase 6.2.
func runInventoryExpiry(ctx context.Context, store *pgstore.Store) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := store.ExpireInventoryReservations(ctx); err != nil {
				slog.Warn("inventory expiry sweep failed", "error", err)
			} else if n > 0 {
				slog.Info("inventory expiry sweep", "freed", n)
			}
		}
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		slog.Warn("invalid float env, using fallback", "key", key, "value", raw, "fallback", fallback)
		return fallback
	}
	return v
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("invalid int env, using fallback", "key", key, "value", raw, "fallback", fallback)
		return fallback
	}
	return v
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
