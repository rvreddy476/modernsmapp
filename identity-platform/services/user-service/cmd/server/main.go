package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/atpost/identity-shared/logging"
	sharedmiddleware "github.com/atpost/identity-shared/middleware"
	tracepkg "github.com/atpost/identity-shared/o11y/trace"
	"github.com/atpost/identity-shared/store/schemabootstrap"
	"github.com/atpost/identity-shared/store/schemaguard"
	"github.com/atpost/identity-shared/transport"
	"github.com/atpost/identity-user-service/database"
	"github.com/atpost/identity-user-service/internal/config"
	"github.com/atpost/identity-user-service/internal/events"
	"github.com/atpost/identity-user-service/internal/purge"
	"github.com/atpost/identity-user-service/internal/http"
	"github.com/atpost/identity-user-service/internal/service"
	"github.com/atpost/identity-user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg := config.Load()
	logger := logging.New("user-service")
	slog.SetDefault(logger)

	// Phase F3.7 — tracing init. Falls back to no-op on collector
	// failure; see auth-service main.go for the full rationale.
	tracerProvider, _ := tracepkg.InitTracer(
		"user-service",
		envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "http://jaeger:4317"),
	)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tracerProvider.Shutdown(shutdownCtx)
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 1. Database
	poolCfg, err := pgxpool.ParseConfig(cfg.PostgresDSN)
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
		logger.Error("unable to connect to database", "err", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		logger.Error("database ping failed", "err", err)
		os.Exit(1)
	}

	// Create the schema this service owns.
	//
	// usr.inbox_events was defined in 006_inbox_events.sql and applied nowhere:
	// the boot-time migration runner this service relied on was pointed at a
	// disabled directory, because that directory's contents could not execute.
	// Strictly additive, idempotent DDL — changes that need a decision about
	// existing data belong in the deployment pipeline.
	if err := schemabootstrap.Apply(ctx, dbPool, database.SetupSQL); err != nil {
		logger.Error("failed to bootstrap user schema", "err", err)
		os.Exit(1)
	}
	logger.Info("user schema ready")

	// Verify every object this build depends on actually exists. usr.users and
	// usr.user_settings come from auth-service, so this service cannot create
	// them and must not assume them. Fatal on purpose.
	if err := schemaguard.Verify(ctx, dbPool, "user-service", store.SchemaRequirements); err != nil {
		logger.Error("schema precondition failed", "err", err)
		os.Exit(1)
	}
	logger.Info("schema preconditions verified", "objects", len(store.SchemaRequirements))

	logger.Info("connected to Postgres")

	// 2. Redis
	rdb, err := transport.NewRedisClientFromEnv(cfg.RedisAddr)
	if err != nil {
		logger.Error("failed to configure redis client", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := rdb.Close(); err != nil {
			logger.Warn("failed to close redis client", "err", err)
		}
	}()
	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Error("redis ping failed", "err", err)
		os.Exit(1)
	}
	logger.Info("connected to Redis")

	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		logger.Error("failed to configure kafka dialer", "err", err)
		os.Exit(1)
	}

	// 3. Dependencies
	userStore := store.New(dbPool)
	userSvc := service.New(userStore, rdb, cfg, logger)
	// Settings-changed invalidation signal for graph/chat permission caches
	// (production chat pass, directive §5.1). Best-effort by design.
	settingsProducer := events.NewProducer(cfg.KafkaBrokers, cfg.KafkaTopic, logger)
	if settingsProducer != nil {
		userSvc.WithProducer(settingsProducer)
		defer func() {
			if err := settingsProducer.Close(); err != nil {
				logger.Warn("failed to close settings producer", "err", err)
			}
		}()
	}
	userHandler := http.New(userSvc, logger)
	// Audit UC1: wire the internal-service-key gate. Without this,
	// X-User-Id is effectively a public header — every other audit
	// closed the same gap; this is the matching identity-platform fix.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		userHandler.WithInternalKey(key)
		logger.Info("user-service: internal-service-key gate enabled")
	} else {
		logger.Warn("user-service: INTERNAL_SERVICE_KEY not set — every endpoint is unauthenticated. Do not run this configuration in production.")
	}

	// 3b. Kafka consumer (inbox-dedup enabled)
	consumer := events.NewConsumerWithDialer(cfg.KafkaBrokers, cfg.KafkaTopic, cfg.KafkaGroupID, kafkaDialer, dbPool, userSvc, logger)
	// Account control (auth-service 30-day deletion): hide usr.users on
	// user.deactivated / user.deletion_scheduled, unhide on the reverse, and
	// on user.purge_requested erase usr.* rows (never auth.*) and ack as
	// "user" onto platform.purge-acks.v1.
	purgeAcks := purge.NewKafkaAckPublisher(cfg.KafkaBrokers, envOr("PURGE_ACKS_TOPIC", purge.DefaultAcksTopic), kafkaDialer)
	defer func() { _ = purgeAcks.Close() }()
	consumer.WithLifecycleHandler(purge.NewHandler("user", userStore, purgeAcks, userStore, logger))
	defer func() {
		if err := consumer.Close(); err != nil {
			logger.Warn("failed to close kafka consumer", "err", err)
		}
	}()
	go consumer.Start(ctx)

	// 4. Server
	r := gin.New()
	// Phase F3.7 — tracing middleware first so spans wrap the rest
	// of the chain and log enrichment picks up trace_id + span_id.
	r.Use(sharedmiddleware.OtelTracing("user-service"))
	r.Use(http.RequestIDMiddleware())
	r.Use(http.LoggerMiddleware(logger))
	r.Use(http.RecoveryMiddleware(logger))
	proxies := cfg.TrustedProxies
	if len(proxies) == 0 {
		proxies = nil
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		logger.Error("failed to set trusted proxies", "err", err)
		os.Exit(1)
	}
	userHandler.RegisterRoutes(r, http.AuthMiddlewareWithKeys(http.JWTKeySet{
		ActiveKID:      cfg.JWTKID,
		ActiveSecret:   cfg.JWTSecret,
		PreviousKID:    cfg.JWTKIDPrevious,
		PreviousSecret: cfg.JWTSecretPrevious,
	}), http.RequireCSRFMiddleware())

	logger.Info("starting user-service", "port", cfg.HTTPPort)
	if err := r.Run(":" + cfg.HTTPPort); err != nil {
		logger.Error("failed to run server", "err", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
