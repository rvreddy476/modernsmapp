package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	dbschema "github.com/atpost/live-service-v2/database"
	v2http "github.com/atpost/live-service-v2/internal/http"
	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/purge"
	"github.com/atpost/live-service-v2/internal/service"
	pgstore "github.com/atpost/live-service-v2/internal/store/postgres"

	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/outbox"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logging.Init(logging.Config{ServiceName: "live-service-v2"})

	port := env("HTTP_PORT", "8117")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := env("REDIS_ADDR", "redis:6379")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	kafkaTopic := env("KAFKA_TOPIC", "social.events.v1")

	graphURL := env("GRAPH_SERVICE_URL", "")
	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
	mediaURL := os.Getenv("MEDIA_SERVICE_URL")

	s3Endpoint := env("MINIO_ENDPOINT", "http://minio:9000")
	lkCfg := livekit.Config{
		APIKey:           os.Getenv("LIVEKIT_API_KEY"),
		APISecret:        os.Getenv("LIVEKIT_API_SECRET"),
		URL:              env("LIVEKIT_URL", "ws://livekit:7880"),
		PublicURL:        env("LIVEKIT_PUBLIC_URL", ""),
		S3Endpoint:       s3Endpoint,
		EgressS3Endpoint: env("LIVE_EGRESS_S3_ENDPOINT", s3Endpoint),
		S3AccessKey:      os.Getenv("MINIO_ACCESS_KEY"),
		S3SecretKey:      os.Getenv("MINIO_SECRET_KEY"),
		S3Bucket:         env("MINIO_BUCKET_LIVE_RECORDINGS", "live-recordings"),
		S3Region:         env("MINIO_REGION", "us-east-1"),
		S3UseSSL:         envBool("MINIO_USE_SSL", false),
	}

	ctx := context.Background()

	// --- Postgres ---
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
		slog.Error("postgres connect", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()
	if err := dbPool.Ping(ctx); err != nil {
		slog.Error("postgres ping", "error", err)
		os.Exit(1)
	}
	if err := pgstore.BootstrapSchema(ctx, dbPool, dbschema.SetupSQL, dbschema.Migrations); err != nil {
		slog.Error("bootstrap schema", "error", err)
		os.Exit(1)
	}
	slog.Info("live-v2 schema ready")

	// --- Redis ---
	rdb, err := transport.NewRedisClientFromEnv(redisAddr)
	if err != nil {
		slog.Error("redis init", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping", "error", err)
		os.Exit(1)
	}

	// --- Kafka ---
	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("kafka dialer", "error", err)
		os.Exit(1)
	}
	// Lifecycle events (live.stream.started|ended|vod_ready) are enqueued in
	// live_v2.outbox_events in the same transaction as the status change;
	// this publisher drains them to Kafka (commerce-service's pattern).
	bgCtx, bgCancel := context.WithCancel(ctx)
	defer bgCancel()
	outboxPublisher := outbox.New(dbPool, outbox.Config{
		DBSchema:     "live_v2",
		KafkaBrokers: strings.Join(kafkaBrokers, ","),
		DefaultTopic: kafkaTopic,
	})
	go outboxPublisher.Run(bgCtx)
	slog.Info("live-v2 outbox publisher started", "topic", kafkaTopic)

	// --- LiveKit + graph clients ---
	lk := livekit.New(lkCfg)
	graph := service.NewHTTPGraphClient(graphURL, internalKey)

	// Who may go live: LIVE_PILOT_USER_IDS. Empty = nobody (fail closed).
	pilot, badPilot := service.ParsePilotUserIDs(os.Getenv("LIVE_PILOT_USER_IDS"))
	if len(badPilot) > 0 {
		slog.Warn("live-v2: LIVE_PILOT_USER_IDS has invalid entries; ignored", "count", len(badPilot))
	}
	if len(pilot) == 0 {
		slog.Warn("live-v2: LIVE_PILOT_USER_IDS is empty — nobody can go live (LIVE_NOT_ENABLED)")
	}
	// media-service recording import: internal key plus, when configured, a
	// live-service-v2 service token (required by media-service outside
	// local/dev).
	importSigner, err := service.ImportSignerFromEnv(os.Getenv)
	if err != nil {
		slog.Error("live-v2: LIVE_SERVICE_TOKEN_KID/PRIVKEY invalid", "error", err)
		os.Exit(1)
	}
	if importSigner == nil {
		slog.Warn("live-v2: LIVE_SERVICE_TOKEN_KID/PRIVKEY not set — recording imports carry no service token (accepted on local/dev only)")
	}
	var media service.MediaImporter
	if imp := service.NewHTTPMediaImporter(mediaURL, internalKey, importSigner); imp != nil {
		media = imp
	} else {
		slog.Warn("live-v2: MEDIA_SERVICE_URL not set — recordings are kept but never imported, so no vod_ready is emitted")
	}

	// Live surfaces (2 Oct 2026): creator cards from identity-profile,
	// categories and channel subscriptions from post-service. Each degrades
	// on its own when unreachable (cards carry the user id only; a category
	// is accepted only if seen before; the Following filter lists nothing).
	profileURL := env("PROFILE_SERVICE_URL", "http://identity-profile:8098")
	postURL := env("POST_SERVICE_URL", "http://post-service:8084")
	var profiles service.ProfileSource
	if c := service.NewHTTPProfiles(profileURL, internalKey); c != nil {
		profiles = c
	}
	var categories service.CategorySource
	if c := service.NewHTTPCategories(postURL, internalKey); c != nil {
		categories = c
	}
	var following service.FollowingSource
	if c := service.NewHTTPFollowing(graphURL, postURL, internalKey); c != nil {
		following = c
	} else {
		slog.Warn("live-v2: GRAPH_SERVICE_URL or POST_SERVICE_URL not set — the Following filter lists nothing")
	}

	// Founding creator badge: a stream on air for at least
	// LIVE_FOUNDING_MIN_LIVE (default 5m) that started before
	// LIVE_FOUNDING_CREATOR_UNTIL (RFC3339; unset = the window is open).
	// An unparseable window refuses to boot rather than staying open.
	foundingUntil, err := service.ParseFoundingUntil(os.Getenv("LIVE_FOUNDING_CREATOR_UNTIL"))
	if err != nil {
		slog.Error("live-v2: founding creator window invalid", "error", err)
		os.Exit(1)
	}

	store := pgstore.New(dbPool)
	store.SetFoundingRule(pgstore.FoundingRule{
		MinLive: envDuration("LIVE_FOUNDING_MIN_LIVE", pgstore.DefaultFoundingMinLive),
		Until:   foundingUntil,
	})
	svc := service.New(store, lk, graph, rdb, service.Config{
		RecordingPublicBaseURL: env("LIVE_RECORDING_PUBLIC_BASE_URL", ""),
		S3Bucket:               lkCfg.S3Bucket,
		S3Endpoint:             lkCfg.S3Endpoint,
		PilotUserIDs:           pilot,
		StartTimeout:           envDuration("LIVE_START_TIMEOUT", service.DefaultStartTimeout),
		ReconnectGrace:         envDuration("LIVE_RECONNECT_GRACE", service.DefaultReconnectGrace),
		EncoderStartTimeout:    envDuration("LIVE_ENCODER_START_TIMEOUT", service.DefaultEncoderStartTimeout),
		Media:                  media,
		Profiles:               profiles,
		Categories:             categories,
		Following:              following,
	})
	// Timeouts by the database clock, LiveKit reconcile, recording imports.
	go svc.RunSweeper(bgCtx, service.SweepInterval)

	// Account control (auth-service 30-day deletion): end the creator's live
	// streams on user.deactivated / user.deletion_scheduled, and on
	// user.purge_requested erase every row keyed by the user in one
	// transaction and ack as "live-v2" onto platform.purge-acks.v1.
	purgeAcks := purge.NewKafkaAckPublisher(kafkaBrokers, env("PURGE_ACKS_TOPIC", purge.DefaultAcksTopic), kafkaDialer)
	defer purgeAcks.Close()
	lifecycle := purge.NewConsumer(kafkaBrokers, env("IDENTITY_KAFKA_TOPIC", "identity.events.v1"),
		"live-service-v2-account-lifecycle", kafkaDialer,
		purge.NewHandler("live-v2", store, purgeAcks, store, slog.Default()), slog.Default())
	defer lifecycle.Close()
	go lifecycle.Start(ctx)

	handler := v2http.New(svc)
	if internalKey != "" {
		handler.WithInternalKey(internalKey)
		slog.Info("live-v2: internal-service-key gate enabled")
	} else {
		slog.Warn("live-v2: INTERNAL_SERVICE_KEY not set — every v1 endpoint is unauthenticated. Do not run this configuration in production.")
	}
	// LiveKit signs webhooks with the API key/secret (webhook.api_key in
	// livekit.yaml). Unset = every webhook is refused, never accepted.
	handler.WithWebhookCredentials(lkCfg.APIKey, lkCfg.APISecret)
	if lkCfg.APIKey == "" || lkCfg.APISecret == "" {
		slog.Warn("live-v2: LIVEKIT_API_KEY/SECRET not set — every LiveKit webhook is refused")
	}
	adminVerifier, err := v2http.ServiceCallersFromEnv(os.Getenv)
	if err != nil {
		slog.Error("live-v2: SERVICE_CALLERS invalid", "error", err)
		os.Exit(1)
	}
	if adminVerifier != nil {
		handler.WithServiceVerifier(adminVerifier)
		slog.Info("live-v2: admin token family enabled", "callers", adminVerifier.Callers())
	} else {
		slog.Warn("live-v2: SERVICE_CALLERS not set — /v1/livestream/internal/admin answers 401")
	}

	// --- Prometheus + health ---
	httpMetrics := metrics.NewHTTPMetrics("live-service-v2")
	checker := health.New("live-service-v2")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

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
			bgCancel()
			dbPool.Close()
			rdb.Close()
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

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// envDuration reads a Go duration ("60s", "2m") or a bare number of
// seconds; anything unparseable or non-positive falls back.
func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	slog.Warn("live-v2: invalid duration; using the default", "key", key, "default", fallback.String())
	return fallback
}
