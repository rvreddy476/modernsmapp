package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/atpost/chat-shared/accessauth"
	"github.com/atpost/chat-shared/logging"
	"github.com/atpost/chat-shared/transport"
	"github.com/atpost/chat-ws-gateway/internal/config"
	httpapi "github.com/atpost/chat-ws-gateway/internal/http"
	"github.com/atpost/chat-ws-gateway/internal/peers"
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

	authKeys, authPolicy, err := accessauth.LoadFromEnv()
	if err != nil {
		logger.Error("refusing to start with unsafe access-token policy", "err", err)
		os.Exit(1)
	}
	if err := cfg.ValidateProduction(authPolicy.Production); err != nil {
		logger.Error("refusing to start with unsafe websocket policy", "err", err)
		os.Exit(1)
	}
	jwtKeys := httpapi.JWTKeySet{
		ActiveKID:      cfg.JWTKID,
		ActiveSecret:   cfg.JWTSecret,
		PreviousKID:    cfg.JWTKIDPrevious,
		PreviousSecret: cfg.JWTSecretPrevious,
		RSAKeys:        authKeys.RSAKeys,
		Policy:         authPolicy,
	}

	// Presence fan-out (chat parity §4): DM peers from chat_db (read-only),
	// permissions from graph-service. Either missing leaves it off.
	var presencePeers httpapi.PeerSource
	var presenceVisibility httpapi.VisibilityChecker
	if cfg.PresenceFanoutEnabled && cfg.ChatPostgresDSN != "" && cfg.GraphServiceURL != "" {
		pg, err := peers.Open(ctx, cfg.ChatPostgresDSN)
		if err != nil {
			logger.Warn("presence fan-out disabled: bad CHAT_POSTGRES_DSN", "err", err)
		} else {
			defer pg.Close()
			if err := pg.Ping(ctx); err != nil {
				logger.Warn("chat_db not reachable yet; presence fan-out retries per transition", "err", err)
			}
			presencePeers = pg
			presenceVisibility = &httpapi.GraphVisibility{
				BaseURL:            cfg.GraphServiceURL,
				InternalServiceKey: cfg.InternalServiceKey,
				Client:             &http.Client{Timeout: 3 * time.Second},
				Redis:              rdb,
				Log:                logger,
			}
		}
	}
	logger.Info("presence fan-out", "enabled", presencePeers != nil)

	server := httpapi.NewServer(rdb, logger, httpapi.ServerOptions{
		JWTSecret:                cfg.JWTSecret,
		JWTKeys:                  jwtKeys,
		AllowedOrigins:           cfg.AllowedOrigins,
		AllowQueryToken:          cfg.WSAllowQueryToken,
		WriteWait:                cfg.WSWriteWait,
		PongWait:                 cfg.WSPongWait,
		PingPeriod:               cfg.WSPingPeriod,
		MaxMessageSize:           cfg.WSMaxMessageSize,
		EntitlementSecret:        cfg.EntitlementSecret,
		EnablePostRooms:          cfg.PostRoomsEnabled,
		PostViewer:               httpapi.NewHTTPPostViewAuthorizer(cfg.PostServiceURL, cfg.InternalServiceKey, nil),
		EnableLiveRooms:          cfg.LiveRoomsEnabled,
		LiveViewer:               httpapi.NewHTTPLiveViewAuthorizer(cfg.LiveServiceV2URL, cfg.InternalServiceKey, nil),
		EnableConversationFrames: cfg.ConversationFramesEnabled,
		TypingNotifier:           httpapi.NewHTTPTypingNotifier(cfg.MessageServiceURL, nil),
		// Hidden chats: subscribe with {conversation_id, open_token}; the
		// gateway fetches the room entitlement from message-service.
		EntitlementFetcher: httpapi.NewHTTPEntitlementFetcher(cfg.MessageServiceURL, nil),
		PresencePeers:      presencePeers,
		PresenceVisibility: presenceVisibility,
		// Audit M5: socket and inbound-frame bounds.
		MaxConnsPerUser:    cfg.WSMaxConnsPerUser,
		MaxConnsPerProcess: cfg.WSMaxConnsPerProcess,
		FrameRate:          float64(cfg.WSFrameRate),
		FrameBurst:         cfg.WSFrameBurst,
		// Audit L2: owner check for call:<id> rooms. EnableScopedRooms is
		// still not set, so this is dormant; update/group_post rooms have no
		// owner route yet and stay refused even if the flag is turned on.
		CallRoomAuthorizer: callRoomAuthorizer(cfg.CallServiceURL),
	})
	// Defers run last-in first-out: the shared pub/sub hub closes before the
	// Redis client it uses.
	defer server.Close()
	logger.Info("realtime rooms", "post_rooms", cfg.PostRoomsEnabled, "live_rooms", cfg.LiveRoomsEnabled, "conversation_frames", cfg.ConversationFramesEnabled)

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

// callRoomAuthorizer is nil (refuse every call room) when no call-service
// URL is configured.
func callRoomAuthorizer(baseURL string) httpapi.ScopedRoomAuthorizer {
	if baseURL == "" {
		return nil
	}
	return httpapi.NewHTTPCallRoomAuthorizer(baseURL, nil)
}
