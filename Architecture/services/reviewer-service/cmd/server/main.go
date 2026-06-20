package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/atpost/reviewer-service/database"
	"github.com/atpost/reviewer-service/internal/clients"
	"github.com/atpost/reviewer-service/internal/prefilter"
	reviewerhttp "github.com/atpost/reviewer-service/internal/http"
	"github.com/atpost/reviewer-service/internal/service"
	"github.com/atpost/reviewer-service/internal/store/postgres"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "reviewer-service"})

	port := env("HTTP_PORT", "8120")
	pgDSN := os.Getenv("POSTGRES_DSN")

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
	if err := postgres.BootstrapSchema(ctx, dbPool, database.SetupSQL, database.Migrations); err != nil {
		slog.Error("failed to bootstrap reviewer schema", "error", err)
		os.Exit(1)
	}
	slog.Info("reviewer schema ready")

	httpMetrics := metrics.NewHTTPMetrics("reviewer-service")

	internalKey := env("INTERNAL_SERVICE_KEY", "")
	cl := clients.New(
		env("GRAPH_SERVICE_URL", "http://graph-service:8083"),
		env("MONETIZATION_SERVICE_URL", "http://monetization-service:8099"),
		env("POST_SERVICE_URL", "http://post-service:8084"),
		internalKey,
	)
	store := postgres.New(dbPool)
	svc := service.New(
		store, cl,
		int64(envInt("REVIEWER_BASE_PAY_PAISE", 500)),
		envInt("REVIEWER_ROTATION_CAP", 3),
		time.Duration(envInt("REVIEWER_ASSIGNMENT_TTL_MIN", 30))*time.Minute,
		env("REVIEWER_CREDIT_ENABLED", "false") == "true",
	)
	svc.SetGrading(service.GradingConfig{
		Enabled:    env("REVIEWER_GRADING_ENABLED", "true") == "true",
		Maturity:   time.Duration(envInt("REVIEWER_GRADING_MATURITY_MIN", 60)) * time.Minute,
		WindowDays: envInt("REVIEWER_GRADING_WINDOW_DAYS", 7),
		BonusPaise: int64(envInt("REVIEWER_BONUS_PAISE", 1000)),
		Interval:   time.Duration(envInt("REVIEWER_GRADING_INTERVAL_MIN", 5)) * time.Minute,
	})
	svc.SetIntegrity(service.IntegrityConfig{
		Enabled:          env("REVIEWER_INTEGRITY_ENABLED", "true") == "true",
		AuditRate:        envFloat("REVIEWER_AUDIT_RATE", 0.10),
		ShadowRate:       envFloat("REVIEWER_SHADOW_RATE", 0.10),
		SuspendThreshold: envInt("REVIEWER_SUSPEND_THRESHOLD", 3),
		Interval:         time.Duration(envInt("REVIEWER_INTEGRITY_INTERVAL_MIN", 10)) * time.Minute,
	})
	// Phase 4 ML pre-filter: auto-resolve clearly-bad / clearly-OK flagged content
	// so only the ambiguous middle reaches a human. Heuristic baseline (spam-score
	// bands); the Classifier interface lets a real model (ai-service) swap in later.
	if env("REVIEWER_PREFILTER_ENABLED", "true") == "true" {
		svc.SetPrefilter(prefilter.NewHeuristic(
			envFloat("REVIEWER_PREFILTER_REJECT_AT", 0.9),
			envFloat("REVIEWER_PREFILTER_APPROVE_BELOW", 0.72),
		))
	}
	svc.SetPromotion(service.PromotionConfig{
		Enabled:    env("REVIEWER_PROMOTE_ENABLED", "false") == "true",
		MinPctile:  envFloat("REVIEWER_PROMOTE_MIN_PCTILE", 0.5),
		Interval:   time.Duration(envInt("REVIEWER_PROMOTE_INTERVAL_MIN", 10)) * time.Minute,
		BatchLimit: envInt("REVIEWER_PROMOTE_BATCH", 100),
	})
	handler := reviewerhttp.New(svc)

	go svc.RunExpirySweeper(ctx)
	go svc.RunGradingWorker(ctx)
	go svc.RunIntegrityWorker(ctx)
	go svc.RunPromotionWorker(ctx)

	checker := health.New("reviewer-service")
	checker.Register("postgres", health.PingCheck(dbPool))

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))
	r.Use(middleware.RequireInternalKey(internalKey))

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

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}
