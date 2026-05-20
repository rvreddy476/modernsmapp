package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/atpost/payments-service/database"
	"github.com/atpost/payments-service/internal/gateway"
	nethttp "github.com/atpost/payments-service/internal/http"
	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	tracepkg "github.com/atpost/shared/o11y/trace"
	sharedserver "github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	logging.Init(logging.Config{ServiceName: "payments-service"})

	// Phase F3.5 — tracing init. See commerce-service for the rationale.
	tracerProvider, _ := tracepkg.InitTracer("payments-service", env("OTEL_EXPORTER_OTLP_ENDPOINT", "http://jaeger:4317"))
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tracerProvider.Shutdown(shutdownCtx)
	}()

	port := env("HTTP_PORT", "8102")
	pgDSN := os.Getenv("POSTGRES_DSN")
	kafkaBrokers := env("KAFKA_BROKERS", "localhost:9092")

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
		slog.Error("failed to bootstrap payments schema", "error", err)
		os.Exit(1)
	}
	slog.Info("payments schema ready")

	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

	httpMetrics := metrics.NewHTTPMetrics("payments-service")
	dbMetrics := metrics.NewDBPoolMetrics("payments-service", "postgres")
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stat := dbPool.Stat()
				dbMetrics.Update(metrics.PgxPoolStat{
					AcquireCount:  stat.AcquireCount(),
					AcquiredConns: int32(stat.AcquiredConns()),
					IdleConns:     int32(stat.IdleConns()),
					TotalConns:    int32(stat.TotalConns()),
					MaxConns:      stat.MaxConns(),
				})
			}
		}
	}()

	checker := health.New("payments-service")
	checker.Register("postgres", health.PingCheck(dbPool))

	store := postgres.New(dbPool)

	// Select payment gateway. Audit P8: previously a missing
	// RAZORPAY_KEY_ID silently selected the stub — production deploys
	// that forgot the env ran with a stub that never moved real money
	// (matches media-service H8 stub-in-prod pattern). Now require
	// PAYMENTS_ALLOW_STUB=true to opt into the stub explicitly; if
	// neither real creds nor the opt-in are set, refuse to start so
	// the misconfiguration is visible at boot.
	var gw gateway.PaymentGateway
	if keyID := os.Getenv("RAZORPAY_KEY_ID"); keyID != "" {
		gw = gateway.NewRazorpayGateway(keyID, os.Getenv("RAZORPAY_KEY_SECRET"))
		slog.Info("payments: using Razorpay gateway (production credentials detected)")
	} else if os.Getenv("PAYMENTS_ALLOW_STUB") == "true" {
		gw = &gateway.StubGateway{}
		slog.Warn("payments: STUB GATEWAY ACTIVE — no real money will move. Set RAZORPAY_KEY_ID + RAZORPAY_KEY_SECRET in production and remove PAYMENTS_ALLOW_STUB.")
	} else {
		slog.Error("payments: RAZORPAY_KEY_ID is required in production; set PAYMENTS_ALLOW_STUB=true for dev/test")
		os.Exit(1)
	}

	webhookSecret := os.Getenv("RAZORPAY_WEBHOOK_SECRET")
	if webhookSecret == "" && os.Getenv("PAYMENTS_ALLOW_STUB") != "true" {
		slog.Error("payments: RAZORPAY_WEBHOOK_SECRET is required when running with the Razorpay gateway")
		os.Exit(1)
	}

	svc := service.NewWithDialer(store, kafkaBrokers, gw, kafkaDialer)
	handler := nethttp.New(svc).WithWebhookSecret(webhookSecret)
	// Audit P-internal: gate /v1/payments/* behind the shared internal-
	// service-key. /webhook is registered outside this gate inside
	// handler.RegisterRoutes (audit P5). Empty key keeps dev unblocked
	// behind a loud WARN, matching every other service in the platform.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		handler.WithInternalKey(key)
		slog.Info("payments-service: internal-service-key gate enabled")
	} else {
		slog.Warn("payments-service: INTERNAL_SERVICE_KEY not set — /v1/payments endpoints are unauthenticated. Do not run this configuration in production.")
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.OtelTracing("payments-service"))
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))
	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)

	sharedserver.Run(r, sharedserver.Config{
		Port: port,
		OnShutdown: func() {
			dbPool.Close()
		},
	})
}
