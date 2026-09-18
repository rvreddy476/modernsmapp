// Command rider-service runs the Mopedu HTTP server.
//
// Mopedu is the B2B2C ride mini-app inside AtPost. Customers ride for free;
// partners pay a monthly subscription for ride-lead access. See
// C:\workspace\atpost\mopedu\MOPEDU_SPEC.md for the full vision.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/rider-service/database"
	"github.com/atpost/rider-service/internal/consumers"
	"github.com/atpost/rider-service/internal/digilocker"
	riderevents "github.com/atpost/rider-service/internal/events"
	riderhttp "github.com/atpost/rider-service/internal/http"
	"github.com/atpost/rider-service/internal/pricing"
	"github.com/atpost/rider-service/internal/riderpii"
	"github.com/atpost/rider-service/internal/routing"
	"github.com/atpost/rider-service/internal/runtimeenv"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/rider-service/internal/wallet"
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

	// Fail closed before touching any dependency: a production process with
	// no internal key serves forged identities, and one on the DigiLocker mock
	// verifies every Aadhaar.
	production := runtimeenv.IsProduction(os.Getenv)
	if err := riderhttp.CheckInternalKey(production, internalKey); err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}
	if internalKey == "" {
		slog.Warn("INTERNAL_SERVICE_KEY is empty; /v1/rider accepts forged X-User-Id and X-Scopes from anything that can reach this port (allowed outside production only)")
	}
	dlClient, err := digilocker.New(digilockerMode, production, digilockerBase, digilockerKey, digilockerSandbox)
	if err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}
	slog.Info("digilocker client selected", "mode", digilockerMode, "production", production)

	ctx := context.Background()

	// Ride OTPs are sealed at rest under RIDER_PII_KEYS ("v1:<base64 32-byte
	// key>[,v2:<key>]", the FOOD_PII_KEYS format). Production refuses to start
	// without it; local/dev without it boots and offer acceptance answers
	// OTP_SEALING_NOT_CONFIGURED, never a plaintext OTP.
	otpCrypto, err := riderpii.FromEnv(ctx, os.Getenv)
	if err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}
	if !otpCrypto.Configured() {
		slog.Warn("RIDER_PII_KEYS unset (local/dev): ride OTPs cannot be sealed, so offers cannot be accepted")
	}

	// Pricing engine environment.
	//   MOPEDU_SURGE_CAP_BPS      demand surge ceiling in bps, default 5000.
	//   MOPEDU_COUPONS_ENABLED    default true; "false" in staging/prod until
	//                             the adviser confirms the GST treatment of
	//                             discounts (like FOOD_COUPONS_ENABLED).
	//   MOPEDU_PLATFORM_GSTIN     the platform's GSTIN, liable under s.9(5)
	//                             for ride fares; required in production,
	//                             validated whenever set. Unset outside
	//                             production prices with the flat rider-local
	//                             table (5% / 18%).
	//   GOOGLE_MAPS_SERVER_KEY    optional; Routes API with haversine fallback.
	//   MOPEDU_ROUTING_TIMEOUT_MS optional, default 2000.
	svcCfg, err := pricingConfigFromEnv(os.Getenv, production)
	if err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}
	routingCfg, err := routing.ConfigFromEnv(os.Getenv)
	if err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}
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

	riderStore := store.New(dbPool).WithRoleIntents(identityroles.NewOutbox("rider", "rider-service"))

	walletClient := wallet.NewHTTPClient(walletURL, internalKey)
	slog.Info("wallet client wired", "url", walletURL)

	riderSvc := service.New(riderStore, walletClient, svcCfg)
	riderSvc.SetDigiLockerClient(dlClient)
	riderSvc.SetRedis(rdb)
	riderSvc.SetOTPCrypto(otpCrypto)

	// Routing: Cache(Fallback(GoogleRoutes, Haversine)). No key: haversine
	// only. Google answers are cached in Redis for five minutes.
	var googleRoutes routing.Router
	if routingCfg.GoogleKey != "" {
		googleRoutes = routing.NewGoogleRoutes(routingCfg.GoogleKey, routing.GoogleOptions{Timeout: routingCfg.Timeout})
	}
	riderSvc.SetRouter(routing.CalculatorFromRouter(
		routing.NewCache(rdb, routing.NewFallback(googleRoutes, routing.Haversine{}, slog.Default()), slog.Default()),
	))
	slog.Info("rider-service: pricing",
		"google_routes", googleRoutes != nil, "routing_timeout", routingCfg.Timeout,
		"surge_cap_bps", svcCfg.SurgeCapBPS, "coupons_enabled", svcCfg.CouponsEnabled,
		"platform_gstin_configured", svcCfg.PlatformGSTIN != "", "otp_sealing", otpCrypto.Configured())

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
	// DBSchema "rider": public.outbox_events on the shared app DB belongs
	// to another service with a different column shape — see setup.sql.
	outboxPublisher := outbox.New(dbPool, outbox.Config{
		DBSchema:     "rider",
		KafkaBrokers: strings.Join(kafkaBrokers, ","),
		DefaultTopic: kafkaTopic,
	})
	riderSvc.WithOutbox(outbox.NewQueuer("rider"), dbPool)
	go outboxPublisher.Run(dispatchCtx)
	slog.Info("outbox publisher started", "topic", kafkaTopic)

	// Identity role worker. Drains rider.identity_role_intents — rows the
	// partner lifecycle commits alongside the partner row itself — into
	// identity-auth-service's internal role API.
	//
	// The default host is identity-auth:8081, the compose service name, not
	// auth-service:8081. commerce-service's compose block records what the
	// wrong default costs: calls that fail silently forever.
	identityAuthURL := env("IDENTITY_AUTH_URL", env("AUTH_SERVICE_URL", "http://identity-auth:8081"))
	roleWorker := identityroles.NewWorker(
		identityroles.NewClient(identityAuthURL, internalKey, "rider-service"),
		identityroles.NewOutbox("rider", "rider-service"),
		dbPool, slog.Default(), identityroles.WorkerConfig{},
	)
	go roleWorker.Run(dispatchCtx)
	slog.Info("identity role worker started", "identity_auth_url", identityAuthURL)
	if internalKey == "" {
		// Loud on purpose: without the key every grant 403s and dead-letters
		// on its first attempt, so no partner ever holds `rider_partner`.
		slog.Warn("INTERNAL_SERVICE_KEY is empty; identity role grants will be rejected")
	}

	// P0.2 — dispatch consumer. CreateRide publishes
	// `rider.ride.requested`; without this consumer the ride sat in
	// `requested` forever because nothing called MatchRide.
	kafkaConsumerMetrics := metrics.NewKafkaConsumerMetrics("rider-service")
	dispatchConsumer := consumers.NewDispatchConsumer(
		riderSvc, kafkaBrokers, kafkaTopic, rdb, kafkaConsumerMetrics,
	)
	go dispatchConsumer.Start(dispatchCtx)
	slog.Info("rider dispatch consumer started", "topic", kafkaTopic)

	// C3: stale-GPS auto-offline worker. Pings every 30s and force-
	// offlines partners whose last GPS update is older than 90s.
	go riderSvc.StartStaleGPSWorker(dispatchCtx)

	// G4.5: scheduled-ride activation worker. Promotes scheduled
	// rides to `requested` ≈ T-15 min so the dispatch consumer
	// picks them up like any other ride.
	go riderSvc.StartScheduledRideActivationWorker(dispatchCtx)

	// Service-token verifier for the admin console family
	// (/v1/rider/internal/admin/*): SERVICE_CALLERS=admin-service plus
	// SERVICE_CALLER_ADMIN_SERVICE_{KID,PUBKEY,OPS}. Unset: no token is
	// accepted and that family answers 401. A named caller with a missing
	// key or empty ops refuses to start.
	serviceVerifier, err := riderhttp.ServiceCallersFromEnv(os.Getenv)
	if err != nil {
		slog.Error("rider-service: SERVICE_CALLERS", "error", err)
		os.Exit(1)
	}
	if serviceVerifier == nil {
		slog.Warn("rider-service: SERVICE_CALLERS not set — admin-service tokens are refused; /v1/rider/internal/admin answers 401")
	} else {
		slog.Info("rider-service: service-token callers registered", "callers", serviceVerifier.Callers())
	}

	handler := riderhttp.New(riderSvc, internalKey).WithServiceAuth(serviceVerifier)

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

