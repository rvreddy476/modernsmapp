package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/food-service/database"
	foodhttp "github.com/atpost/food-service/internal/http"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/outbox"
	"github.com/atpost/shared/realtime"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "food-service"})

	port := env("HTTP_PORT", "8113")
	pgDSN := os.Getenv("POSTGRES_DSN")
	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := env("KAFKA_TOPIC", "food-events")

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
	if err := postgres.BootstrapSchema(ctx, dbPool, database.SetupSQL); err != nil {
		slog.Error("failed to bootstrap food schema", "error", err)
		os.Exit(1)
	}
	slog.Info("food schema ready")

	httpMetrics := metrics.NewHTTPMetrics("food-service")
	dbMetrics := metrics.NewDBPoolMetrics("food-service", "postgres")
	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	checker := health.New("food-service")
	checker.Register("postgres", health.PingCheck(dbPool))

	store := postgres.New(dbPool)
	svc := service.New(store)

	// Realtime: best-effort Pub/Sub publishes + topic-token signer.
	// REALTIME_TOKEN_SECRET must match notification-service's verifier.
	if rtSecret := env("REALTIME_TOKEN_SECRET", internalKey); rtSecret != "" {
		redisAddr := os.Getenv("REDIS_ADDR")
		if rdb, err := transport.NewRedisClientFromEnv(redisAddr); err == nil {
			svc.WithRealtime(
				realtime.NewPublisher(rdb),
				realtime.NewTokenSigner([]byte(rtSecret)),
			)
			slog.Info("food-service realtime wired", "redis", redisAddr)
		} else {
			slog.Warn("food-service: redis unavailable, realtime disabled", "error", err)
		}
	}

	// P0.3 — durable outbox publisher. Domain events PlaceOrder /
	// ConfirmPayment / CancelOrder enqueue here (via service.emit);
	// this publisher drains the table and retries on Kafka outage.
	outboxCtx, outboxCancel := context.WithCancel(ctx)
	defer outboxCancel()
	outboxPublisher := outbox.New(dbPool, outbox.Config{
		KafkaBrokers: strings.Join(kafkaBrokers, ","),
		DefaultTopic: kafkaTopic,
	})
	go outboxPublisher.Run(outboxCtx)
	slog.Info("outbox publisher started", "topic", kafkaTopic)

	svc.WithOutbox(outbox.NewQueuer(""), dbPool)

	handler := foodhttp.New(svc).WithInternalKey(internalKey)

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
