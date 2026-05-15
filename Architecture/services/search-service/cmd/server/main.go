package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/search-service/internal/events"
	"github.com/atpost/search-service/internal/http"
	"github.com/atpost/search-service/internal/reindex"
	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "search-service"})

	// 2. Config
	port := env("HTTP_PORT", "8089")
	opensearchURL := env("OPENSEARCH_URL", "http://opensearch:9200")
	kafkaBrokers := env("KAFKA_BROKERS", "redpanda:9092")
	redisAddr := env("REDIS_ADDR", "redis:6379")

	// 3. OpenSearch Store
	searchStore, err := search.New(opensearchURL)
	if err != nil {
		slog.Error("failed to initialize opensearch store", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to opensearch")

	// 4. Redis
	ctx := context.Background()
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

	// 5. Kafka Consumers — search-service indexes events from BOTH the
	// social events topic (post/feed/graph publish here) AND the
	// identity events topic (auth-service publishes UserRegistered /
	// UserProfileUpdated / HandleChanged here). Without the identity
	// consumer the users_v1 OpenSearch index stays empty no matter how
	// many people register, because identity events never traverse the
	// social topic. Same dual-consumer pattern chat-service uses for
	// its KAFKA_TOPIC + IDENTITY_KAFKA_TOPIC config.
	socialTopic := env("KAFKA_TOPIC", "social.events.v1")
	identityTopic := env("IDENTITY_KAFKA_TOPIC", "identity.events.v1")
	brokerList := strings.Split(kafkaBrokers, ",")

	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer consumerCancel()

	socialConsumer := events.NewConsumerWithDialer(
		brokerList, "search-service-group", socialTopic, searchStore, kafkaDialer,
	)
	go socialConsumer.Start(consumerCtx)
	slog.Info("started kafka consumer", "topic", socialTopic, "group", "search-service-group")

	identityConsumer := events.NewConsumerWithDialer(
		brokerList, "search-service-identity-group", identityTopic, searchStore, kafkaDialer,
	)
	go identityConsumer.Start(consumerCtx)
	slog.Info("started kafka consumer", "topic", identityTopic, "group", "search-service-identity-group")

	// 6. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("search-service")

	// 7. Health checker
	checker := health.New("search-service")
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 8. HTTP Handlers
	profileServiceURL := env("PROFILE_SERVICE_URL", "http://identity-profile:8098")
	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
	handler := http.New(searchStore, rdb).WithReindexSource(profileServiceURL)
	if internalKey != "" {
		handler.WithInternalKey(internalKey)
		slog.Info("search-service: internal-service-key gate enabled")
	} else {
		slog.Warn("search-service: INTERNAL_SERVICE_KEY not set — endpoints, including the admin reindex, are unauthenticated. Do not run this in production.")
	}

	// Startup auto-heal: if users_v1 is empty (wiped index / fresh
	// OpenSearch volume / events lost beyond Kafka retention), rebuild
	// it from profile-service rather than waiting for users to
	// re-register. A populated index is left alone.
	reindex.AutoHealUsersOnStartup(ctx, nil, profileServiceURL, internalKey, searchStore, slog.Default())

	// 9. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)

	// 10. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			consumerCancel()
			rdb.Close()
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
