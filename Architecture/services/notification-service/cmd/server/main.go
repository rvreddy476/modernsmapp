package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/events"
	"github.com/atpost/notification-service/internal/http"
	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/atpost/notification-service/internal/workers"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "notification-service"})

	// 2. Config
	port := env("HTTP_PORT", "8088")
	scyllaHosts := env("SCYLLA_HOSTS", "scylla")
	redisAddr := env("REDIS_ADDR", "redis:6379")
	kafkaBrokers := env("KAFKA_BROKERS", "redpanda:9092")

	ctx := context.Background()

	// 3. Database (Scylla)
	cluster := gocql.NewCluster(strings.Split(scyllaHosts, ",")...)
	cluster.Keyspace = "social_notify"
	cluster.Consistency = gocql.Quorum
	session, err := cluster.CreateSession()
	if err != nil {
		slog.Error("failed to connect to scylla", "error", err)
		os.Exit(1)
	}
	defer session.Close()
	slog.Info("connected to scylla")

	// 4. Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping failed", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()
	slog.Info("connected to redis")

	// 5. Database (Postgres -- for preferences & devices)
	pgDSN := os.Getenv("POSTGRES_DSN")
	var pgStore *postgres.Store
	var dbPool *pgxpool.Pool
	if pgDSN != "" {
		pool, err := pgxpool.New(ctx, pgDSN)
		if err != nil {
			slog.Warn("unable to connect to postgres (preferences disabled)", "error", err)
		} else {
			dbPool = pool
			defer dbPool.Close()
			if err := dbPool.Ping(ctx); err != nil {
				slog.Warn("postgres ping failed", "error", err)
			} else {
				slog.Info("connected to postgres")
				pgStore = postgres.New(dbPool)
				ensureNotifSchema(ctx, dbPool)
			}
		}
	}

	// 6. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("notification-service")

	if dbPool != nil {
		dbMetrics := metrics.NewDBPoolMetrics("notification-service", "postgres")
		go collectDBPoolStats(ctx, dbPool, dbMetrics)
	}

	// 7. Health checker
	checker := health.New("notification-service")
	checker.Register("scylladb", health.ScyllaCheck(func(ctx context.Context) error {
		return session.Query("SELECT now() FROM system.local").Exec()
	}))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))
	if dbPool != nil {
		checker.Register("postgres", health.PingCheck(dbPool))
	}

	// 8. Dependencies
	scyllaStore := scylla.New(session)
	notifSvc := service.New(scyllaStore, rdb)
	if pgStore != nil {
		notifSvc.SetPGStore(pgStore)
	}
	notifHandler := http.New(notifSvc, rdb)

	// 9. Kafka Consumers
	consumer := events.NewConsumer(
		strings.Split(kafkaBrokers, ","),
		"notification-service-group",
		"social.events.v1",
		notifSvc,
	)
	go consumer.Start(ctx)
	slog.Info("kafka consumer started", "topic", "social.events.v1")

	callConsumer := events.NewCallConsumer(
		strings.Split(kafkaBrokers, ","),
		"notification-service-calls-group",
		env("KAFKA_CALL_NOTIFICATIONS_TOPIC", "call.notifications"),
		notifSvc,
	)
	go callConsumer.Start(ctx)
	slog.Info("kafka call consumer started", "topic", "call.notifications")

	// 9b. Background workers
	go workers.StartCleanupWorker(ctx, session)
	go workers.StartReconciliationWorker(ctx, session, rdb)
	if dbPool != nil && pgStore != nil {
		go workers.StartDigestWorker(ctx, dbPool, pgStore, session)
		slog.Info("digest worker started")
	}
	slog.Info("background workers started")

	// 10. HTTP Server
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	notifHandler.RegisterRoutes(r)

	// 11. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			rdb.Close()
			session.Close()
			if dbPool != nil {
				dbPool.Close()
			}
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

// ensureNotifSchema creates Postgres tables for notification preferences and devices.
func ensureNotifSchema(ctx context.Context, db *pgxpool.Pool) {
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS notification_preferences (
			user_id          UUID PRIMARY KEY,
			email_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
			push_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
			sms_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
			quiet_hours_start TIME,
			quiet_hours_end   TIME,
			muted_types      JSONB NOT NULL DEFAULT '[]',
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS user_devices (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id    UUID NOT NULL,
			platform   TEXT NOT NULL CHECK (platform IN ('ios', 'android', 'web')),
			push_token TEXT NOT NULL,
			is_active  BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(user_id, platform, push_token)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_devices_user ON user_devices (user_id) WHERE is_active = TRUE`,
	}
	for _, stmt := range ddl {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("notification schema migration", "error", err)
		}
	}
	slog.Info("notification preferences schema ensured")
}
