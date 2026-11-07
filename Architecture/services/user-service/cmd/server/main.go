package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/atpost/user-service/database"
	"github.com/atpost/user-service/internal/events"
	"github.com/atpost/user-service/internal/http"
	"github.com/atpost/user-service/internal/identityclient"
	"github.com/atpost/user-service/internal/mediaclient"
	"github.com/atpost/user-service/internal/presence"
	"github.com/atpost/user-service/internal/purge"
	"github.com/atpost/user-service/internal/reconcile"
	"github.com/atpost/user-service/internal/service"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "user-service"})

	// 2. Config
	port := env("HTTP_PORT", "8082")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := env("KAFKA_BROKERS", "localhost:9092")
	profileURL := env("PROFILE_SERVICE_URL", "http://identity-profile:8098")
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
		slog.Error("failed to bootstrap user schema", "error", err)
		os.Exit(1)
	}
	slog.Info("user schema ready")

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

	// 5. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("user-service")
	dbMetrics := metrics.NewDBPoolMetrics("user-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 6. Health checker
	checker := health.New("user-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 7. Dependencies
	userStore := store.New(dbPool)
	identityClient := identityclient.New(profileURL, internalKey)
	userSvc := service.New(userStore, rdb).WithIdentityClient(identityClient)
	// Media attached to channels, portfolio items and business pages is
	// checked (the actor's own finished upload) and claimed in media-service
	// so its owner cannot delete it from under the row (service/media_claims.go).
	// media-service must already register claimant "user-service".
	if mc := mediaclient.New(env("MEDIA_SERVICE_URL", "http://media-service:8087"), internalKey); mc != nil {
		userSvc.WithMedia(mc)
	} else {
		slog.Warn("user-service: MEDIA_SERVICE_URL or INTERNAL_SERVICE_KEY not set — writes that attach media " +
			"(channel picture/banner, portfolio media, page avatar/cover) are refused with 503 MEDIA_UNAVAILABLE")
	}
	presenceStore := presence.New(rdb)
	userHandler := http.New(userSvc, presenceStore, userStore).WithInternalRoutes(internalKey)
	if err := http.CheckInternalKeyConfig(internalKey, os.Getenv); err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}
	if internalKey == "" {
		slog.Warn("user-service: INTERNAL_SERVICE_KEY not set — /internal/* routes answer 503 INTERNAL_KEY_NOT_CONFIGURED " +
			"(projection repair, DLQ replay and subscriber fan-out are unavailable)")
	}
	// Private profile fields (first/last name, dob, gender) on
	// GET /v1/users/:userId and /by-username go to the owner, or to a sibling
	// service holding a token for users:profile.read_private. The internal key
	// is not enough: the gateway injects it on anonymous requests too.
	serviceCallers, err := http.ServiceCallersFromEnv(os.Getenv)
	if err != nil {
		slog.Error("refusing to start: invalid SERVICE_CALLERS", "error", err)
		os.Exit(1)
	}
	if serviceCallers != nil {
		userHandler.WithServiceAuth(serviceCallers)
		slog.Info("user-service: service-token callers registered for private profile reads", "callers", serviceCallers.Callers())
	}
	// Admin console: /v1/users/internal/admin/* admits only admin-service
	// tokens for audience "social", from the same SERVICE_CALLERS registry.
	adminCallers, err := http.AdminServiceCallersFromEnv(os.Getenv)
	if err != nil {
		slog.Error("refusing to start: invalid SERVICE_CALLERS for the admin family", "error", err)
		os.Exit(1)
	}
	userHandler.WithAdminServiceAuth(adminCallers)
	if adminCallers == nil {
		slog.Warn("user-service: no SERVICE_CALLERS — /v1/users/internal/admin/* answers 401 (legacy PAGES_ADMIN_USER_IDS routes unaffected)")
	}

	// 8. Kafka Consumer
	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

	consumer := events.NewConsumerWithDialer([]string{kafkaBrokers}, "social.events.v1", userSvc, userStore, kafkaDialer)
	userHandler.WithDLQConsumer(consumer)
	go consumer.Start(ctx)

	// Account control (auth-service 30-day deletion): hide the projection
	// row on user.deactivated / user.deletion_scheduled, unhide on the
	// reverse, and on user.purge_requested erase every app-side profile row
	// and ack as "user-extras" onto platform.purge-acks.v1 (the identity
	// user-service acks as "user").
	purgeAcks := purge.NewKafkaAckPublisher(strings.Split(kafkaBrokers, ","),
		env("PURGE_ACKS_TOPIC", purge.DefaultAcksTopic), kafkaDialer)
	defer purgeAcks.Close()
	lifecycle := purge.NewConsumer(strings.Split(kafkaBrokers, ","),
		env("IDENTITY_KAFKA_TOPIC", "identity.events.v1"), "user-service-account-lifecycle", kafkaDialer,
		purge.NewHandler("user-extras", userStore, purgeAcks, userSvc, slog.Default()), slog.Default())
	defer lifecycle.Close()
	go lifecycle.Start(ctx)

	// Projection reconcile — keeps app.users converged with the identity
	// profile-service, repairing any row lost to a missed UserRegistered
	// event. Runs a full pass on startup, then incremental every 5m.
	go reconcile.New(userStore, identityClient).Start(ctx)

	// Projection health metrics — exported at /metrics for Prometheus.
	go collectProjectionStats(ctx, userSvc)

	// Status expiry cleanup (every 5 min)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleared, err := userSvc.ClearExpiredStatuses(ctx)
				if err != nil {
					slog.Error("status cleanup error", "error", err)
				} else if cleared > 0 {
					slog.Info("cleared expired statuses", "count", cleared)
				}
			}
		}
	}()

	// Media claim drain: settle holders queued in media_claim_pending
	// (deleted channels and portfolio items, disabled pages, purged accounts,
	// failed releases) so their owners' media is deletable again.
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			if rep, err := userSvc.DrainMediaClaims(ctx); err != nil {
				slog.Error("media claim drain error", "error", err)
			} else if rep.Rows > 0 {
				slog.Info("media claim drain", "report", rep.String())
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	// Page follower-count reconciliation — recompute from active follow rows
	// to catch drift (spec §11). Runs once on startup, then every 6h.
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		run := func() {
			if n, err := userSvc.ReconcileFollowerCounts(ctx); err != nil {
				slog.Error("page follower reconcile error", "error", err)
			} else if n > 0 {
				slog.Info("page follower counts reconciled", "pages_updated", n)
			}
		}
		run()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()

	// 9. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))
	// Belt-and-suspenders runtime canary for the relationship-separation
	// contract. Logs (but does not mutate) responses where a follow field
	// leaks onto a "user" payload or a friend field onto a "page" payload.
	// See shared/middleware/entity_response_guard.go.
	r.Use(middleware.EntityResponseGuard())

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	userHandler.RegisterRoutes(r)

	// 10. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
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

// collectProjectionStats exports app.users projection-health gauges to
// Prometheus every minute, so drift, DLQ depth, and reconcile staleness are
// observable and alertable (see prometheus/alerts.yml).
func collectProjectionStats(ctx context.Context, svc *service.Service) {
	users := promauto.NewGauge(prometheus.GaugeOpts{
		Name: "atpost_projection_users_total",
		Help: "Number of rows in the local app.users projection.",
	})
	master := promauto.NewGauge(prometheus.GaugeOpts{
		Name: "atpost_projection_master_total",
		Help: "Number of profiles in the identity master (profile-service).",
	})
	missing := promauto.NewGauge(prometheus.GaugeOpts{
		Name: "atpost_projection_missing_total",
		Help: "Users present in the identity master but missing from app.users.",
	})
	dlq := promauto.NewGauge(prometheus.GaugeOpts{
		Name: "atpost_projection_dlq_unreplayed_total",
		Help: "Unreplayed consumer dead-letter-queue entries.",
	})
	lastOK := promauto.NewGauge(prometheus.GaugeOpts{
		Name: "atpost_projection_reconcile_last_success_timestamp_seconds",
		Help: "Unix time of the last successful projection reconcile run.",
	})

	sample := func() {
		r, err := svc.ProjectionHealth(ctx)
		if err != nil {
			slog.Warn("projection metrics sample failed", "error", err)
			return
		}
		users.Set(float64(r.ProjectionCount))
		master.Set(float64(r.MasterCount))
		missing.Set(float64(r.MissingCount))
		dlq.Set(float64(r.DLQUnreplayed))
		if r.LastSuccessAt != nil {
			lastOK.Set(float64(r.LastSuccessAt.Unix()))
		}
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	sample()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sample()
		}
	}
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
