package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/dating-service/internal/digilocker"
	datingevents "github.com/atpost/dating-service/internal/events"
	datinghttp "github.com/atpost/dating-service/internal/http"
	"github.com/atpost/dating-service/internal/matcher"
	"github.com/atpost/dating-service/internal/moderation"
	"github.com/atpost/dating-service/internal/payments"
	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	_ "github.com/atpost/dating-service/internal/telemetry" // register Pulse gauges
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
	logging.Init(logging.Config{ServiceName: "dating-service"})

	port := env("HTTP_PORT", "8112")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := env("KAFKA_TOPIC", "dating-events")

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
		slog.Error("failed to bootstrap dating schema", "error", err)
		os.Exit(1)
	}
	slog.Info("dating schema ready")

	// Sprint 5: seed the premium plan catalogue. Idempotent.
	if err := store.New(dbPool).SeedPremiumPlans(ctx); err != nil {
		slog.Error("failed to seed premium plans", "error", err)
		os.Exit(1)
	}
	slog.Info("premium plans seeded")

	httpMetrics := metrics.NewHTTPMetrics("dating-service")
	dbMetrics := metrics.NewDBPoolMetrics("dating-service", "postgres")
	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	checker := health.New("dating-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	datingStore := store.New(dbPool)
	datingSvc := service.New(datingStore, rdb)

	graphProvider := matcher.NewHTTPGraphProvider(
		os.Getenv("GRAPH_SERVICE_URL"),
		os.Getenv("COMMUNITY_SERVICE_URL"),
	)
	datingSvc.SetGraphProvider(graphProvider)

	producer := datingevents.NewProducerWithDialer(kafkaBrokers, kafkaTopic, kafkaDialer)
	datingSvc.SetProducer(producer)
	slog.Info("kafka producer initialized", "topic", kafkaTopic)

	// Sprint 3: message-service client for the match-formation saga.
	datingSvc.SetMessageClient(service.NewHTTPMessageClient())
	slog.Info("message-service client initialized")

	// Sprint 4: media-service embedding fetch (selfie face match).
	datingSvc.SetMediaServiceClient(service.NewHTTPMediaClient())

	// Sprint 4: DigiLocker partner client. Default mode is "mock" for
	// safety; production must explicitly set DIGILOCKER_MODE=http.
	// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
	switch strings.ToLower(env("DIGILOCKER_MODE", "mock")) {
	case "http":
		datingSvc.SetDigiLockerClient(digilocker.NewHTTPClient(
			os.Getenv("DIGILOCKER_BASE_URL"),
			os.Getenv("DIGILOCKER_API_KEY"),
			env("DIGILOCKER_SANDBOX", "true") == "true",
		))
		slog.Info("digilocker http client initialized")
	default:
		datingSvc.SetDigiLockerClient(digilocker.NewMockClient())
		slog.Info("digilocker mock client initialized (set DIGILOCKER_MODE=http for production)")
	}

	// Sprint 4: graph + community clients for vouching eligibility checks.
	datingSvc.SetGraphServiceClient(service.NewHTTPGraphServiceClient())
	datingSvc.SetCommunityServiceClient(service.NewHTTPCommunityServiceClient())

	// Sprint 4: feature flag client for moderation strict-mode gate.
	// SHADOW MODE (default): pulse_moderation_strict=false → no user
	// action is taken regardless of confidence. The flag must be flipped
	// in feature-flag-service before strict mode runs.
	datingSvc.SetFeatureFlagsClient(service.NewHTTPFeatureFlagsClient())

	// Sprint 4: LLM moderation client (layer 2). Mock if URL unset.
	if os.Getenv("LLM_MODERATION_URL") != "" {
		datingSvc.SetModerationLLMClient(moderation.NewHTTPClient())
		slog.Info("llm moderation http client initialized")
	} else {
		datingSvc.SetModerationLLMClient(moderation.NewMockClient())
		slog.Info("llm moderation mock client initialized (set LLM_MODERATION_URL for production)")
	}

	// Sprint 5: Razorpay client. Default mode is "mock" for safety;
	// production must explicitly set RAZORPAY_MODE=http.
	switch strings.ToLower(env("RAZORPAY_MODE", "mock")) {
	case "http":
		datingSvc.SetRazorpayClient(payments.NewHTTPClient(
			os.Getenv("RAZORPAY_KEY_ID"),
			os.Getenv("RAZORPAY_KEY_SECRET"),
			os.Getenv("RAZORPAY_WEBHOOK_SECRET"),
			os.Getenv("RAZORPAY_BASE_URL"),
		))
		slog.Info("razorpay http client initialized")
	default:
		datingSvc.SetRazorpayClient(payments.NewMockClient())
		slog.Info("razorpay mock client initialized (set RAZORPAY_MODE=http for production)")
	}

	// Sprint 5: DPDP wiring. Producer doubles as the export-event publisher
	// because *events.Producer satisfies service.DataExportPublisher.
	datingSvc.SetDataExportPublisher(producer)
	if v := os.Getenv("DPDP_POLICY_VERSION"); v != "" {
		datingSvc.SetConsentPolicyVersion(v)
	}

	consumer := datingevents.NewConsumerWithDialer(kafkaBrokers, "dating-service-consumer", datingStore, kafkaDialer)
	consumer.SetLayer2Processor(datingSvc)
	consumerCtx, cancelConsumer := context.WithCancel(ctx)
	go consumer.Start(consumerCtx)
	slog.Info("kafka consumer started")

	datingHandler := datinghttp.New(datingSvc)

	// P0-2 (PRODUCTION_GAP_ANALYSIS.md): gate every /v1/dating/* route
	// behind the shared internal-service-key. The api-gateway strips any
	// inbound X-Internal-Service-Key from public clients and injects the
	// trusted one before forwarding, so dating-service is reachable only
	// via the gateway. Without this, anyone reaching the pod could spoof
	// X-User-Id and impersonate any user. Empty key keeps the dev loop
	// unblocked but emits a loud warning.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		datingHandler.WithInternalKey(key)
		slog.Info("dating-service: internal-service-key gate enabled")
	} else {
		slog.Warn("dating-service: INTERNAL_SERVICE_KEY not set — every /v1/dating/* endpoint is unauthenticated. DO NOT run this configuration in production.")
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	datingHandler.RegisterRoutes(r)

	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			cancelConsumer()
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
