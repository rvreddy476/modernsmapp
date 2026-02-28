package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/identity-platform/profile-service/internal/config"
	"github.com/identity-platform/profile-service/internal/events"
	"github.com/identity-platform/profile-service/internal/http"
	"github.com/identity-platform/profile-service/internal/service"
	"github.com/identity-platform/profile-service/internal/store"
	"github.com/identity-platform/shared/logging"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg := config.Load()
	logger := logging.New("profile-service")
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
	profileStore := store.New(dbPool)
	profileProducer := events.NewProducer(cfg.KafkaBrokers, cfg.KafkaTopic)
	defer func() {
		if err := profileProducer.Close(); err != nil {
			logger.Warn("failed to close kafka producer", "err", err)
		}
	}()
	profileSvc := service.New(profileStore, rdb, profileProducer, cfg, logger)
	profileHandler := http.New(profileSvc, logger)

	// 4. Server
	r := gin.New()
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
	profileHandler.RegisterRoutes(r, http.AuthMiddleware(cfg.JWTSecret), http.RequireCSRFMiddleware())

	logger.Info("starting profile-service", "port", cfg.HTTPPort)
	if err := r.Run(":" + cfg.HTTPPort); err != nil {
		logger.Error("failed to run server", "err", err)
		os.Exit(1)
	}
}
