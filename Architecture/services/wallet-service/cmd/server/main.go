// Command wallet-service runs the consumer-wallet HTTP server.
//
// BC-of-PPI MODEL: this service holds no money. It mirrors a partner-bank
// PPI for UX speed and audit. See database/setup.sql + the package docs in
// internal/bank for the integration boundary.
package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/atpost/wallet-service/database"
	"github.com/atpost/wallet-service/internal/bank"
	walletevents "github.com/atpost/wallet-service/internal/events"
	wallethttp "github.com/atpost/wallet-service/internal/http"
	"github.com/atpost/wallet-service/internal/service"
	"github.com/atpost/wallet-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "wallet-service"})

	port := env("HTTP_PORT", "8114")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := env("KAFKA_TOPIC", "wallet-events")

	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
	partnerVPA := env("PARTNER_BANK_VPA", "atpostwallet@partnerbank")
	poolBankRef := env("PARTNER_BANK_POOL_REF", "mock-ppi-pool")
	bankPartner := strings.ToLower(env("BANK_PARTNER", "mock"))
	digilockerBase := os.Getenv("DIGILOCKER_BASE_URL")
	digilockerRedirect := env("DIGILOCKER_REDIRECT_URI", "https://atpost.app/wallet/kyc/aadhaar/callback")

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
		slog.Error("failed to bootstrap wallet schema", "error", err)
		os.Exit(1)
	}
	slog.Info("wallet schema ready")

	httpMetrics := metrics.NewHTTPMetrics("wallet-service")
	dbMetrics := metrics.NewDBPoolMetrics("wallet-service", "postgres")
	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	checker := health.New("wallet-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	walletStore := store.New(dbPool)

	// Partner-bank client. Default mock; production must explicitly opt in
	// to icici via BANK_PARTNER=icici. Mirrors the dating-service Razorpay
	// pattern (Sprint 5).
	var bankClient bank.BankClient
	switch bankPartner {
	case "icici":
		bankClient = bank.NewHTTPClient(
			os.Getenv("ICICI_BASE_URL"),
			os.Getenv("ICICI_API_KEY"),
			os.Getenv("ICICI_BC_ID"),
		)
		slog.Info("partner bank client: icici (HTTP)")
	default:
		bankClient = bank.NewMockClient()
		slog.Info("partner bank client: mock (set BANK_PARTNER=icici for production)")
	}

	walletSvc := service.New(walletStore, bankClient, service.Config{
		PartnerBankVPA: partnerVPA,
		AppDisplayName: "AtPost Wallet",
		PoolBankRef:    poolBankRef,
	})

	producer := walletevents.NewProducerWithDialer(kafkaBrokers, kafkaTopic, kafkaDialer)
	walletSvc.SetProducer(producer)
	slog.Info("kafka producer initialized", "topic", kafkaTopic)

	walletHandler := wallethttp.New(walletSvc, internalKey, digilockerBase, digilockerRedirect, nil)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	walletHandler.RegisterRoutes(r)

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
