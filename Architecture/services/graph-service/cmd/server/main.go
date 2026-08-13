package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/graph-service/database"
	"github.com/atpost/graph-service/internal/events"
	graphHttp "github.com/atpost/graph-service/internal/http"
	"github.com/atpost/graph-service/internal/reconcile"
	"github.com/atpost/graph-service/internal/service"
	"github.com/atpost/graph-service/internal/store"
	"github.com/atpost/graph-service/internal/userclient"
	"github.com/atpost/shared/counters"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "graph-service"})

	// 2. Config
	port := env("HTTP_PORT", "8083")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := env("KAFKA_BROKERS", "redpanda:9092")
	userServiceURL := env("USER_SERVICE_URL", "http://identity-user:8110")
	appUserURL := env("APP_USER_SERVICE_URL", "http://user-service:8082")
	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")

	// 3. Database
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

	if err := store.BootstrapSchema(ctx, dbPool, database.SetupSQL, database.Migrations); err != nil {
		slog.Error("failed to bootstrap graph schema", "error", err)
		os.Exit(1)
	}
	slog.Info("graph schema ready")

	// 4. Redis
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

	// 5. Kafka Producer
	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}
	producer := events.NewProducerWithDialer(strings.Split(kafkaBrokers, ","), "social.events.v1", kafkaDialer)
	defer producer.Close()
	slog.Info("kafka producer ready")

	// 6. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("graph-service")
	dbMetrics := metrics.NewDBPoolMetrics("graph-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 7. Health checker
	checker := health.New("graph-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 8. Dependencies
	graphStore := store.New(dbPool)
	graphSvc := service.New(graphStore, rdb, producer)
	// Wire the permission resolver's privacy-settings source (spec §9.8).
	graphSvc.WithPermissionSource(userServiceURL, internalKey)
	// Wire read-through repair of the app.users projection for close-friends.
	graphSvc.WithUserEnsurer(userclient.New(appUserURL, internalKey))
	graphHandler := graphHttp.New(graphSvc)

	// Module 3 SR-2 — the outbox relay.
	//
	// Block/unblock/follow events are written to graph_outbox_events in the
	// SAME transaction as the relationship mutation, so they can never be
	// lost. That guarantee is only worth anything if something delivers them:
	// without this goroutine the table simply accumulates safety events that
	// chat, feed, search and notifications never receive, which is
	// indistinguishable from the fire-and-forget goroutine it replaced.
	//
	// Multiple replicas may run this concurrently — rows are leased with
	// FOR UPDATE SKIP LOCKED, so each row is delivered by exactly one replica.
	go graphStore.RunRelay(ctx, events.NewOutboxPublisher(producer),
		store.DefaultRelayConfig(), func(format string, args ...any) {
			slog.Warn(fmt.Sprintf(format, args...))
		})

	// Module 3 SR-3 — import blocks from the retired shadow graph.
	//
	// profile-service kept its own `profile.blocks`, which nothing enforced.
	// Every row there is a block a real user asked for and never received.
	// Retiring the routes stops new divergence; this closes the gap for the
	// people already unprotected, under an ANY-BLOCK-WINS rule that never
	// treats an absence as an unblock.
	//
	// It runs periodically rather than once because a not-yet-updated client
	// can still be writing to the legacy table, and those rows are still
	// someone asking for protection.
	if legacyDSN := os.Getenv("LEGACY_PROFILE_BLOCKS_DSN"); legacyDSN != "" {
		legacyPool, lerr := pgxpool.New(ctx, legacyDSN)
		if lerr != nil {
			slog.Error("legacy block reconciler: cannot connect", "error", lerr)
			os.Exit(1)
		}
		defer legacyPool.Close()
		go runLegacyBlockReconciler(ctx, graphStore, store.New(legacyPool))
		slog.Info("legacy block reconciler started")
	} else {
		slog.Warn("LEGACY_PROFILE_BLOCKS_DSN not set — blocks recorded in the retired " +
			"profile.blocks table will NOT be imported. Users who blocked someone through " +
			"the old profile route remain unprotected in feed, search and chat.")
	}

	// Expire stale connection requests hourly (spec §8.3).
	go reconcile.NewConnectionRequestSweeper(graphStore).Start(ctx)

	// Reconcile drift in denormalized follower/following counts every
	// hour. The CountReconciler was previously defined but never
	// started — without it, any missed counter event silently leaves
	// users' follower_count off-by-N forever.
	go reconcile.NewCountReconciler(dbPool).Start(ctx)

	// Sharded-counter flush workers: drain Redis follower/following
	// deltas every 10s and materialise the sum into counts.<col>.
	// Removes per-follow contention on the singleton counts row — a
	// 10M-follower celebrity used to serialise every join on this one
	// row. The hourly CountReconciler above stays as the drift safety
	// net.
	if mc := graphSvc.FollowerCounter(); mc != nil {
		flush := func(ctx context.Context, userIDStr string, total int64) error {
			id, err := uuid.Parse(userIDStr)
			if err != nil {
				return err
			}
			return graphStore.SetCountColumn(ctx, id, "follower_count", total)
		}
		go counters.NewWorker(mc, flush, counters.WorkerOptions{}).Start(ctx)
		slog.Info("graph-service follower-count sharded flush worker started")
	}
	if mc := graphSvc.FollowingCounter(); mc != nil {
		flush := func(ctx context.Context, userIDStr string, total int64) error {
			id, err := uuid.Parse(userIDStr)
			if err != nil {
				return err
			}
			return graphStore.SetCountColumn(ctx, id, "following_count", total)
		}
		go counters.NewWorker(mc, flush, counters.WorkerOptions{}).Start(ctx)
		slog.Info("graph-service following-count sharded flush worker started")
	}

	// Audit CG2: gate every /v1/graph route behind the shared internal
	// service key. The handler already supports the middleware, but
	// previously main.go never wired the env var so the gate was a
	// no-op and every endpoint was open. Empty key keeps the dev
	// loop unblocked but emits a loud startup warning.
	// SR-3: attribute mutating graph writes to a named caller. Strict in
	// production; the permissive mode exists only to roll this out without an
	// instant outage if a legitimate caller was missed, and it leaves the
	// duplicate-writer hole open, so it warns.
	strictWriteSource := env("GRAPH_WRITE_SOURCE_STRICT", "true") == "true"
	if !strictWriteSource {
		slog.Warn("graph-service: GRAPH_WRITE_SOURCE_STRICT=false — unattributed graph writes are PERMITTED. " +
			"A second service can create follows and blocks without review. Do not run this configuration in production.")
	}
	graphHandler.WithCanonicalWriteSource(strictWriteSource, func(format string, args ...any) {
		slog.Warn(fmt.Sprintf(format, args...))
	})

	if internalKey != "" {
		graphHandler.WithInternalKey(internalKey)
		slog.Info("graph-service: internal-service-key gate enabled")
	} else {
		slog.Warn("graph-service: INTERNAL_SERVICE_KEY not set — every /v1/graph endpoint is unauthenticated. Do not run this configuration in production.")
	}

	// 9. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	graphHandler.RegisterRoutes(r)

	// 10. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			producer.Close()
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

// runLegacyBlockReconciler imports blocks from the retired profile.blocks
// table into the canonical graph, forever.
//
// SR-3. It reads through a store built on the LEGACY database pool and writes
// through the canonical one, so the two never share a transaction — the legacy
// database may be a different cluster entirely.
func runLegacyBlockReconciler(ctx context.Context, canonical, legacy *store.Store) {
	const interval = 15 * time.Minute
	for {
		res, err := canonical.ReconcileLegacyBlocks(ctx, store.NewPgLegacyBlockSource(legacy), 500)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			slog.Error("legacy block reconciler pass failed", "error", err,
				"scanned", res.Scanned, "imported", res.Imported)
		case res.Imported > 0:
			// Not an Info: every import here is a user who was unprotected
			// until this moment, and the count should be visible.
			slog.Warn("imported blocks from the retired shadow graph",
				"imported", res.Imported, "scanned", res.Scanned)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
