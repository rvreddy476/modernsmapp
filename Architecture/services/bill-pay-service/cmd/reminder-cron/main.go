// Command reminder-cron walks billpay.reminders joined with the latest
// fetched bill per account and emits one billpay.bill.due_soon event per
// row whose due-date is within days_before_due of today. Run daily.
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
	logging.Init(logging.Config{ServiceName: "bill-pay-reminder-cron"})

	pgDSN := os.Getenv("POSTGRES_DSN")
	kafkaBrokers := strings.Split(envDefault("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := envDefault("KAFKA_TOPIC", "billpay-events")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
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

	// Reminder cron does not call Setu or Wallet — but the Service constructor
	// requires both. Wire mocks/no-ops; they will not be exercised on the
	// reminder code path.
	svc := service.New(store.New(pool), setu.NewMockClient(), wallet.NewMockClient(), service.Config{})
	svc.SetProducer(producer)

	count, err := svc.RunReminderCron(ctx, time.Now())
	if err != nil {
		slog.Error("run reminder cron", "error", err)
		os.Exit(1)
	}
	slog.Info("reminder cron complete", "fired", count)
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
