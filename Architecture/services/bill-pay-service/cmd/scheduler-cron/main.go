// Command scheduler-cron walks billpay.scheduled_payments WHERE next_run_date
// <= today AND is_active and executes Pay() for each. Monthly schedules
// auto-advance by 30 days; one-off schedules deactivate. Run daily.
package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/bill-pay-service/database"
	billpayevents "github.com/atpost/bill-pay-service/internal/events"
	"github.com/atpost/bill-pay-service/internal/service"
	"github.com/atpost/bill-pay-service/internal/setu"
	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/atpost/bill-pay-service/internal/wallet"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/transport"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "bill-pay-scheduler-cron"})

	pgDSN := os.Getenv("POSTGRES_DSN")
	kafkaBrokers := strings.Split(envDefault("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := envDefault("KAFKA_TOPIC", "billpay-events")
	walletURL := envDefault("WALLET_SERVICE_URL", "http://wallet-service:8114")
	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
	setuMode := strings.ToLower(envDefault("SETU_MODE", "mock"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		slog.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := database.BootstrapSchema(ctx, pool); err != nil {
		slog.Error("bootstrap schema", "error", err)
		os.Exit(1)
	}

	dialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("kafka dialer", "error", err)
		os.Exit(1)
	}
	producer := billpayevents.NewProducerWithDialer(kafkaBrokers, kafkaTopic, dialer)
	defer func() { _ = producer.Close() }()

	var setuClient setu.SetuClient
	if setuMode == "http" {
		setuClient = setu.NewHTTPClient(
			envDefault("SETU_BASE_URL", "https://prod.setu.co"),
			os.Getenv("SETU_CLIENT_ID"),
			os.Getenv("SETU_CLIENT_SECRET"),
			os.Getenv("SETU_WEBHOOK_SECRET"),
		)
	} else {
		setuClient = setu.NewMockClient()
	}
	walletClient := wallet.NewHTTPClient(walletURL, internalKey)

	svc := service.New(store.New(pool), setuClient, walletClient, service.Config{})
	svc.SetProducer(producer)

	executed, failed, err := svc.RunScheduledCron(ctx, time.Now())
	if err != nil {
		slog.Error("run scheduled cron", "error", err)
		os.Exit(1)
	}
	slog.Info("scheduler cron complete", "executed", executed, "failed", failed)
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
