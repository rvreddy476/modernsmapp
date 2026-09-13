package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/food-service/database"
	"github.com/atpost/food-service/internal/foodpii"
	foodhttp "github.com/atpost/food-service/internal/http"
	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/food-service/internal/payout"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/blob"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/identityroles"
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
	logging.Init(logging.Config{ServiceName: "food-service"})

	port := env("HTTP_PORT", "8113")
	pgDSN := os.Getenv("POSTGRES_DSN")
	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := env("KAFKA_TOPIC", "food-events")

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
	if err := postgres.BootstrapSchema(ctx, dbPool, database.SetupSQL); err != nil {
		slog.Error("failed to bootstrap food schema", "error", err)
		os.Exit(1)
	}
	slog.Info("food schema ready")

	httpMetrics := metrics.NewHTTPMetrics("food-service")
	dbMetrics := metrics.NewDBPoolMetrics("food-service", "postgres")
	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	checker := health.New("food-service")
	checker.Register("postgres", health.PingCheck(dbPool))

	// FOOD_RESTAURANT_TIMEZONE / FOOD_DEFAULT_DELIVERY_RADIUS_KM /
	// FOOD_AVG_RIDER_SPEED_KMH. A bad value stops startup rather than silently
	// widening the delivery radius.
	orderingCfg, err := postgres.OrderingConfigFromEnv()
	if err != nil {
		slog.Error("invalid ordering config", "error", err)
		os.Exit(1)
	}
	store := postgres.New(dbPool).
		WithRoleIntents(identityroles.NewOutbox("", "food-service")).
		WithOrderingConfig(orderingCfg)
	svc := service.New(store)

	// Wave 1 B1/B2: restaurant PAN and payout-account sealing. FOOD_PII_KEYS
	// ("v1:<base64 32-byte key>[,v2:<key>]") and FOOD_PII_LOOKUP_SALT are
	// required unless ENV is local/dev/development; locally without them the
	// compliance and payout-account routes answer 503 PII_NOT_CONFIGURED
	// instead of ever writing plaintext.
	piiCrypto, err := foodpii.FromEnv(ctx, os.Getenv)
	if err != nil {
		slog.Error("food-service: PII sealing is not configured", "error", err)
		os.Exit(1)
	}
	if piiCrypto == nil {
		slog.Warn("food-service: FOOD_PII_KEYS / FOOD_PII_LOOKUP_SALT unset (local/dev) — " +
			"compliance and payout-account routes answer 503 PII_NOT_CONFIGURED")
	}
	svc.WithPII(piiCrypto)
	// FOOD_PENNY_DROP_ENABLED defaults false: payout accounts stay NOT_VERIFIED
	// (verification_pending_ops). Payouts are off; nothing calls a transfer.
	bankVerifier, err := payout.VerifierFromEnv(os.Getenv)
	if err != nil {
		slog.Error("food-service: bank verifier", "error", err)
		os.Exit(1)
	}
	svc.WithBankVerifier(bankVerifier)

	// Payments-service client: food-service's own Ed25519 service token
	// (FOOD_SERVICE_TOKEN_KEY / FOOD_SERVICE_TOKEN_KID). Without a key it
	// falls back to the shared internal key ONLY when ENV is local/dev; any
	// other ENV, including a blank one, refuses to start.
	pmClient, err := payments.ClientFromEnv(os.Getenv)
	if err != nil {
		slog.Error("food-service: payments client could not be built", "error", err)
		os.Exit(1)
	}
	if pmClient.LegacyAuth() {
		slog.Warn("food-service: FOOD_SERVICE_TOKEN_KEY not set — payments calls carry the shared internal key " +
			"(legacy mode, local/dev only). Issue a signing key and register food-service in payments' SERVICE_CALLERS.")
	} else {
		slog.Info("food-service: payments client ready (service-token auth)")
	}
	svc.WithPayments(pmClient)
	// FOOD_COD_ENABLED / FOOD_WALLET_PAYMENTS_ENABLED default off: online only.
	payFlags := payments.FlagsFromEnv(os.Getenv)
	svc.WithPaymentFlags(payFlags)
	slog.Info("food-service: payment methods", "cod_enabled", payFlags.CODEnabled, "wallet_enabled", payFlags.WalletEnabled)

	// Realtime: best-effort Pub/Sub publishes + topic-token signer.
	// REALTIME_TOKEN_SECRET must match notification-service's verifier.
	if rtSecret := env("REALTIME_TOKEN_SECRET", internalKey); rtSecret != "" {
		redisAddr := os.Getenv("REDIS_ADDR")
		if rdb, err := transport.NewRedisClientFromEnv(redisAddr); err == nil {
			svc.WithRealtime(
				realtime.NewPublisher(rdb),
				realtime.NewTokenSigner([]byte(rtSecret)),
			)
			slog.Info("food-service realtime wired", "redis", redisAddr)
		} else {
			slog.Warn("food-service: redis unavailable, realtime disabled", "error", err)
		}
	}

	// P0.3 — durable outbox publisher. Domain events PlaceOrder /
	// ConfirmPayment / CancelOrder enqueue here (via service.emit);
	// this publisher drains the table and retries on Kafka outage.
	outboxCtx, outboxCancel := context.WithCancel(ctx)
	defer outboxCancel()
	outboxPublisher := outbox.New(dbPool, outbox.Config{
		// food.outbox_events: food shares the app database, whose public
		// outbox_events belongs to another declaration.
		DBSchema:     "food",
		KafkaBrokers: strings.Join(kafkaBrokers, ","),
		DefaultTopic: kafkaTopic,
	})
	go outboxPublisher.Run(outboxCtx)
	slog.Info("outbox publisher started", "topic", kafkaTopic)

	// Identity role worker. Drains identity_role_intents — rows the partner
	// lifecycle commits alongside the partner row itself — into
	// identity-auth-service's internal role API.
	//
	// The default host is identity-auth:8081, the compose service name.
	// commerce-service's compose block records what happens when a client
	// defaults to `auth-service` instead: the calls fail silently forever.
	identityAuthURL := env("IDENTITY_AUTH_URL", env("AUTH_SERVICE_URL", "http://identity-auth:8081"))
	roleWorker := identityroles.NewWorker(
		identityroles.NewClient(identityAuthURL, internalKey, "food-service"),
		identityroles.NewOutbox("", "food-service"),
		dbPool, slog.Default(), identityroles.WorkerConfig{},
	)
	go roleWorker.Run(outboxCtx)
	slog.Info("identity role worker started", "identity_auth_url", identityAuthURL)
	if internalKey == "" {
		// Loud on purpose: without the key every grant 403s and dead-letters
		// on its first attempt, so no partner ever holds their role.
		slog.Warn("INTERNAL_SERVICE_KEY is empty; identity role grants will be rejected")
	}

	svc.WithOutbox(outbox.NewQueuer("food"), dbPool)

	// Payment events: the ONLY path that marks a food order paid, failed or
	// refunded. Inbox row + decision + guarded transition in one transaction.
	paymentConsumer := payments.NewConsumer(store, kafkaBrokers, nil, svc.OnPaymentEventApplied)
	go paymentConsumer.Start(outboxCtx)
	defer paymentConsumer.Close()
	slog.Info("payment event consumer started", "topic", "social.events.v1", "group", "food-payments")

	// MinIO for settlement-file offload. Optional — when the env is
	// absent or the client fails to connect, settlement files keep
	// living inline in food.settlement_files.body and the download
	// handler streams from there. See internal/store/blob/store.go.
	if minioEndpoint := os.Getenv("MINIO_ENDPOINT"); minioEndpoint != "" {
		bucket := env("FOOD_BLOB_BUCKET", "food")
		useSSL := strings.EqualFold(env("MINIO_USE_SSL", "false"), "true")
		blobStore, err := blob.New(
			minioEndpoint,
			os.Getenv("MINIO_ACCESS_KEY"),
			os.Getenv("MINIO_SECRET_KEY"),
			bucket,
			useSSL,
			os.Getenv("MINIO_PUBLIC_ENDPOINT"),
		)
		if err != nil {
			slog.Warn("food-service: MinIO unavailable, settlement files stay inline",
				"endpoint", minioEndpoint, "error", err)
		} else {
			svc.WithBlobStore(blobStore)
			slog.Info("food-service: MinIO wired", "bucket", bucket)
		}
	}

	// B1: SLA auto-reject worker. Scans every 15s for CONFIRMED orders
	// past their accept_deadline_at and transitions them to
	// RESTAURANT_REJECTED so the customer is refunded promptly.
	go svc.StartSLAAutoRejectWorker(outboxCtx)

	// B4: delivery offer dispatch worker. Expires stale offers + fans
	// out new offers to up to 5 nearby online partners per ready order.
	go svc.StartDeliveryDispatchWorker(outboxCtx)

	// E: fraud score worker. Runs every 6h, writes per-user signals
	// (refund_abuse + coupon_burn) into food.fraud_scores so the
	// admin queue can triage high-risk customers.
	go svc.StartFraudScoreWorker(outboxCtx)

	handler := foodhttp.New(svc).WithInternalKey(internalKey)

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
