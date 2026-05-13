package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/atpost/chat-call-service/database"
	"github.com/atpost/chat-call-service/internal/config"
	callhttp "github.com/atpost/chat-call-service/internal/http"
	"github.com/atpost/chat-call-service/internal/service"
	"github.com/atpost/chat-call-service/internal/sfu"
	"github.com/atpost/chat-call-service/internal/store/postgres"
	"github.com/atpost/chat-shared/logging"
	"github.com/atpost/chat-shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

func main() {
	cfg := config.Load()
	logger := logging.New("call-service")
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 1. Postgres (call_db)
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
		logger.Error("unable to connect to postgres", "err", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		logger.Error("postgres ping failed", "err", err)
		os.Exit(1)
	}
	logger.Info("connected to Postgres (call_db)")

	if err := postgres.BootstrapSchema(ctx, dbPool, database.SetupSQL); err != nil {
		logger.Error("failed to bootstrap call schema", "err", err)
		os.Exit(1)
	}
	logger.Info("call schema ready")

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

	// 3. Kafka Writers (3 topics)
	lifecycleWriter := kafka.NewWriter(kafka.WriterConfig{
		Brokers:      cfg.KafkaBrokers,
		Topic:        cfg.KafkaLifecycleTopic,
		Balancer:     &kafka.LeastBytes{},
		WriteTimeout: 10 * time.Second,
		Dialer:       kafkaDialer,
	})
	notificationWriter := kafka.NewWriter(kafka.WriterConfig{
		Brokers:      cfg.KafkaBrokers,
		Topic:        cfg.KafkaNotificationTopic,
		Balancer:     &kafka.LeastBytes{},
		WriteTimeout: 10 * time.Second,
		Dialer:       kafkaDialer,
	})
	analyticsWriter := kafka.NewWriter(kafka.WriterConfig{
		Brokers:      cfg.KafkaBrokers,
		Topic:        cfg.KafkaAnalyticsTopic,
		Balancer:     &kafka.LeastBytes{},
		WriteTimeout: 10 * time.Second,
		Dialer:       kafkaDialer,
	})
	defer lifecycleWriter.Close()
	defer notificationWriter.Close()
	defer analyticsWriter.Close()
	logger.Info("kafka producers initialized",
		"lifecycle_topic", cfg.KafkaLifecycleTopic,
		"notification_topic", cfg.KafkaNotificationTopic,
		"analytics_topic", cfg.KafkaAnalyticsTopic,
	)

	// 4. SFU Provider
	configuredICEServers, err := sfu.ParseICEServersJSON(cfg.ICEServersJSON)
	if err != nil {
		logger.Error("invalid ICE_SERVERS_JSON", "err", err)
		os.Exit(1)
	}

	var sfuProvider sfu.SFUProvider
	hasLiveKitHost := cfg.LiveKitHost != ""
	hasLiveKitCreds := cfg.LiveKitAPIKey != "" || cfg.LiveKitAPISecret != ""
	if hasLiveKitHost || hasLiveKitCreds {
		if !hasLiveKitHost || cfg.LiveKitAPIKey == "" || cfg.LiveKitAPISecret == "" {
			logger.Error("incomplete livekit configuration", "host_set", hasLiveKitHost, "api_key_set", cfg.LiveKitAPIKey != "", "api_secret_set", cfg.LiveKitAPISecret != "")
			os.Exit(1)
		}

		liveKitProvider, err := sfu.NewLiveKitProvider(cfg.LiveKitHost, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret)
		if err != nil {
			logger.Error("failed to initialize livekit provider", "err", err)
			os.Exit(1)
		}
		sfuProvider = liveKitProvider
	} else {
		sfuProvider = sfu.NewStubProviderWithICEServers(configuredICEServers)
	}
	if len(configuredICEServers) > 0 {
		logger.Info("custom ICE servers configured", "count", len(configuredICEServers))
	}
	logger.Info("SFU provider initialized", "provider", sfuProvider.ProviderName())

	// 5. Store + Service
	store := postgres.NewCallStore(dbPool)
	rateLimiter := service.NewRateLimiter(rdb)
	// Audit C2: gate direct calls on the social graph. GRAPH_SERVICE_URL
	// must be set in production for this to do anything; empty means
	// the policy is a no-op (used for tests + isolated dev rigs).
	callPolicy := service.NewCallPolicy(os.Getenv("GRAPH_SERVICE_URL"))
	svc := service.New(store, sfuProvider, rateLimiter, callPolicy, rdb, logger, cfg.ReconnectGraceSeconds)
	handler := callhttp.New(svc, logger)

	// 6. Outbox Relay (background)
	outboxRelay := service.NewOutboxRelay(store, lifecycleWriter, notificationWriter, analyticsWriter, logger, cfg.OutboxPollInterval)
	go outboxRelay.Start(ctx)

	// 7. Ringing Timeout Worker (background)
	timeoutWorker := service.NewRingingTimeoutWorker(store, svc, logger, cfg.RingTimeoutSeconds)
	go timeoutWorker.Start(ctx)

	// 8. HTTP Server
	r := gin.New()
	r.Use(callhttp.RequestIDMiddleware())
	r.Use(callhttp.LoggerMiddleware(logger))
	r.Use(callhttp.RecoveryMiddleware(logger))
	r.Use(callhttp.CORSMiddleware())
	r.Use(callhttp.AuthMiddleware(cfg.JWTSecret, logger))

	proxies := cfg.TrustedProxies
	if len(proxies) == 0 {
		proxies = nil
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		logger.Error("failed to set trusted proxies", "err", err)
		os.Exit(1)
	}

	handler.RegisterRoutes(r)

	logger.Info("starting call-service", "port", cfg.HTTPPort)
	if err := r.Run(":" + cfg.HTTPPort); err != nil {
		logger.Error("failed to run server", "err", err)
		os.Exit(1)
	}
}
