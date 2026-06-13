package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/atpost/ai-service/database"
	aihttp "github.com/atpost/ai-service/internal/http"
	"github.com/atpost/ai-service/internal/provider"
	"github.com/atpost/ai-service/internal/service"
	"github.com/atpost/ai-service/internal/store/postgres"
	"github.com/atpost/ai-service/internal/worker"
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
	logging.Init(logging.Config{ServiceName: "ai-service"})

	// 2. Config
	port := env("HTTP_PORT", "8098")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := env("REDIS_ADDR", "localhost:6379")
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")

	ctx := context.Background()

	// 3. Postgres
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
		slog.Error("failed to bootstrap ai schema", "error", err)
		os.Exit(1)
	}
	slog.Info("ai schema ready")

	// Ensure schema
	ensureSchema(ctx, dbPool)

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

	// 5. Provider selection: use Anthropic if key is set, otherwise stub.
	var textProvider provider.TextProvider
	var modProvider provider.ModerationProvider

	if anthropicKey != "" {
		slog.Info("ai-service: using Anthropic provider")
		ap := provider.NewAnthropicProvider(anthropicKey)
		textProvider = ap
		modProvider = ap
	} else {
		slog.Info("ai-service: ANTHROPIC_API_KEY not set - using stub provider")
		textProvider = provider.NewStubTextProvider()
		modProvider = provider.NewStubModerationProvider()
	}

	// 6. Dependencies
	pgStore := postgres.New(dbPool)
	aiSvc := service.NewWithProvider(pgStore, rdb, textProvider)

	// 7. Start async job worker.
	workerCtx, cancelWorker := context.WithCancel(ctx)
	jobWorker := worker.New(pgStore, textProvider, modProvider, 3)
	go jobWorker.Run(workerCtx)

	// 8. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("ai-service")
	dbMetrics := metrics.NewDBPoolMetrics("ai-service", "postgres")
	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 9. Health checker
	checker := health.New("ai-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 10. HTTP server
	handler := aihttp.New(aiSvc)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)

	slog.Info("ai-service starting", "port", port)

	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			cancelWorker()
			rdb.Close()
			dbPool.Close()
			slog.Info("ai-service shutdown complete")
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

func ensureSchema(ctx context.Context, db *pgxpool.Pool) {
	ddl := []string{
		`CREATE SCHEMA IF NOT EXISTS ai`,
		`CREATE TABLE IF NOT EXISTS ai.ai_jobs (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			job_type        TEXT NOT NULL,
			input_ref_type  TEXT NOT NULL,
			input_ref_id    UUID NOT NULL,
			requester_id    UUID,
			status          TEXT NOT NULL DEFAULT 'queued',
			result          JSONB,
			error_message   TEXT,
			model_version   TEXT,
			latency_ms      INT,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			completed_at    TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ai_jobs_type_status ON ai.ai_jobs(job_type, status, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_ai_jobs_ref ON ai.ai_jobs(input_ref_type, input_ref_id)`,
		`CREATE TABLE IF NOT EXISTS ai.moderation_ai_results (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			content_type    TEXT NOT NULL,
			content_id      UUID NOT NULL,
			text_score      REAL,
			image_score     REAL,
			flags           TEXT[],
			action          TEXT NOT NULL DEFAULT 'allow',
			model_version   TEXT,
			checked_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (content_type, content_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mod_results_content ON ai.moderation_ai_results(content_type, content_id)`,
		`CREATE INDEX IF NOT EXISTS idx_mod_results_action ON ai.moderation_ai_results(action, checked_at DESC)`,
	}
	for _, stmt := range ddl {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("schema migration", "error", err)
		}
	}
	slog.Info("ai schema ensured")
}
