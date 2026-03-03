package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/events"
	internalhttp "github.com/atpost/identity-auth-service/internal/http"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/logging"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg := config.Load()
	logger := logging.New("auth-service")
	slog.SetDefault(logger)

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
	dbPool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		logger.Error("unable to connect to database", "err", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		logger.Error("database ping failed", "err", err)
		os.Exit(1)
	}
	logger.Info("connected to Postgres")

	// 2. Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddr,
	})
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

	// 3. Dependencies
	authStore := store.New(dbPool)
	authProducer := events.NewProducer(cfg.KafkaBrokers, cfg.KafkaTopic)
	defer func() {
		if err := authProducer.Close(); err != nil {
			logger.Warn("failed to close kafka producer", "err", err)
		}
	}()

	authSvc := service.New(authStore, authProducer, cfg, logger, rdb)
	authHandler := internalhttp.New(authSvc, cfg, logger)

	// 4. Outbox Relay
	relay := events.NewOutboxRelay(authStore, authProducer, logger, 1*time.Second)
	go relay.Start(ctx)

	// 5. Server
	r := gin.New()
	r.Use(internalhttp.RequestIDMiddleware())
	r.Use(internalhttp.LoggerMiddleware(logger))
	r.Use(internalhttp.RecoveryMiddleware(logger))
	r.Use(internalhttp.CORSMiddleware())
	proxies := cfg.TrustedProxies
	if len(proxies) == 0 {
		proxies = nil
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		logger.Error("failed to set trusted proxies", "err", err)
		os.Exit(1)
	}
	authMW := internalhttp.AuthMiddleware(cfg.JWTSecret)
	csrfMW := internalhttp.RequireCSRFMiddleware()
	authHandler.RegisterRoutes(r, authMW, csrfMW)
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
