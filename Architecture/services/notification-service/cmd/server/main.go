package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/events"
	"github.com/atpost/notification-service/internal/graph"
	"github.com/atpost/notification-service/internal/http"
	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/atpost/notification-service/internal/workers"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/mailer"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "notification-service"})

	// 2. Config
	port := env("HTTP_PORT", "8088")
	scyllaHosts := env("SCYLLA_HOSTS", "scylla")
	redisAddr := env("REDIS_ADDR", "redis:6379")
	kafkaBrokers := env("KAFKA_BROKERS", "redpanda:9092")

	ctx := context.Background()

	// 3. Database (Scylla)
	// Audit HS3: NumConns was hardcoded to 10 — at 100k notifications/sec
	// with 50 ms Scylla latency that means ~5k concurrent requests
	// queueing on 10 conns. Make it env-tunable; default bumped to 40
	// which matches the postgres pool sizing pattern in other services.
	cluster := gocql.NewCluster(strings.Split(scyllaHosts, ",")...)
	cluster.Keyspace = "social_notify"
	cluster.Consistency = gocql.Quorum
	cluster.NumConns = envInt("SCYLLA_NUM_CONNS", 40)
	cluster.MaxPreparedStmts = 1000
	session, err := cluster.CreateSession()
	if err != nil {
		slog.Error("failed to connect to scylla", "error", err)
		os.Exit(1)
	}
	defer session.Close()
	slog.Info("connected to scylla")

	// 4. Redis
	rdb, err := transport.NewRedisClientFromEnv(redisAddr)
	if err != nil {
		slog.Error("failed to configure redis client", "error", err)
		os.Exit(1)
	}
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping failed", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()
	slog.Info("connected to redis")

	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

	// 5. Database (Postgres -- for preferences & devices)
	pgDSN := os.Getenv("POSTGRES_DSN")
	var pgStore *postgres.Store
	var dbPool *pgxpool.Pool
	if pgDSN != "" {
		pgPoolCfg, err := pgxpool.ParseConfig(pgDSN)
		if err != nil {
			slog.Warn("unable to parse postgres config (preferences disabled)", "error", err)
		} else {
			pgPoolCfg.MaxConns = 25
			pgPoolCfg.MinConns = 5
			pgPoolCfg.MaxConnLifetime = 15 * time.Minute
			pgPoolCfg.MaxConnIdleTime = 5 * time.Minute
			pool, err := pgxpool.NewWithConfig(ctx, pgPoolCfg)
			if err != nil {
				slog.Warn("unable to connect to postgres (preferences disabled)", "error", err)
			} else {
				dbPool = pool
				defer dbPool.Close()
				if err := dbPool.Ping(ctx); err != nil {
					slog.Warn("postgres ping failed", "error", err)
				} else {
					slog.Info("connected to postgres")
					pgStore = postgres.New(dbPool)
					ensureNotifSchema(ctx, dbPool)
				}
			}
		}
	}

	// 6. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("notification-service")

	if dbPool != nil {
		dbMetrics := metrics.NewDBPoolMetrics("notification-service", "postgres")
		go collectDBPoolStats(ctx, dbPool, dbMetrics)
	}

	// 7. Health checker
	checker := health.New("notification-service")
	checker.Register("scylladb", health.ScyllaCheck(func(ctx context.Context) error {
		return session.Query("SELECT now() FROM system.local").Exec()
	}))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))
	if dbPool != nil {
		checker.Register("postgres", health.PingCheck(dbPool))
	}

	// 8. Dependencies
	scyllaStore := scylla.New(session)
	notifSvc := service.New(scyllaStore, rdb)
	if pgStore != nil {
		notifSvc.SetPGStore(pgStore)
	}

	// Transactional email transport. Uses SMTP when configured, otherwise logs.
	if smtpHost := os.Getenv("SMTP_HOST"); smtpHost != "" {
		notifSvc.SetMailer(&mailer.SMTPMailer{
			Host:     smtpHost,
			Port:     env("SMTP_PORT", "587"),
			Username: os.Getenv("SMTP_USERNAME"),
			Password: os.Getenv("SMTP_PASSWORD"),
			FromName: env("SMTP_FROM_NAME", "Postbook"),
			FromAddr: env("SMTP_FROM_ADDR", "no-reply@postbook.app"),
		})
		slog.Info("smtp mailer configured", "host", smtpHost)
	} else {
		notifSvc.SetMailer(mailer.NoopMailer{})
		slog.Info("smtp not configured; using noop mailer")
	}

	notifHandler := http.New(notifSvc, rdb)

	// Audit CS1: previously the handler defined WithInternalKey but
	// main.go never called it — every /v1/notifications/* endpoint
	// was reachable directly, including the bundle endpoint that
	// accepts an arbitrary recipient user_id. The env was already
	// being read (for the graph client below) but not passed here.
	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
	if internalKey != "" {
		notifHandler.WithInternalKey(internalKey)
		slog.Info("notification-service: internal-service-key gate enabled")
	} else {
		slog.Warn("notification-service: INTERNAL_SERVICE_KEY not set — every endpoint is unauthenticated. Do not run this configuration in production.")
	}

	// 9. Kafka Consumers
	consumer := events.NewConsumerWithDialer(
		strings.Split(kafkaBrokers, ","),
		"notification-service-group",
		"social.events.v1",
		notifSvc,
		kafkaDialer,
	)
	// Attach graph-service client so PostCreated events fan out to followers.
	graphURL := env("GRAPH_SERVICE_URL", "http://graph-service:8083")
	consumer.WithGraph(graph.New(graphURL, internalKey))
	slog.Info("graph client attached", "graph_url", graphURL)
	go consumer.Start(ctx)
	slog.Info("kafka consumer started", "topic", "social.events.v1")

	callConsumer := events.NewCallConsumerWithDialer(
		strings.Split(kafkaBrokers, ","),
		"notification-service-calls-group",
		env("KAFKA_CALL_NOTIFICATIONS_TOPIC", "call.notifications"),
		notifSvc,
		kafkaDialer,
	)
	go callConsumer.Start(ctx)
	slog.Info("kafka call consumer started", "topic", "call.notifications")

	// Chat events (spec §18): DM + message-request notifications. Separate
	// topic + consumer group so chat-event lag doesn't block social/call
	// notification delivery.
	chatTopic := env("CHAT_KAFKA_TOPIC", "chat.events.v1")
	chatConsumer := events.NewChatConsumerWithDialer(
		strings.Split(kafkaBrokers, ","),
		env("CHAT_KAFKA_GROUP_ID", "notification-service-chat"),
		chatTopic,
		notifSvc,
		kafkaDialer,
	)
	go chatConsumer.Start(ctx)
	slog.Info("kafka chat consumer started", "topic", chatTopic)

	// QA events live on a dedicated topic by default. Reuse the main consumer
	// type — its processMessage routes Q&A events through handleQAEvent.
	qaTopic := env("KAFKA_QA_TOPIC", "qa-events")
	qaConsumer := events.NewConsumerWithDialer(
		strings.Split(kafkaBrokers, ","),
		"notification-service-qa-group",
		qaTopic,
		notifSvc,
		kafkaDialer,
	)
	go qaConsumer.Start(ctx)
	slog.Info("kafka qa consumer started", "topic", qaTopic)

	// Sprint 3: Dating events live on dating-events topic. Same Consumer
	// type, separate consumer-group so dating-event lag doesn't impact
	// QA delivery.
	datingTopic := env("KAFKA_DATING_TOPIC", "dating-events")
	datingConsumer := events.NewConsumerWithDialer(
		strings.Split(kafkaBrokers, ","),
		"notification-service-dating-group",
		datingTopic,
		notifSvc,
		kafkaDialer,
	)
	go datingConsumer.Start(ctx)
	slog.Info("kafka dating consumer started", "topic", datingTopic)

	// Phase 2 mini-apps: each has its own Kafka topic + consumer group so
	// lag in one domain (e.g. wallet replay during a payments incident)
	// doesn't block other domains' notification delivery. The shared
	// processMessage dispatcher routes to handleWalletEvent /
	// handleBillPayEvent / handleRiderEvent based on event-type prefix.
	walletTopic := env("KAFKA_WALLET_TOPIC", "wallet-events")
	walletConsumer := events.NewConsumerWithDialer(
		strings.Split(kafkaBrokers, ","),
		"notification-service-wallet-group",
		walletTopic,
		notifSvc,
		kafkaDialer,
	)
	go walletConsumer.Start(ctx)
	slog.Info("kafka wallet consumer started", "topic", walletTopic)

	billpayTopic := env("KAFKA_BILLPAY_TOPIC", "billpay-events")
	billpayConsumer := events.NewConsumerWithDialer(
		strings.Split(kafkaBrokers, ","),
		"notification-service-billpay-group",
		billpayTopic,
		notifSvc,
		kafkaDialer,
	)
	go billpayConsumer.Start(ctx)
	slog.Info("kafka billpay consumer started", "topic", billpayTopic)

	riderTopic := env("KAFKA_RIDER_TOPIC", "rider-events")
	riderConsumer := events.NewConsumerWithDialer(
		strings.Split(kafkaBrokers, ","),
		"notification-service-rider-group",
		riderTopic,
		notifSvc,
		kafkaDialer,
	)
	go riderConsumer.Start(ctx)
	slog.Info("kafka rider consumer started", "topic", riderTopic)

	// 9b. Background workers
	go workers.StartCleanupWorker(ctx, session)
	go workers.StartReconciliationWorker(ctx, session, rdb)
	if dbPool != nil && pgStore != nil {
		go workers.StartDigestWorker(ctx, dbPool, pgStore, session)
		slog.Info("digest worker started")
	}
	slog.Info("background workers started")

	// 10. HTTP Server
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())

	// Realtime SSE gateway. Registered BEFORE notifHandler.RegisterRoutes
	// because notifHandler.RegisterRoutes installs RequireInternalKey via
	// r.Use(...) which would otherwise gate this end-user endpoint behind
	// the service-to-service key.
	rtSecret := env("REALTIME_TOKEN_SECRET", internalKey)
	if rtSecret == "" {
		slog.Warn("REALTIME_TOKEN_SECRET not set — realtime gateway disabled")
	} else {
		realtimeHandler := http.NewRealtimeHandler([]byte(rtSecret), rdb)
		realtimeHandler.Register(r)
		slog.Info("realtime SSE gateway registered", "path", "/v1/realtime/sse")
	}

	notifHandler.RegisterRoutes(r)

	// 11. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			rdb.Close()
			session.Close()
			if dbPool != nil {
				dbPool.Close()
			}
			slog.Info("cleanup completed")
		},
	}); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func collectDBPoolStats(ctx context.Context, pool *pgxpool.Pool, m *metrics.DBPoolMetrics) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stat := pool.Stat()
			m.Update(metrics.PgxPoolStat{
				AcquireCount:  stat.AcquireCount(),
				AcquiredConns: stat.AcquiredConns(),
				IdleConns:     stat.IdleConns(),
				TotalConns:    stat.TotalConns(),
				MaxConns:      stat.MaxConns(),
			})
		}
	}
}

// ensureNotifSchema creates Postgres tables for notification preferences and devices.
func ensureNotifSchema(ctx context.Context, db *pgxpool.Pool) {
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS notification_preferences (
			user_id          UUID PRIMARY KEY,
			email_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
			push_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
			sms_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
			quiet_hours_start TIME,
			quiet_hours_end   TIME,
			muted_types      JSONB NOT NULL DEFAULT '[]',
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS user_devices (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id    UUID NOT NULL,
			platform   TEXT NOT NULL CHECK (platform IN ('ios', 'android', 'web')),
			push_token TEXT NOT NULL,
			is_active  BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(user_id, platform, push_token)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_devices_user ON user_devices (user_id) WHERE is_active = TRUE`,
	}
	for _, stmt := range ddl {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("notification schema migration", "error", err)
		}
	}
	slog.Info("notification preferences schema ensured")
}
