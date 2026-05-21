// Command rider-service runs the Mopedu HTTP server.
//
// Mopedu is the B2B2C ride mini-app inside AtPost. Customers ride for free;
// partners pay a monthly subscription for ride-lead access. See
// C:\workspace\atpost\mopedu\MOPEDU_SPEC.md for the full vision.
package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/rider-service/database"
	"github.com/atpost/rider-service/internal/consumers"
	"github.com/atpost/rider-service/internal/digilocker"
	riderevents "github.com/atpost/rider-service/internal/events"
	riderhttp "github.com/atpost/rider-service/internal/http"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/rider-service/internal/wallet"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/outbox"
	"github.com/atpost/shared/realtime"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "rider-service"})

	port := env("HTTP_PORT", "8116")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := env("KAFKA_TOPIC", "rider-events")

	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
	walletURL := env("WALLET_SERVICE_URL", "http://wallet-service:8114")
	digilockerMode := strings.ToLower(env("DIGILOCKER_MODE", "mock"))
	digilockerBase := os.Getenv("DIGILOCKER_BASE_URL")
	digilockerKey := os.Getenv("DIGILOCKER_API_KEY")
	digilockerSandbox := strings.EqualFold(env("DIGILOCKER_SANDBOX", "true"), "true")

	ctx := context.Background()
	poolCfg, err := pgxpool.ParseConfig(pgDSN)
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
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		slog.Error("postgres ping failed", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to postgres")

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

	if err := database.BootstrapSchema(ctx, dbPool); err != nil {
		slog.Error("failed to bootstrap rider schema", "error", err)
		os.Exit(1)
	}
	slog.Info("rider schema ready")

	httpMetrics := metrics.NewHTTPMetrics("rider-service")
	dbMetrics := metrics.NewDBPoolMetrics("rider-service", "postgres")
	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	checker := health.New("rider-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	riderStore := store.New(dbPool)

	// DigiLocker partner client. Default mock; production must explicitly opt
	// in via DIGILOCKER_MODE=http. Mirrors dating-service.
	var dlClient digilocker.Client
	switch digilockerMode {
	case "http":
		dlClient = digilocker.NewHTTPClient(digilockerBase, digilockerKey, digilockerSandbox)
		slog.Info("digilocker client: http")
	default:
		dlClient = digilocker.NewMockClient()
		slog.Info("digilocker client: mock (set DIGILOCKER_MODE=http for production)")
	}

	walletClient := wallet.NewHTTPClient(walletURL, internalKey)
	slog.Info("wallet client wired", "url", walletURL)

	riderSvc := service.New(riderStore, walletClient, service.Config{})
	riderSvc.SetDigiLockerClient(dlClient)
	riderSvc.SetRedis(rdb)

	// Realtime: best-effort Pub/Sub publishes + topic-token signer.
	// REALTIME_TOKEN_SECRET must match notification-service's verifier.
	if rtSecret := env("REALTIME_TOKEN_SECRET", internalKey); rtSecret != "" {
		riderSvc.WithRealtime(
			realtime.NewPublisher(rdb),
			realtime.NewTokenSigner([]byte(rtSecret)),
		)
		slog.Info("rider-service realtime wired")
	}

	producer := riderevents.NewProducerWithDialer(kafkaBrokers, kafkaTopic, kafkaDialer)
	riderSvc.SetProducer(producer)
	slog.Info("kafka producer initialized", "topic", kafkaTopic)

	// One cancel context shared by both background goroutines so
	// shutdown stops them together.
	dispatchCtx, dispatchCancel := context.WithCancel(ctx)
	defer dispatchCancel()

	// P0.3 — durable outbox publisher. New event-publish sites
	// should `outbox.Queuer.Enqueue(ctx, tx, ...)` inside the same
	// tx as the domain write; this publisher drains the table and
	// retries on Kafka outage. The existing direct producer
	// continues to work for legacy paths until they migrate.
	outboxPublisher := outbox.New(dbPool, outbox.Config{
		KafkaBrokers: strings.Join(kafkaBrokers, ","),
		DefaultTopic: kafkaTopic,
	})
	go outboxPublisher.Run(dispatchCtx)
	slog.Info("outbox publisher started", "topic", kafkaTopic)

	// P0.2 — dispatch consumer. CreateRide publishes
	// `rider.ride.requested`; without this consumer the ride sat in
	// `requested` forever because nothing called MatchRide.
	kafkaConsumerMetrics := metrics.NewKafkaConsumerMetrics("rider-service")
	dispatchConsumer := consumers.NewDispatchConsumer(
		riderSvc, kafkaBrokers, kafkaTopic, rdb, kafkaConsumerMetrics,
	)
	go dispatchConsumer.Start(dispatchCtx)
	slog.Info("rider dispatch consumer started", "topic", kafkaTopic)

	handler := riderhttp.New(riderSvc, internalKey)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)

	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			if err := producer.Close(); err != nil {
				slog.Warn("failed to close kafka producer", "error", err)
			}
			rdb.Close()
			dbPool.Close()
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
