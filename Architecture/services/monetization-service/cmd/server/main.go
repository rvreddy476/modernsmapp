package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/monetization-service/database"
	"github.com/atpost/monetization-service/internal/events"
	"github.com/atpost/monetization-service/internal/http"
	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/monetization-service/internal/workers"
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
	logging.Init(logging.Config{ServiceName: "monetization-service"})

	// 2. Config
	port := env("HTTP_PORT", "8099")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")

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

	if err := postgres.BootstrapSchema(ctx, dbPool, database.SetupSQL); err != nil {
		slog.Error("failed to bootstrap monetization schema", "error", err)
		os.Exit(1)
	}
	slog.Info("monetization schema ready")

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

	// W2 — schema is fully owned by setup.sql via BootstrapSchema above.
	// The previous ensureSchema() duplicated all of setup.sql's DDL and
	// defined `wallets` as a TABLE (the latent footgun behind the
	// 2026-05-13 crash loop). Everything in setup.sql is idempotent +
	// IF NOT EXISTS-guarded, so a second pass isn't needed.

	// 5. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("monetization-service")
	dbMetrics := metrics.NewDBPoolMetrics("monetization-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 6. Health checker
	checker := health.New("monetization-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 7. Dependencies
	monetizationStore := postgres.New(dbPool)
	monetizationSvc := service.New(monetizationStore, rdb).
		WithCreatorFundConfig(loadCreatorFundConfig())
	monetizationHandler := http.New(monetizationSvc)

	// 7a. Kafka producer + background workers
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "localhost:9092"), ",")
	kafkaTopic := env("KAFKA_MONETIZATION_TOPIC", events.TopicMonetization)
	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}
	monetizationProducer := events.NewProducerWithDialer(kafkaBrokers, kafkaTopic, kafkaDialer)
	defer monetizationProducer.Close()

	// Tier 1a: producer is also the entitlement-publisher seam, used
	// by Subscribe/Unsubscribe to invalidate post-service's cache.
	monetizationSvc.WithEntitlementPublisher(monetizationProducer)

	go workers.StartAll(ctx, monetizationStore, monetizationProducer, monetizationSvc)

	// 8. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	monetizationHandler.RegisterRoutes(r)

	// 9. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
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

// loadCreatorFundConfig assembles the creator-fund knob set from CF_*
// env variables, falling back to the launch defaults baked into the
// service package. Negative or unparseable values are ignored.
func loadCreatorFundConfig() service.CreatorFundConfig {
	cfg := service.DefaultCreatorFundConfig()
	if v := os.Getenv("CF_ELIGIBILITY_VIEW_SCORE"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 {
			cfg.EligibilityViewScore = n
		}
	}
	if v := os.Getenv("CF_ELIGIBILITY_WATCH_MS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			cfg.EligibilityWatchTimeMs = n
		}
	}
	if v := os.Getenv("CF_ELIGIBILITY_CONTENT_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.EligibilityContentCount = n
		}
	}
	if v := os.Getenv("CF_PLATFORM_FEE_BPS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 && n <= 10_000 {
			cfg.PlatformFeeBps = n
		}
	}
	return cfg
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
