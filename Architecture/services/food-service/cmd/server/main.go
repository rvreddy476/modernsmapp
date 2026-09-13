package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/food-service/database"
	"github.com/atpost/food-service/internal/digilocker"
	"github.com/atpost/food-service/internal/foodpii"
	foodhttp "github.com/atpost/food-service/internal/http"
	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/food-service/internal/payout"
	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/settlement"
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
	// Wave 1 B3: totals through shared/gst. FOOD_PLATFORM_FEE_PAISE (500),
	// FOOD_DELIVERY_FEE_PAISE (2900), FOOD_COUPONS_ENABLED (false) and
	// FOOD_PLATFORM_GSTIN, which is validated whenever set and required unless
	// ENV is local/dev/development.
	pricingCfg, err := pricing.ConfigFromEnv(os.Getenv)
	if err != nil {
		slog.Error("invalid pricing config", "error", err)
		os.Exit(1)
	}
	if pricingCfg.PlatformGSTIN == "" {
		slog.Warn("food-service: FOOD_PLATFORM_GSTIN unset (local/dev) — carts carry pricing_error " +
			"FOOD_PLATFORM_GSTIN_NOT_CONFIGURED and orders are refused with 503")
	}
	// FOOD_COMMISSION_GST_BP (1800) and FOOD_TCS_RATE_BP (50): settlement
	// rates, both pending adviser confirmation.
	settlementRules, err := settlement.RulesFromEnv(os.Getenv)
	if err != nil {
		slog.Error("invalid settlement config", "error", err)
		os.Exit(1)
	}
	slog.Info("food-service: pricing",
		"platform_fee_paise", pricingCfg.PlatformFeePaise, "delivery_fee_paise", pricingCfg.DeliveryFeePaise,
		"coupons_enabled", pricingCfg.CouponsEnabled, "platform_gstin_configured", pricingCfg.PlatformGSTIN != "",
		"commission_gst_bp", settlementRules.CommissionGSTBP, "tcs_rate_bp", settlementRules.TCSRateBP)
	store := postgres.New(dbPool).
		WithRoleIntents(identityroles.NewOutbox("", "food-service")).
		WithOrderingConfig(orderingCfg).
		WithPricingConfig(pricingCfg).
		WithSettlementRules(settlementRules)
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

	// Wave 1 B4: delivery-partner verification. DIGILOCKER_MODE is mock|http|
	// disabled; unset means mock in local/dev and is refused elsewhere, and the
	// mock itself is refused unless ENV is local/dev/development.
	// FOOD_PUBLIC_BASE_URL (gateway origin; redirect_uri and the dev authorize
	// URL are built from it) and FOOD_RIDER_APP_LINK_URL (where the public
	// return route sends the browser). http mode also needs
	// DIGILOCKER_AUTHORIZE_URL, DIGILOCKER_TOKEN_URL,
	// DIGILOCKER_ISSUED_DOCUMENTS_URL, DIGILOCKER_CLIENT_ID and
	// DIGILOCKER_CLIENT_SECRET.
	dlSettings, err := digilocker.SettingsFromEnv(os.Getenv)
	if err != nil {
		slog.Error("food-service: DigiLocker configuration refused", "error", err)
		os.Exit(1)
	}
	svc.WithDigiLocker(dlSettings)
	slog.Info("food-service: digilocker", "mode", dlSettings.Mode,
		"public_base_url_configured", dlSettings.PublicBaseURL != "", "app_link_configured", dlSettings.AppLinkURL != "")
	// Seal delivery-partner document numbers written before B4 and clear the
	// plaintext. Rows it cannot seal keep their value and are counted.
	if piiCrypto != nil {
		backfill, err := svc.BackfillDeliveryDocumentNumbers(ctx)
		if err != nil {
			slog.Error("food-service: delivery document backfill stopped; plaintext numbers remain", "error", err)
		}
		if backfill.AadhaarRefused > 0 || backfill.Conflicts > 0 || backfill.Failed > 0 {
			slog.Warn("food-service: delivery documents still hold plaintext numbers and need ops",
				"aadhaar_refused", backfill.AadhaarRefused, "conflicts", backfill.Conflicts, "failed", backfill.Failed)
		}
		slog.Info("food-service: delivery document backfill", "sealed", backfill.Sealed)
	}

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

	// Realtime (B5a): food events go onto Redis Streams (XADD rts:<topic>),
	// which notification-service's SSE gateway reads with XREAD. Env:
	// REDIS_ADDR (plus REDIS_USERNAME / REDIS_PASSWORD / REDIS_DB and the
	// REDIS_* TLS settings read by shared/transport) and REALTIME_TOKEN_SECRET,
	// falling back to INTERNAL_SERVICE_KEY; it must match notification-service's
	// verifier. Either missing: realtime is DISABLED, said so here, dropped
	// events are WARNed once, and the token route answers 503.
	rtSecret, rtSecretSource := os.Getenv("REALTIME_TOKEN_SECRET"), "REALTIME_TOKEN_SECRET"
	if rtSecret == "" {
		rtSecret, rtSecretSource = internalKey, "INTERNAL_SERVICE_KEY"
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	switch {
	case redisAddr == "":
		slog.Warn("food-service: realtime DISABLED — REDIS_ADDR is unset; no live order, restaurant or rider " +
			"events will be published and POST /v1/food/realtime/token answers 503")
	case rtSecret == "":
		slog.Warn("food-service: realtime DISABLED — neither REALTIME_TOKEN_SECRET nor INTERNAL_SERVICE_KEY is set; " +
			"POST /v1/food/realtime/token answers 503")
	default:
		rdb, err := transport.NewRedisClientFromEnv(redisAddr)
		if err != nil {
			slog.Warn("food-service: realtime DISABLED — redis client could not be built", "redis_addr", redisAddr, "error", err)
			break
		}
		svc.WithRealtime(service.NewRealtimePublisher(rdb), realtime.NewTokenSigner([]byte(rtSecret)))
		pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
		pingErr := rdb.Ping(pingCtx).Err()
		pingCancel()
		if pingErr != nil {
			slog.Warn("food-service: realtime wired (Redis Streams) but redis did not answer a ping; publishes will fail and be logged",
				"redis_addr", redisAddr, "error", pingErr)
		} else {
			slog.Info("food-service: realtime wired (Redis Streams)", "redis_addr", redisAddr,
				"token_secret_source", rtSecretSource, "token_ttl", service.RealtimeTokenTTL)
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

	// B5a: rider presence. Every 30s riders silent for 5 minutes go offline;
	// hourly, rider location history older than 30 days is deleted.
	go svc.StartRiderPresenceWorker(outboxCtx)

	// E: fraud score worker. Runs every 6h, writes per-user signals
	// (refund_abuse + coupon_burn) into food.fraud_scores so the
	// admin queue can triage high-risk customers.
	go svc.StartFraudScoreWorker(outboxCtx)

	handler := foodhttp.New(svc).WithInternalKey(internalKey).
		WithDigiLockerDevRoutes(os.Getenv("ENV"), dlSettings.Mock())

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
