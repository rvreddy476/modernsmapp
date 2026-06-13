package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/atpost/suggestion-service/internal/events"
	handler "github.com/atpost/suggestion-service/internal/http"
	"github.com/atpost/suggestion-service/internal/service"
	"github.com/atpost/suggestion-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "suggestion-service"})

	// 2. Config
	port := env("HTTP_PORT", "8100")
	pgDSN := os.Getenv("POSTGRES_DSN")
	identityDSN := os.Getenv("IDENTITY_POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := env("KAFKA_BROKERS", "redpanda:9092")
	scyllaHosts := env("SCYLLA_HOSTS", "scylla:9042")

	ctx := context.Background()

	// 3. App DB pool (graph tables + suggestion tables)
	appPoolCfg, err := pgxpool.ParseConfig(pgDSN)
	if err != nil {
		slog.Error("parse app db config", "error", err)
		os.Exit(1)
	}
	appPoolCfg.MaxConns = 25
	appPoolCfg.MinConns = 5
	appPoolCfg.MaxConnLifetime = 15 * time.Minute
	appPoolCfg.MaxConnIdleTime = 5 * time.Minute
	appPool, err := pgxpool.NewWithConfig(ctx, appPoolCfg)
	if err != nil {
		slog.Error("app DB connect failed", "error", err)
		os.Exit(1)
	}
	defer appPool.Close()
	if err := appPool.Ping(ctx); err != nil {
		slog.Error("app DB ping failed", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to app database")

	// 4. Identity DB pool (read-only for profiles)
	identityPoolCfg, err := pgxpool.ParseConfig(identityDSN)
	if err != nil {
		slog.Error("parse identity db config", "error", err)
		os.Exit(1)
	}
	identityPoolCfg.MaxConns = 25
	identityPoolCfg.MinConns = 5
	identityPoolCfg.MaxConnLifetime = 15 * time.Minute
	identityPoolCfg.MaxConnIdleTime = 5 * time.Minute
	identityPool, err := pgxpool.NewWithConfig(ctx, identityPoolCfg)
	if err != nil {
		slog.Error("identity DB connect failed", "error", err)
		os.Exit(1)
	}
	defer identityPool.Close()
	if err := identityPool.Ping(ctx); err != nil {
		slog.Error("identity DB ping failed", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to identity database")

	// 5. Redis
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

	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

	// 6. ScyllaDB (optional — graceful degradation)
	var scyllaStore *store.ScyllaStore
	var scyllaSession *gocql.Session
	cluster := gocql.NewCluster(strings.Split(scyllaHosts, ",")...)
	cluster.Keyspace = "social_analytics"
	cluster.Consistency = gocql.LocalQuorum
	cluster.ConnectTimeout = 10 * time.Second
	cluster.Timeout = 5 * time.Second
	cluster.NumConns = 10
	cluster.MaxPreparedStmts = 1000
	scyllaSession, err = cluster.CreateSession()
	if err != nil {
		slog.Warn("scylladb connect failed, pair signals disabled", "error", err)
	} else {
		scyllaStore = store.NewScyllaStore(scyllaSession)
		defer scyllaSession.Close()
		slog.Info("connected to scylladb")
	}

	// 7. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("suggestion-service")
	appDBMetrics := metrics.NewDBPoolMetrics("suggestion-service", "app_postgres")
	identityDBMetrics := metrics.NewDBPoolMetrics("suggestion-service", "identity_postgres")

	go collectDBPoolStats(ctx, appPool, appDBMetrics)
	go collectDBPoolStats(ctx, identityPool, identityDBMetrics)

	// 8. Health checker
	checker := health.New("suggestion-service")
	checker.Register("app_postgres", health.PingCheck(appPool))
	checker.Register("identity_postgres", health.PingCheck(identityPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))
	if scyllaSession != nil {
		checker.Register("scylladb", health.ScyllaCheck(func(ctx context.Context) error {
			return scyllaSession.Query("SELECT now() FROM system.local").Exec()
		}))
	}

	// 9. Store + ensure schema
	suggStore := store.New(appPool, identityPool)
	if err := suggStore.EnsureSchema(ctx); err != nil {
		slog.Warn("schema ensure failed, tables may already exist", "error", err)
	}
	if err := suggStore.EnsureSchemaV2(ctx); err != nil {
		slog.Warn("schema V2 migration failed", "error", err)
	}

	// 10. Service
	suggSvc := service.New(suggStore, rdb)
	if scyllaStore != nil {
		suggSvc.SetScyllaStore(scyllaStore)
	}

	// 11. Event consumer (background)
	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer consumerCancel()
	consumer := events.NewConsumerWithDialer(
		strings.Split(kafkaBrokers, ","),
		"suggestion-service",
		"social.events.v1",
		rdb,
		suggSvc,
		suggStore,
		kafkaDialer,
	)
	go consumer.Start(consumerCtx)
	defer consumer.Close()

	// 12. Tiered background batch jobs
	go runTieredBatchJobs(ctx, suggSvc)

	// 13. HTTP handler + routes
	h := handler.New(suggSvc)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	h.RegisterRoutes(r)

	// 14. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			consumerCancel()
			consumer.Close()
			rdb.Close()
			if scyllaSession != nil {
				scyllaSession.Close()
			}
			identityPool.Close()
			appPool.Close()
			slog.Info("cleanup completed")
		},
	}); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

// runTieredBatchJobs runs the 3 tiered batch jobs on separate schedules.
func runTieredBatchJobs(ctx context.Context, svc *service.Service) {
	// Initial delay to let the service warm up
	time.Sleep(30 * time.Second)

	hotTicker := time.NewTicker(1 * time.Hour)
	fullTicker := time.NewTicker(6 * time.Hour)
	coldTicker := time.NewTicker(24 * time.Hour)
	defer hotTicker.Stop()
	defer fullTicker.Stop()
	defer coldTicker.Stop()

	for {
		select {
		case <-hotTicker.C:
			slog.Info("[cron] starting hot signals refresh")
			batchCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			if err := svc.RunHotSignalsRefresh(batchCtx); err != nil {
				slog.Error("[cron] hot signals error", "error", err)
			}
			cancel()

		case <-fullTicker.C:
			slog.Info("[cron] starting full candidate generation")
			batchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			if err := svc.RunFriendCandidatesFull(batchCtx); err != nil {
				slog.Error("[cron] friend candidates error", "error", err)
			}
			if err := svc.RunFollowCandidatesFull(batchCtx); err != nil {
				slog.Error("[cron] follow candidates error", "error", err)
			}
			cancel()

		case <-coldTicker.C:
			slog.Info("[cron] starting cold signals refresh")
			batchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			if err := svc.RunColdSignalsRefresh(batchCtx); err != nil {
				slog.Error("[cron] cold signals error", "error", err)
			}
			cancel()

		case <-ctx.Done():
			return
		}
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
