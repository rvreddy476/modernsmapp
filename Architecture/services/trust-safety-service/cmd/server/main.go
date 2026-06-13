package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/atpost/trust-safety-service/database"
	tsevents "github.com/atpost/trust-safety-service/internal/events"
	"github.com/atpost/trust-safety-service/internal/http"
	"github.com/atpost/trust-safety-service/internal/reconcile"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "trust-safety-service"})

	// 2. Config
	port := env("HTTP_PORT", "8091")
	pgDSN := os.Getenv("POSTGRES_DSN")
	kafkaBrokers := env("KAFKA_BROKERS", "redpanda:9092")

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

	if err := postgres.BootstrapSchema(ctx, dbPool, database.SetupSQL, database.Migrations); err != nil {
		slog.Error("failed to bootstrap trust-safety schema", "error", err)
		os.Exit(1)
	}
	slog.Info("trust-safety schema ready")

	// 4. Kafka writer
	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}
	kafkaWriter := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  []string{kafkaBrokers},
		Topic:    "social.events.v1",
		Balancer: &kafka.LeastBytes{},
		Dialer:   kafkaDialer,
	})
	defer kafkaWriter.Close()

	// 5. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("trust-safety-service")
	dbMetrics := metrics.NewDBPoolMetrics("trust-safety-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 6. Health checker
	checker := health.New("trust-safety-service")
	checker.Register("postgres", health.PingCheck(dbPool))

	// 7. Dependencies
	store := postgres.New(dbPool)
	svc := service.New(store, kafkaWriter)
	handler := http.New(svc)

	// 7b. Trust-score recompute job (spec §8.11/§10.1/§10.2) — read-only:
	// recomputes trust_score/trust_tier in trust.user_trust_state every 6h.
	trustStateStore := postgres.NewTrustStateStore(dbPool)
	go reconcile.NewTrustScoreReconciler(trustStateStore).Start(ctx)

	// 7c. Connection-request auto-filter (friends-sheets spec §5.1/§9.2 — P1.4).
	// Consumes ConnectionRequested events off the shared social.events.v1 topic,
	// scores each request against trust-safety's own data (shadowban / trust
	// score / prior reports), and on a filter decision pushes the request to
	// graph-service's hidden queue + emits ConnectionRequestFiltered back onto
	// social.events.v1 via the same kafkaWriter. Fail-open: errors are logged
	// and skipped, never crashing the consumer.
	connFilterStore := postgres.NewConnectionFilterStore(dbPool)
	graphClient := tsevents.NewGraphClient(
		env("GRAPH_SERVICE_URL", "http://graph-service:8083"),
		env("INTERNAL_SERVICE_KEY", ""),
	)
	socialConsumerMetrics := metrics.NewKafkaConsumerMetrics("trust-safety-service")
	socialConsumer := tsevents.NewSocialConsumer(
		[]string{kafkaBrokers},
		"social.events.v1",
		connFilterStore,
		graphClient,
		kafkaWriter,
		socialConsumerMetrics,
	)
	go socialConsumer.Start(ctx)
	slog.Info("connection-request auto-filter consumer started", "topic", "social.events.v1")

	// 8. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))
	r.Use(middleware.RequireInternalKey(env("INTERNAL_SERVICE_KEY", "")))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)

	// 9. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			socialConsumer.Close()
			kafkaWriter.Close()
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
