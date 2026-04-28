package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/atpost/chat-message-service/database"
	"github.com/atpost/chat-message-service/internal/config"
	"github.com/atpost/chat-message-service/internal/events"
	"github.com/atpost/chat-message-service/internal/http"
	"github.com/atpost/chat-message-service/internal/service"
	pgStore "github.com/atpost/chat-message-service/internal/store/postgres"
	scyllaStore "github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/atpost/chat-shared/logging"
	"github.com/atpost/chat-shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg := config.Load()
	logger := logging.New("message-service")
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 1. Postgres
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

	if err := pgStore.BootstrapSchema(ctx, dbPool, database.SetupSQL); err != nil {
		logger.Error("failed to bootstrap chat schema", "err", err)
		os.Exit(1)
	}
	logger.Info("chat schema ready")
	logger.Info("connected to Postgres")

	// 2. ScyllaDB
	cluster := gocql.NewCluster(cfg.ScyllaHosts...)
	cluster.Keyspace = cfg.ScyllaKeyspace
	cluster.Consistency = gocql.LocalQuorum

	scyllaSession, err := cluster.CreateSession()
	if err != nil {
		logger.Error("unable to connect to scylladb", "err", err)
		os.Exit(1)
	}
	defer scyllaSession.Close()
	logger.Info("connected to ScyllaDB")

	// 3. Redis
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

	// 4. Kafka Producer
	producer := events.NewProducerWithDialer(cfg.KafkaBrokers, cfg.KafkaTopic, kafkaDialer)
	defer func() {
		if err := producer.Close(); err != nil {
			logger.Warn("failed to close kafka producer", "err", err)
		}
	}()
	logger.Info("kafka producer initialized", "brokers", cfg.KafkaBrokers, "topic", cfg.KafkaTopic)

	// 5. Stores + Service
	convStore := pgStore.New(dbPool)
	msgStore := scyllaStore.New(scyllaSession)
	svc := service.New(convStore, msgStore, rdb, producer, logger, cfg.OutboxPollInterval)
	svc.SetUserDirectory(cfg.UserServiceURL, cfg.InternalServiceKey)
	handler := http.New(svc, logger)

	// 6. Identity Event Consumer (background)
	identityConsumer := events.NewIdentityConsumerWithDialer(cfg.KafkaBrokers, cfg.IdentityKafkaTopic, cfg.IdentityKafkaGroupID, kafkaDialer, convStore, logger)
	go identityConsumer.Start(ctx)

	// 7. Outbox Relay (background)
	go svc.StartOutboxRelay(ctx)

	// 8. Scheduled Message Worker (background)
	go svc.StartScheduledMessageWorker(ctx)

	// 9. HTTP Server
	r := gin.New()
	r.Use(http.RequestIDMiddleware())
	r.Use(http.LoggerMiddleware(logger))
	r.Use(http.RecoveryMiddleware(logger))
	r.Use(http.CORSMiddleware())
	r.Use(http.AuthMiddleware(cfg.JWTSecret, logger))

	proxies := cfg.TrustedProxies
	if len(proxies) == 0 {
		proxies = nil
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		logger.Error("failed to set trusted proxies", "err", err)
		os.Exit(1)
	}

	handler.RegisterRoutes(r)

	logger.Info("starting message-service", "port", cfg.HTTPPort)
	if err := r.Run(":" + cfg.HTTPPort); err != nil {
		logger.Error("failed to run server", "err", err)
		os.Exit(1)
	}
}
