package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/atpost/shared/transport"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/user-service/database"
	"github.com/atpost/user-service/internal/events"
	"github.com/atpost/user-service/internal/http"
	"github.com/atpost/user-service/internal/identityclient"
	"github.com/atpost/user-service/internal/presence"
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

	// Auto-migrate Phase 6 tables
	ensurePhase6Schema(ctx, dbPool)

	// Migration 006 — business_pages followers, media, website columns
	for _, stmt := range []string{
		`ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS follower_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS cover_media_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS avatar_media_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS website TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS page_followers (
			page_id UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
			user_id UUID NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (page_id, user_id))`,
		`CREATE INDEX IF NOT EXISTS idx_page_followers_user ON page_followers (user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_business_pages_category ON business_pages (category)`,
	} {
		if _, err := dbPool.Exec(ctx, stmt); err != nil {
			slog.Warn("migration 006", "error", err)
		}
	}
	slog.Info("migration 006 applied")

	// Migration 007 — seller_id + status on business_pages
	for _, stmt := range []string{
		`ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('draft','active','suspended'))`,
		`ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS seller_id UUID`,
		`CREATE INDEX IF NOT EXISTS idx_business_pages_seller ON business_pages(seller_id) WHERE seller_id IS NOT NULL`,
	} {
		if _, err := dbPool.Exec(ctx, stmt); err != nil {
			slog.Warn("migration 007", "error", err)
		}
	}
	slog.Info("migration 007 applied")

	// Migration 005 — profile extras (pins, portfolio, QR, wellbeing, screen time)
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS profile_pins (
			user_id      UUID NOT NULL,
			content_type TEXT NOT NULL CHECK (content_type IN ('post','reel','video','product')),
			content_id   UUID NOT NULL,
			pin_order    INT NOT NULL DEFAULT 0,
			pinned_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (user_id, content_type, content_id))`,
		`CREATE TABLE IF NOT EXISTS portfolio_items (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id     UUID NOT NULL,
			title       TEXT NOT NULL,
			description TEXT,
			type        TEXT NOT NULL CHECK (type IN ('project','article','video','design','achievement')),
			url         TEXT,
			media_id    UUID,
			sort_order  INT NOT NULL DEFAULT 0,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE INDEX IF NOT EXISTS idx_portfolio_user ON portfolio_items(user_id, sort_order)`,
		`CREATE TABLE IF NOT EXISTS profile_qr_codes (
			user_id    UUID PRIMARY KEY,
			qr_url     TEXT NOT NULL,
			short_link TEXT NOT NULL,
			scan_count BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE TABLE IF NOT EXISTS digital_wellbeing (
			user_id             UUID PRIMARY KEY,
			daily_limit_mins    INT,
			focus_mode_enabled  BOOLEAN NOT NULL DEFAULT FALSE,
			focus_mode_until    TIMESTAMPTZ,
			bedtime_start       TIME,
			bedtime_end         TIME,
			nudge_interval_mins INT NOT NULL DEFAULT 30,
			hide_like_counts    BOOLEAN NOT NULL DEFAULT FALSE,
			detox_mode_until    TIMESTAMPTZ,
			updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE TABLE IF NOT EXISTS screen_time_log (
			id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id       UUID NOT NULL,
			date          DATE NOT NULL,
			minutes       INT NOT NULL DEFAULT 0,
			session_count INT NOT NULL DEFAULT 0,
			UNIQUE (user_id, date))`,
		`CREATE INDEX IF NOT EXISTS idx_screen_time_user ON screen_time_log(user_id, date DESC)`,
	} {
		if _, err := dbPool.Exec(ctx, stmt); err != nil {
			slog.Warn("migration 005", "error", err)
		}
	}
	slog.Info("migration 005 applied")

	// Migration 008 — projection reconcile checkpoint (durable cursor for the
	// app.users ← profile.profiles reconcile job).
	if _, err := dbPool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS projection_sync_checkpoint (
			projection_name TEXT PRIMARY KEY,
			last_synced_at  TIMESTAMPTZ NOT NULL,
			last_success_at TIMESTAMPTZ,
			last_error      TEXT,
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		slog.Warn("migration 008", "error", err)
	}
	slog.Info("migration 008 applied")

	// Migration 009 — consumer dead-letter queue (failed Kafka events parked
	// for inspection + replay instead of being dropped with a log line).
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS consumer_dlq (
			id          BIGSERIAL PRIMARY KEY,
			topic       TEXT NOT NULL,
			event_type  TEXT NOT NULL DEFAULT '',
			payload     TEXT NOT NULL,
			error       TEXT NOT NULL,
			failed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
			replayed_at TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_consumer_dlq_unreplayed ON consumer_dlq(id) WHERE replayed_at IS NULL`,
	} {
		if _, err := dbPool.Exec(ctx, stmt); err != nil {
			slog.Warn("migration 009", "error", err)
		}
	}
	slog.Info("migration 009 applied")

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
	presenceStore := presence.New(rdb)
	userHandler := http.New(userSvc, presenceStore, userStore).WithInternalRoutes(internalKey)
	if internalKey == "" {
		slog.Warn("user-service: INTERNAL_SERVICE_KEY not set — /internal/* routes are unauthenticated")
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

// ensurePhase6Schema creates tables for channels, business pages, reputation, and endorsements.
func ensurePhase6Schema(ctx context.Context, db *pgxpool.Pool) {
	ddl := []string{
		// Alter users table with status/mood and pronouns
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS pronouns TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS status_text TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS status_emoji TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS status_expires_at TIMESTAMPTZ`,

		// Add click_count to user_links
		`ALTER TABLE user_links ADD COLUMN IF NOT EXISTS click_count INTEGER NOT NULL DEFAULT 0`,

		// Channels
		`CREATE TABLE IF NOT EXISTS channels (
			id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id          UUID NOT NULL REFERENCES users(id),
			handle           TEXT NOT NULL UNIQUE,
			name             TEXT NOT NULL,
			description      TEXT NOT NULL DEFAULT '',
			icon_url         TEXT NOT NULL DEFAULT '',
			banner_url       TEXT NOT NULL DEFAULT '',
			category         TEXT NOT NULL DEFAULT '',
			country          TEXT NOT NULL DEFAULT '',
			language         TEXT NOT NULL DEFAULT '',
			contact_email    TEXT NOT NULL DEFAULT '',
			collab_status    TEXT NOT NULL DEFAULT 'closed',
			content_schedule TEXT NOT NULL DEFAULT '',
			subscriber_count INTEGER NOT NULL DEFAULT 0,
			is_verified      BOOLEAN NOT NULL DEFAULT FALSE,
			created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_channels_user ON channels (user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_channels_handle ON channels (handle)`,

		// Channel links
		`CREATE TABLE IF NOT EXISTS channel_links (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
			title      TEXT NOT NULL,
			url        TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_channel_links_channel ON channel_links (channel_id)`,

		// Channel milestones
		`CREATE TABLE IF NOT EXISTS channel_milestones (
			id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			channel_id     UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
			milestone_type TEXT NOT NULL,
			title          TEXT NOT NULL,
			achieved_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
			is_public      BOOLEAN NOT NULL DEFAULT TRUE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_channel_milestones_channel ON channel_milestones (channel_id)`,

		// Business pages
		`CREATE TABLE IF NOT EXISTS business_pages (
			id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id        UUID NOT NULL REFERENCES users(id),
			page_handle    TEXT NOT NULL UNIQUE,
			page_name      TEXT NOT NULL,
			category       TEXT NOT NULL,
			description    TEXT NOT NULL DEFAULT '',
			address        TEXT NOT NULL DEFAULT '',
			lat            DOUBLE PRECISION,
			lng            DOUBLE PRECISION,
			business_hours JSONB,
			phone          TEXT NOT NULL DEFAULT '',
			whatsapp       TEXT NOT NULL DEFAULT '',
			business_email TEXT NOT NULL DEFAULT '',
			services       JSONB,
			price_range    TEXT NOT NULL DEFAULT '',
			booking_url    TEXT NOT NULL DEFAULT '',
			menu_urls      JSONB,
			is_verified    BOOLEAN NOT NULL DEFAULT FALSE,
			avg_rating     DOUBLE PRECISION NOT NULL DEFAULT 0,
			review_count   INTEGER NOT NULL DEFAULT 0,
			faq            JSONB,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_business_pages_user ON business_pages (user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_business_pages_handle ON business_pages (page_handle)`,

		// Business reviews
		`CREATE TABLE IF NOT EXISTS business_reviews (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			page_id     UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
			reviewer_id UUID NOT NULL,
			rating      INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
			review_text TEXT NOT NULL DEFAULT '',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(page_id, reviewer_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_business_reviews_page ON business_reviews (page_id, created_at DESC)`,

		// User reputation
		`CREATE TABLE IF NOT EXISTS user_reputation (
			user_id              UUID PRIMARY KEY REFERENCES users(id),
			trust_score          DECIMAL(3,2) NOT NULL DEFAULT 0.50,
			endorsement_count    INTEGER NOT NULL DEFAULT 0,
			cross_platform_proofs JSONB NOT NULL DEFAULT '{}'
		)`,

		// Endorsements
		`CREATE TABLE IF NOT EXISTS endorsements (
			id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			from_user_id UUID NOT NULL REFERENCES users(id),
			to_user_id   UUID NOT NULL REFERENCES users(id),
			skill_tag    TEXT NOT NULL,
			message      TEXT NOT NULL DEFAULT '',
			created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(from_user_id, to_user_id, skill_tag)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_endorsements_to ON endorsements (to_user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_endorsements_from ON endorsements (from_user_id)`,

		// --- Ensure-Publisher: handle + channel auto-creation ---

		// Account-level handle on users table
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS handle TEXT UNIQUE`,

		// is_default flag on channels
		`ALTER TABLE channels ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT FALSE`,

		// Global handles registry (source of truth for uniqueness across accounts, channels, pages)
		`CREATE TABLE IF NOT EXISTS handles (
			handle     TEXT PRIMARY KEY,
			owner_type TEXT NOT NULL,
			owner_id   UUID NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_handles_owner ON handles (owner_id, owner_type)`,

		// Handle history for redirects after renames
		`CREATE TABLE IF NOT EXISTS handle_history (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			handle     TEXT NOT NULL,
			owner_type TEXT NOT NULL,
			owner_id   UUID NOT NULL,
			changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_handle_history_handle ON handle_history (handle)`,

		// Channel members (multi-role support)
		`CREATE TABLE IF NOT EXISTS channel_members (
			channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
			user_id    UUID NOT NULL,
			role       TEXT NOT NULL DEFAULT 'owner',
			joined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (channel_id, user_id)
		)`,
	}

	for _, stmt := range ddl {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("phase6 schema migration", "error", err)
		}
	}
	slog.Info("phase 6 schema ensured", "tables", "channels, business pages, reputation, endorsements, status")
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
