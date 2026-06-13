// Command bill-pay-service runs the Bill-pay HTTP server.
//
// Phase 2 D2: Setu is the BBPS aggregator (rail); AtPost is the consumer-facing
// biller. SETU_MODE=mock|http selects between MockClient and HTTPClient.
package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/bill-pay-service/database"
	billpayevents "github.com/atpost/bill-pay-service/internal/events"
	billpayhttp "github.com/atpost/bill-pay-service/internal/http"
	"github.com/atpost/bill-pay-service/internal/service"
	"github.com/atpost/bill-pay-service/internal/setu"
	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/atpost/bill-pay-service/internal/wallet"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "bill-pay-service"})

	port := env("HTTP_PORT", "8115")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := env("KAFKA_TOPIC", "billpay-events")

	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
	walletURL := env("WALLET_SERVICE_URL", "http://wallet-service:8114")
	setuMode := strings.ToLower(env("SETU_MODE", "mock"))
	setuBase := env("SETU_BASE_URL", "https://prod.setu.co")
	setuClientID := os.Getenv("SETU_CLIENT_ID")
	setuClientSecret := os.Getenv("SETU_CLIENT_SECRET")
	setuWebhookSecret := os.Getenv("SETU_WEBHOOK_SECRET")

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
		slog.Error("failed to bootstrap billpay schema", "error", err)
		os.Exit(1)
	}
	slog.Info("billpay schema ready")

	httpMetrics := metrics.NewHTTPMetrics("bill-pay-service")
	dbMetrics := metrics.NewDBPoolMetrics("bill-pay-service", "postgres")
	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	checker := health.New("bill-pay-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	billpayStore := store.New(dbPool)

	// Setu client. Default mock; production must explicitly opt in via
	// SETU_MODE=http. Mirrors the wallet-service BANK_PARTNER pattern.
	var setuClient setu.SetuClient
	switch setuMode {
	case "http":
		setuClient = setu.NewHTTPClient(setuBase, setuClientID, setuClientSecret, setuWebhookSecret)
		slog.Info("setu client: http")
	default:
		mock := setu.NewMockClient()
		if setuWebhookSecret != "" {
			mock.SetWebhookSecret(setuWebhookSecret)
		}
		setuClient = mock
		slog.Info("setu client: mock (set SETU_MODE=http for production)")
	}

	walletClient := wallet.NewHTTPClient(walletURL, internalKey)
	slog.Info("wallet client wired", "url", walletURL)

	billpaySvc := service.New(billpayStore, setuClient, walletClient, service.Config{
		DefaultMobileCircle: env("DEFAULT_MOBILE_CIRCLE", "KA"),
	})

	producer := billpayevents.NewProducerWithDialer(kafkaBrokers, kafkaTopic, kafkaDialer)
	billpaySvc.SetProducer(producer)
	slog.Info("kafka producer initialized", "topic", kafkaTopic)

	handler := billpayhttp.New(billpaySvc, internalKey)

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
