// Command doorstep-service runs Doorstep (home services, Urban Company
// style): a fixed-price catalogue booked into a time slot, delivered by gig
// professionals. Plan: the "Doorstep" section of the programme plan; contract:
// contracts/doorstep/openapi.yaml and asyncapi.yaml.
//
// A1 scope: catalogue, zones/serviceability, quotes and the admin-internal
// catalogue and config CRUD. Later lanes add professionals (A2), bookings
// and payments (A3), dispatch and realtime (A4), the visit (A5).
package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/database"
	"github.com/atpost/doorstep-service/internal/config"
	"github.com/atpost/doorstep-service/internal/devseed"
	doorstephttp "github.com/atpost/doorstep-service/internal/http"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/outbox"
	"github.com/atpost/shared/server"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The pgx store is the service's store.
var _ service.Store = (*store.Store)(nil)

func main() {
	logging.Init(logging.Config{ServiceName: "doorstep-service"})

	// Fail closed before touching any dependency: a production process with
	// no internal key serves forged identities; without the platform GSTIN
	// it cannot price the s.9(5) categories; the dev seed never runs there.
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}
	if err := doorstephttp.CheckInternalKey(cfg.Production, cfg.InternalKey); err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}
	if cfg.InternalKey == "" {
		slog.Warn("INTERNAL_SERVICE_KEY is empty; /v1/doorstep accepts forged X-User-Id from anything that can reach this port (allowed outside production only)")
	}
	// Admin console: SERVICE_CALLERS=admin-service plus
	// SERVICE_CALLER_ADMIN_SERVICE_{KID,PUBKEY,OPS}. Unset: no token is
	// accepted and /v1/doorstep/internal/admin answers 401. A named caller
	// with a missing key or empty ops refuses to start.
	serviceVerifier, err := doorstephttp.ServiceCallersFromEnv(os.Getenv)
	if err != nil {
		slog.Error("refusing to start: SERVICE_CALLERS", "error", err)
		os.Exit(1)
	}
	taxComputer, err := tax.NewGST(nil, cfg.PlatformGSTIN)
	if err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	poolCfg, err := pgxpool.ParseConfig(cfg.PostgresDSN)
	if err != nil {
		slog.Error("parse db config", "error", err)
		os.Exit(1)
	}
	// Every statement names the doorstep schema; the path only has to reach
	// the PostGIS types and schema_migrations in public. Pinning it keeps
	// migrationrunner's bookkeeping in public.schema_migrations whatever
	// search_path the DSN carries.
	poolCfg.ConnConfig.RuntimeParams["search_path"] = "public"
	poolCfg.MaxConns = 20
	poolCfg.MinConns = 2
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

	if err := database.BootstrapSchema(ctx, dbPool); err != nil {
		slog.Error("failed to bootstrap doorstep schema", "error", err)
		os.Exit(1)
	}
	slog.Info("doorstep schema ready")

	if cfg.DevSeed {
		if err := devseed.Run(ctx, dbPool, os.Getenv); err != nil {
			slog.Error("dev seed failed", "error", err)
			os.Exit(1)
		}
		cats, svcs, opts, addons, rates := devseed.Counts()
		slog.Info("doorstep dev seed applied (Hyderabad)", "categories", cats, "services", svcs, "options", opts, "addons", addons, "rate_cards", rates)
	}

	httpMetrics := metrics.NewHTTPMetrics("doorstep-service")
	dbMetrics := metrics.NewDBPoolMetrics("doorstep-service", "postgres")
	bgCtx, bgCancel := context.WithCancel(ctx)
	defer bgCancel()
	go collectDBPoolStats(bgCtx, dbPool, dbMetrics)

	checker := health.New("doorstep-service")
	checker.Register("postgres", health.PingCheck(dbPool))

	// Durable outbox: every doorstep.events message is enqueued in the same
	// transaction as its state change (doorstep.outbox_events, schema-local
	// because public.outbox_events belongs to another service) and drained
	// here. A1 emits no events yet; A3+ enqueue through outbox.NewQueuer("doorstep").
	outboxPublisher := outbox.New(dbPool, outbox.Config{
		DBSchema:     "doorstep",
		KafkaBrokers: strings.Join(cfg.KafkaBrokers, ","),
		DefaultTopic: cfg.KafkaTopic,
	})
	go outboxPublisher.Run(bgCtx)
	slog.Info("outbox publisher started", "topic", cfg.KafkaTopic)

	svc := service.New(store.New(dbPool), taxComputer, cfg.QuoteTTL)
	slog.Info("doorstep-service: pricing", "gst_computed", taxComputer.Computed(), "quote_ttl", cfg.QuoteTTL)
	if serviceVerifier == nil {
		slog.Warn("doorstep-service: SERVICE_CALLERS not set — admin-service tokens are refused; /v1/doorstep/internal/admin answers 401")
	} else {
		slog.Info("doorstep-service: service-token callers registered", "callers", serviceVerifier.Callers())
	}
	handler := doorstephttp.New(svc, cfg.InternalKey).WithServiceAuth(serviceVerifier)

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
		Port:            cfg.HTTPPort,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			bgCancel()
			dbPool.Close()
			slog.Info("cleanup completed")
		},
	}); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
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
