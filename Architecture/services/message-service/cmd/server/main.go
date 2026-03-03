package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	apihttp "github.com/atpost/message-service/internal/http"
	"github.com/atpost/message-service/internal/kafka"
	"github.com/atpost/message-service/internal/policy"
	"github.com/atpost/message-service/internal/service"
	"github.com/atpost/message-service/internal/store/postgres"
	"github.com/atpost/message-service/internal/store/scylla"
	"github.com/atpost/message-service/internal/ws"
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
	logging.Init(logging.Config{ServiceName: "message-service"})

	// 2. Config
	port := env("HTTP_PORT", "8092")
	pgDSN := env("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/identity_db?sslmode=disable")
	scyllaHosts := env("SCYLLA_HOSTS", "scylla")
	redisAddr := env("REDIS_ADDR", "redis:6379")
	kafkaBrokers := env("KAFKA_BROKERS", "kafka:9092")
	jwtSecret := env("JWT_SECRET", "dev_secret_change_me")
	graphServiceURL := env("GRAPH_SERVICE_URL", "http://localhost:8083")

	ctx := context.Background()

	// 3. Database (Postgres)
	pgPool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer pgPool.Close()

	if err := pgPool.Ping(ctx); err != nil {
		slog.Error("postgres ping failed", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to postgres")

	// 3b. Schema migration -- pinned message columns (idempotent via ADD COLUMN IF NOT EXISTS)
	_, err = pgPool.Exec(ctx, `
		ALTER TABLE chat.conversations
		    ADD COLUMN IF NOT EXISTS pinned_message_id TEXT,
		    ADD COLUMN IF NOT EXISTS pinned_at         TIMESTAMPTZ,
		    ADD COLUMN IF NOT EXISTS pinned_by         UUID;
	`)
	if err != nil {
		slog.Error("failed to run pinned-message schema migration", "error", err)
		os.Exit(1)
	}
	slog.Info("pinned-message schema migration applied")

	// 4. Database (ScyllaDB)
	cluster := gocql.NewCluster(strings.Split(scyllaHosts, ",")...)
	cluster.Keyspace = "postbook"
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 5 * time.Second
	session, err := cluster.CreateSession()
	if err != nil {
		slog.Error("failed to connect to scylladb", "error", err, "hosts", scyllaHosts)
		os.Exit(1)
	}
	defer session.Close()
	slog.Info("connected to scylladb", "keyspace", "postbook")

	// 5. Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("failed to connect to redis", "error", err, "addr", redisAddr)
		os.Exit(1)
	}
	defer rdb.Close()
	slog.Info("connected to redis")

	// 6. Kafka Producer
	kp := kafka.NewProducer(strings.Split(kafkaBrokers, ","), "postbook-messages")
	defer kp.Close()
	slog.Info("connected to kafka", "topic", "postbook-messages")

	// 7. Dependencies
	scyllaStore := scylla.New(session)
	convStore := postgres.New(pgPool)
	dmPol := policy.NewDMPolicy(graphServiceURL)
	msgSvc := service.New(scyllaStore, convStore, rdb, kp, dmPol)
	msgHandler := apihttp.New(msgSvc, pgPool)

	// 8. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("message-service")
	dbMetrics := metrics.NewDBPoolMetrics("message-service", "postgres")

	go collectDBPoolStats(ctx, pgPool, dbMetrics)

	// 9. Health checker
	checker := health.New("message-service")
	checker.Register("postgres", health.PingCheck(pgPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))
	checker.Register("scylladb", health.ScyllaCheck(func(ctx context.Context) error {
		return session.Query("SELECT now() FROM system.local").Exec()
	}))

	// 10. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	msgHandler.RegisterRoutes(r)

	// WebSocket endpoint for real-time chat delivery
	wsHub := ws.NewHub(rdb, jwtSecret)
	r.GET("/v1/ws/connect", wsHub.HandleConnect)

	// 11. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			kp.Close()
			rdb.Close()
			session.Close()
			pgPool.Close()
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
