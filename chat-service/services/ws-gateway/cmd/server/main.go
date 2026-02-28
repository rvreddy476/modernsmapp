package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chat-service/shared/logging"
	"github.com/chat-service/ws-gateway/internal/config"
	httpapi "github.com/chat-service/ws-gateway/internal/http"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg := config.Load()
	logger := logging.New("ws-gateway")
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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

	server := httpapi.NewServer(rdb, logger, httpapi.ServerOptions{
		JWTSecret:      cfg.JWTSecret,
		AllowedOrigins: cfg.AllowedOrigins,
		WriteWait:      cfg.WSWriteWait,
		PongWait:       cfg.WSPongWait,
		PingPeriod:     cfg.WSPingPeriod,
		MaxMessageSize: cfg.WSMaxMessageSize,
	})

	httpServer := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      server.Routes(),
		ReadTimeout:  cfg.HTTPReadTimeout,
		WriteTimeout: cfg.HTTPWriteTimeout,
		IdleTimeout:  cfg.HTTPIdleTimeout,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Warn("http shutdown failed", "err", err)
		}
	}()

	logger.Info("starting ws-gateway", "port", cfg.HTTPPort)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("failed to run ws-gateway", "err", err)
		os.Exit(1)
	}
}