// pricingConfigFromEnv reads the pricing engine variables (see main).
func pricingConfigFromEnv(getenv func(string) string, production bool) (service.Config, error) {
	cfg := service.Config{}
	if raw := strings.TrimSpace(getenv("MOPEDU_SURGE_CAP_BPS")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 || n > 30000 {
			return cfg, fmt.Errorf("MOPEDU_SURGE_CAP_BPS must be a whole number of basis points between 0 and 30000")
		}
		cfg.SurgeCapBPS = n
	}
	if raw := strings.TrimSpace(getenv("MOPEDU_COUPONS_ENABLED")); raw != "" {
		on, err := strconv.ParseBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("MOPEDU_COUPONS_ENABLED must be true or false")
		}
		cfg.CouponsEnabled, cfg.CouponsFlagSet = on, true
	} else {
		cfg.CouponsEnabled, cfg.CouponsFlagSet = true, true
	}
	raw := strings.TrimSpace(getenv("MOPEDU_PLATFORM_GSTIN"))
	if raw == "" {
		if production {
			return cfg, fmt.Errorf("MOPEDU_PLATFORM_GSTIN is required in production: the platform is liable for GST on ride fares under s.9(5)")
		}
		return cfg, nil
	}
	if _, err := pricing.NewGSTComputer(nil, raw, ""); err != nil {
		return cfg, fmt.Errorf("MOPEDU_PLATFORM_GSTIN is not a valid GSTIN")
	}
	cfg.PlatformGSTIN = raw
	return cfg, nil
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
