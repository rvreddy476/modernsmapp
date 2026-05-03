// Command setu-webhook-receiver is a thin standalone binary that listens for
// Setu BBPS webhooks if you choose to expose webhook delivery via a separate
// pod (operationally useful when the main HTTP service is bursty and the
// webhook traffic must NOT be queued behind user requests).
//
// In v1 the main bill-pay-service binary handles webhooks at
// /v1/billpay/internal/setu-webhook directly; this binary mirrors the same
// route on a dedicated port so a future deploy split is a one-line config
// change. Keep idempotent: webhook-handling logic is the same idempotent
// HandleSetuWebhook from internal/service/payments.go.
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
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "bill-pay-webhook-receiver"})

	port := envDefault("HTTP_PORT", "8116")
	pgDSN := os.Getenv("POSTGRES_DSN")
	kafkaBrokers := strings.Split(envDefault("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := envDefault("KAFKA_TOPIC", "billpay-events")
	walletURL := envDefault("WALLET_SERVICE_URL", "http://wallet-service:8114")
	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
	setuMode := strings.ToLower(envDefault("SETU_MODE", "mock"))
	setuWebhookSecret := os.Getenv("SETU_WEBHOOK_SECRET")

	ctx := context.Background()
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

	var setuClient setu.SetuClient
	if setuMode == "http" {
		setuClient = setu.NewHTTPClient(
			envDefault("SETU_BASE_URL", "https://prod.setu.co"),
			os.Getenv("SETU_CLIENT_ID"),
			os.Getenv("SETU_CLIENT_SECRET"),
			setuWebhookSecret,
		)
	} else {
		mock := setu.NewMockClient()
		if setuWebhookSecret != "" {
			mock.SetWebhookSecret(setuWebhookSecret)
		}
		setuClient = mock
	}
	walletClient := wallet.NewHTTPClient(walletURL, internalKey)

	svc := service.New(store.New(pool), setuClient, walletClient, service.Config{})
	svc.SetProducer(producer)

	handler := billpayhttp.New(svc, internalKey)
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	handler.RegisterRoutes(r)

	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			_ = producer.Close()
			pool.Close()
		},
	}); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
