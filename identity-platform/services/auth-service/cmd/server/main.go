package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/atpost/identity-auth-service/database"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/events"
	internalhttp "github.com/atpost/identity-auth-service/internal/http"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/internal/store"
	authcrypto "github.com/atpost/identity-shared/crypto"
	"github.com/atpost/identity-shared/logging"
	sharedmiddleware "github.com/atpost/identity-shared/middleware"
	tracepkg "github.com/atpost/identity-shared/o11y/trace"
	"github.com/atpost/identity-shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg := config.Load()
	logger := logging.New("auth-service")
	slog.SetDefault(logger)

	// Phase F3.7 — wire OpenTelemetry. The gateway already injects
	// `traceparent` headers, so spans created here link back to the
	// originating browser/mobile request. Falls back to a no-op
	// provider when the collector is unreachable so boot still
	// succeeds in environments without observability infra.
	tracerProvider, _ := tracepkg.InitTracer(
		"auth-service",
		envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "http://jaeger:4317"),
	)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tracerProvider.Shutdown(shutdownCtx)
	}()

	if cfg.PostgresDSN == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	if cfg.JWTSecret == "" {
		slog.Error("JWT_SECRET is required")
		os.Exit(1)
	}

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

	if err := store.BootstrapSchema(ctx, dbPool, database.SetupSQL); err != nil {
		logger.Error("failed to bootstrap auth schema", "err", err)
		os.Exit(1)
	}
	logger.Info("auth schema ready")

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
	authStore := store.New(dbPool)
	// TOTP secret encryption at rest. Without TOTP_ENCRYPTION_KEY the
	// store falls back to plaintext column — a startup warning here
	// makes the misconfig visible in logs + ops dashboards. Key must
	// be 64 hex chars (AES-256 key).
	if hexKey := os.Getenv("TOTP_ENCRYPTION_KEY"); hexKey != "" {
		box, err := authcrypto.NewSecretBox(hexKey)
		if err != nil {
			logger.Error("totp encryption disabled — bad key", "err", err)
		} else {
			authStore.WithTOTPEncryption(box)
			logger.Info("totp secret encryption enabled")
		}
	} else {
		logger.Warn("TOTP_ENCRYPTION_KEY not set — 2FA secrets stored in plaintext (DO NOT run this in production)")
	}
	authProducer := events.NewProducerWithDialer(cfg.KafkaBrokers, cfg.KafkaTopic, kafkaDialer)
	defer func() {
		if err := authProducer.Close(); err != nil {
			logger.Warn("failed to close kafka producer", "err", err)
		}
	}()

	miniAppSessionSigner, err := service.NewMiniAppSessionSigner(cfg, logger)
	if err != nil {
		logger.Error("failed to configure mini app session signer", "err", err)
		os.Exit(1)
	}

	authSvc := service.New(authStore, authProducer, cfg, logger, rdb, miniAppSessionSigner)
	authHandler := internalhttp.New(authSvc, cfg, logger, rdb)

	// 4. Outbox Relay
	relay := events.NewOutboxRelay(authStore, authProducer, logger, 1*time.Second)
	go relay.Start(ctx)

	// 5. Server
	r := gin.New()
	// Phase F3.7 — tracing middleware runs first so the span context
	// is available to RequestID + Logger downstream. Same ordering
	// as the Architecture-side services.
	r.Use(sharedmiddleware.OtelTracing("auth-service"))
	r.Use(internalhttp.RequestIDMiddleware())
	r.Use(internalhttp.LoggerMiddleware(logger))
	r.Use(internalhttp.RecoveryMiddleware(logger))
	// CORS is handled by the API Gateway — do not add duplicate headers here.
	proxies := cfg.TrustedProxies
	if len(proxies) == 0 {
		proxies = nil
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		logger.Error("failed to set trusted proxies", "err", err)
		os.Exit(1)
	}
	// A10 — pass Redis so the JWT middleware can consult the session
	// revocation cache. Fail-open on miss; the access-token TTL caps
	// the worst-case revocation lag.
	// C7 — kid-aware verify so a rotation has a window where both the
	// previous and active secret verify (set JWT_SECRET_PREVIOUS during
	// the cutover; unset once AccessTokenTTL has elapsed).
	// RSAPublic/RSAKID let the service verify the RS256 tokens it mints (nil
	// when signing is HS256). Derived from the loaded private key, so no extra
	// public-key env is needed for the service's own endpoints.
	authMW := internalhttp.AuthMiddlewareWithKeys(internalhttp.JWTKeySet{
		ActiveKID:      cfg.JWTKID,
		ActiveSecret:   cfg.JWTSecret,
		PreviousKID:    cfg.JWTKIDPrevious,
		PreviousSecret: cfg.JWTSecretPrevious,
		RSAPublic:      authSvc.AccessTokenPublicKey(),
		RSAKID:         cfg.AccessTokenRS256KID,
	}, rdb)
	csrfMW := internalhttp.RequireCSRFMiddleware()
	authHandler.RegisterRoutes(r, authMW, csrfMW)
	authHandler.RegisterWebAuthnRoutes(r, authMW, csrfMW) // no-op unless built with -tags webauthn
	authHandler.RegisterDocsRoutes(r)

	srv := &http.Server{
		Addr:    ":" + cfg.HTTPPort,
		Handler: r,
	}

	go func() {
		logger.Info("starting auth-service", "port", cfg.HTTPPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal via the existing NotifyContext
	<-ctx.Done()

	slog.Info("shutting down auth service...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("auth service stopped")
}

// envOr returns the env var or fallback. Local helper so we don't
// pull in another shared package just for one read.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
