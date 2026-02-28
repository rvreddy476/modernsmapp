package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/identity-platform/auth-service/internal/config"
	"github.com/identity-platform/auth-service/internal/events"
	"github.com/identity-platform/auth-service/internal/http"
	"github.com/identity-platform/auth-service/internal/service"
	"github.com/identity-platform/auth-service/internal/store"
	"github.com/identity-platform/shared/logging"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg := config.Load()
	logger := logging.New("auth-service")
	slog.SetDefault(logger)
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
	authHandler := http.New(authSvc, cfg, logger)

	// 4. Outbox Relay
	relay := events.NewOutboxRelay(authStore, authProducer, logger, 1*time.Second)
	go relay.Start(ctx)

	// 5. Server
	r := gin.New()
	r.Use(http.RequestIDMiddleware())
	r.Use(http.LoggerMiddleware(logger))
	r.Use(http.RecoveryMiddleware(logger))
	r.Use(http.CORSMiddleware())
	proxies := cfg.TrustedProxies
	if len(proxies) == 0 {
		proxies = nil
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		logger.Error("failed to set trusted proxies", "err", err)
		os.Exit(1)
	}
	authMW := http.AuthMiddleware(cfg.JWTSecret)
	csrfMW := http.RequireCSRFMiddleware()
	authHandler.RegisterRoutes(r, authMW, csrfMW)
	authHandler.RegisterDocsRoutes(r)

	logger.Info("starting auth-service", "port", cfg.HTTPPort)
	if err := r.Run(":" + cfg.HTTPPort); err != nil {
		logger.Error("failed to run server", "err", err)
		os.Exit(1)
	}
}
