package main

import (
	"context"
	"crypto/rsa"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/atpost/chat-shared/transport"
	"github.com/atpost/chat-shared/logging"
	"github.com/atpost/chat-ws-gateway/internal/config"
	httpapi "github.com/atpost/chat-ws-gateway/internal/http"
)

func main() {
	cfg := config.Load()
	logger := logging.New("ws-gateway")
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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

	jwtKeys := httpapi.JWTKeySet{
		ActiveKID:      cfg.JWTKID,
		ActiveSecret:   cfg.JWTSecret,
		PreviousKID:    cfg.JWTKIDPrevious,
		PreviousSecret: cfg.JWTSecretPrevious,
	}
	// Optional RS256 verification (additive): load auth-service's public key.
	if cfg.JWTPublicKeyPEM != "" {
		pub, perr := httpapi.ParseRSAPublicKeyPEM(cfg.JWTPublicKeyPEM)
		if perr != nil {
			logger.Error("failed to parse JWT_PUBLIC_KEY_PEM", "err", perr)
			os.Exit(1)
		}
		jwtKeys.RSAKeys = map[string]*rsa.PublicKey{cfg.JWTRS256KID: pub}
		logger.Info("RS256 token verification enabled", "kid", cfg.JWTRS256KID)
	}

	server := httpapi.NewServer(rdb, logger, httpapi.ServerOptions{
		JWTSecret: cfg.JWTSecret,
		JWTKeys:   jwtKeys,
		AllowedOrigins: cfg.AllowedOrigins,
		AllowQueryToken: cfg.WSAllowQueryToken,
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
