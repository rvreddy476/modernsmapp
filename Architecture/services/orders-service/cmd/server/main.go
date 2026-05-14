package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/atpost/orders-service/database"
	nethttp "github.com/atpost/orders-service/internal/http"
	"github.com/atpost/orders-service/internal/service"
	"github.com/atpost/orders-service/internal/store/postgres"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
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
	logging.Init(logging.Config{ServiceName: "orders-service"})

	port := env("HTTP_PORT", "8101")
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
		slog.Error("failed to bootstrap orders schema", "error", err)
		os.Exit(1)
	}
	slog.Info("orders schema ready")

	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

	httpMetrics := metrics.NewHTTPMetrics("orders-service")
	dbMetrics := metrics.NewDBPoolMetrics("orders-service", "postgres")
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

	checker := health.New("orders-service")
	checker.Register("postgres", health.PingCheck(dbPool))

	store := postgres.New(dbPool)
	svc := service.NewWithDialer(store, kafkaBrokers, kafkaDialer)
	handler := nethttp.New(svc)
	// Audit O1: same internal-key wiring as the rest of the platform.
	// Without this every /v1/orders endpoint, including UpdateOrderStatus
	// and ResolveDispute, was reachable directly.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		handler.WithInternalKey(key)
		slog.Info("orders-service: internal-service-key gate enabled")
	} else {
		slog.Warn("orders-service: INTERNAL_SERVICE_KEY not set — every endpoint is unauthenticated. Do not run this configuration in production.")
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
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
