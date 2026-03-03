package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/engagement/consumers"
	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/post-service/internal/http"
	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "post-service"})

	// 2. Config
	port := env("HTTP_PORT", "8084")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	scyllaHosts := env("SCYLLA_HOSTS", "localhost")
	kafkaBrokers := env("KAFKA_BROKERS", "kafka:9092")

	// 3. Database (Postgres)
	ctx := context.Background()
	dbPool, err := pgxpool.New(ctx, pgDSN)
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

	// Auto-migrate engagement tables (idempotent -- uses IF NOT EXISTS)
	ensureSchema(ctx, dbPool)

	// 4. Database (ScyllaDB)
	cluster := gocql.NewCluster(scyllaHosts)
	cluster.Keyspace = "social_engagement"
	cluster.Consistency = gocql.Quorum
	scyllaSession, err := cluster.CreateSession()
	if err != nil {
		slog.Error("failed to connect to scylladb", "error", err)
		os.Exit(1)
	}
	defer scyllaSession.Close()
	slog.Info("connected to scylladb")

	// 5. Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping failed", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()
	slog.Info("connected to redis")

	// 6. Dependencies
	pgStore := postgres.New(dbPool)
	scyllaInteractionStore := scylla.New(scyllaSession)
	postSvc := service.New(pgStore, scyllaInteractionStore, rdb)

	// 7. Kafka producers
	brokers := strings.Split(kafkaBrokers, ",")
	legacyProducer := postEvents.NewProducer(brokers, "social.events.v1")
	defer legacyProducer.Close()
	postSvc.SetProducer(legacyProducer)

	engProducer := engagement.NewProducer(brokers, "social.events.v1")
	defer engProducer.Close()
	postSvc.SetEngagementProducer(engProducer)
	postSvc.SetScyllaSession(scyllaSession)
	slog.Info("kafka producers initialized")

	// 8. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("post-service")
	dbMetrics := metrics.NewDBPoolMetrics("post-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 9. Health checker
	checker := health.New("post-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))
	checker.Register("scylladb", health.ScyllaCheck(func(ctx context.Context) error {
		return scyllaSession.Query("SELECT now() FROM system.local").Exec()
	}))

	// 10. Start engagement consumers (async Kafka -> ScyllaDB / PG / WS)
	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer consumerCancel()
	engTopic := "social.events.v1"

	scyllaConsumer := consumers.NewScyllaLikeConsumer(scyllaSession, rdb)
	go scyllaConsumer.Start(consumerCtx, brokers, engTopic)

	pgCounterConsumer := consumers.NewPGCounterConsumer(dbPool, rdb)
	go pgCounterConsumer.Start(consumerCtx, brokers, engTopic)

	wsBroadcaster := consumers.NewWSBroadcasterConsumer(rdb)
	go wsBroadcaster.Start(consumerCtx, brokers, engTopic)

	slog.Info("engagement consumers started")

	// 11. Reconciliation worker (every 5 min)
	reconciler := engagement.NewReconciler(rdb, scyllaSession, dbPool)
	go reconciler.Start(consumerCtx, 5*time.Minute)

	// Story expiry cleanup (every 5 min)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-consumerCtx.Done():
				return
			case <-ticker.C:
				deleted, err := postSvc.CleanupExpiredStories(consumerCtx)
				if err != nil {
					slog.Error("story cleanup error", "error", err)
				} else if deleted > 0 {
					slog.Info("cleaned up expired stories", "count", deleted)
				}
			}
		}
	}()

	// Event log cleanup (every hour)
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-consumerCtx.Done():
				return
			case <-ticker.C:
				engagement.CleanupEventLog(consumerCtx, dbPool, 48*time.Hour)
			}
		}
	}()
	slog.Info("reconciler and cleanup workers started")

	// 12. HTTP Server
	postHandler := http.New(postSvc, rdb)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	postHandler.RegisterRoutes(r)

	// 13. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			consumerCancel()
			legacyProducer.Close()
			engProducer.Close()
			rdb.Close()
			scyllaSession.Close()
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

// ensureSchema creates engagement-related tables if they don't exist.
// Idempotent -- safe to run on every startup.
func ensureSchema(ctx context.Context, db *pgxpool.Pool) {
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS comments (
			id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			post_id        UUID NOT NULL REFERENCES posts(id),
			author_id      UUID NOT NULL,
			parent_id      UUID REFERENCES comments(id),
			body           TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
			like_count     INTEGER NOT NULL DEFAULT 0,
			dislike_count  INTEGER NOT NULL DEFAULT 0,
			reply_count    INTEGER NOT NULL DEFAULT 0,
			is_reply       BOOLEAN NOT NULL DEFAULT FALSE,
			is_deleted     BOOLEAN NOT NULL DEFAULT FALSE,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_comments_post ON comments (post_id, created_at DESC) WHERE is_deleted = FALSE`,
		`CREATE INDEX IF NOT EXISTS idx_comments_parent ON comments (parent_id, created_at ASC) WHERE parent_id IS NOT NULL AND is_deleted = FALSE`,
		`CREATE INDEX IF NOT EXISTS idx_comments_author ON comments (author_id, created_at DESC) WHERE is_deleted = FALSE`,
		`CREATE TABLE IF NOT EXISTS post_engagement_counts (
			post_id         UUID PRIMARY KEY REFERENCES posts(id),
			like_count      INTEGER NOT NULL DEFAULT 0,
			comment_count   INTEGER NOT NULL DEFAULT 0,
			share_count     INTEGER NOT NULL DEFAULT 0,
			bookmark_count  INTEGER NOT NULL DEFAULT 0,
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS engagement_event_log (
			event_id      TEXT PRIMARY KEY,
			event_type    TEXT NOT NULL,
			target_id     UUID NOT NULL,
			processed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_event_log_age ON engagement_event_log (processed_at)`,
	}

	for _, stmt := range ddl {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("schema migration", "error", err)
		}
	}

	// Create trigger for auto-creating engagement counts (ignore error if already exists)
	db.Exec(ctx, `
		CREATE OR REPLACE FUNCTION create_engagement_counts()
		RETURNS TRIGGER AS $$
		BEGIN
			INSERT INTO post_engagement_counts (post_id) VALUES (NEW.id) ON CONFLICT DO NOTHING;
			RETURN NEW;
		END; $$ LANGUAGE plpgsql`)

	db.Exec(ctx, `DROP TRIGGER IF EXISTS trg_create_engagement_counts ON posts`)
	db.Exec(ctx, `
		CREATE TRIGGER trg_create_engagement_counts
			AFTER INSERT ON posts
			FOR EACH ROW EXECUTE FUNCTION create_engagement_counts()`)

	// Backfill engagement counts for existing posts that don't have a row yet
	db.Exec(ctx, `
		INSERT INTO post_engagement_counts (post_id)
		SELECT id FROM posts WHERE id NOT IN (SELECT post_id FROM post_engagement_counts)
		ON CONFLICT DO NOTHING`)

	// Stories table
	storyDDL := []string{
		`CREATE TABLE IF NOT EXISTS stories (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			author_id       UUID NOT NULL,
			media_url       TEXT NOT NULL,
			media_type      TEXT NOT NULL CHECK (media_type IN ('image', 'video')),
			caption         TEXT NOT NULL DEFAULT '',
			visibility      TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'followers', 'close_friends')),
			view_count      INTEGER NOT NULL DEFAULT 0,
			expires_at      TIMESTAMPTZ NOT NULL,
			is_highlight    BOOLEAN NOT NULL DEFAULT FALSE,
			highlight_group TEXT,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_stories_author ON stories (author_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_stories_expires ON stories (expires_at) WHERE is_highlight = FALSE`,
	}
	for _, stmt := range storyDDL {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("stories schema", "error", err)
		}
	}

	// Reactions table (multi-reaction: like, love, haha, wow, sad, angry)
	reactionDDL := []string{
		`CREATE TABLE IF NOT EXISTS reactions (
			id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			target_type   TEXT NOT NULL,
			target_id     UUID NOT NULL,
			user_id       UUID NOT NULL,
			reaction_type TEXT NOT NULL CHECK (reaction_type IN ('like', 'love', 'haha', 'wow', 'sad', 'angry')),
			created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(target_type, target_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reactions_target ON reactions (target_type, target_id)`,
		`CREATE INDEX IF NOT EXISTS idx_reactions_user ON reactions (user_id, target_type, target_id)`,
	}
	for _, stmt := range reactionDDL {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("reactions schema", "error", err)
		}
	}

	// Saved items table
	savedDDL := []string{
		`CREATE TABLE IF NOT EXISTS saved_items (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id         UUID NOT NULL,
			target_type     TEXT NOT NULL,
			target_id       UUID NOT NULL,
			collection_name TEXT NOT NULL DEFAULT 'All Saved',
			created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(user_id, target_type, target_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_saved_items_user ON saved_items (user_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_saved_items_collection ON saved_items (user_id, collection_name, created_at DESC)`,
	}
	for _, stmt := range savedDDL {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("saved_items schema", "error", err)
		}
	}

	// Alter posts table to add new columns (safe -- ADD COLUMN IF NOT EXISTS)
	alterPostsDDL := []string{
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS hashtags TEXT[] DEFAULT '{}'`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS mentions UUID[] DEFAULT '{}'`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS location_name TEXT`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS location_lat DOUBLE PRECISION`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS location_lng DOUBLE PRECISION`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS post_type TEXT NOT NULL DEFAULT 'text'`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS app_origin TEXT NOT NULL DEFAULT 'postbook'`,
		`CREATE INDEX IF NOT EXISTS idx_posts_hashtags ON posts USING GIN (hashtags)`,
	}
	for _, stmt := range alterPostsDDL {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("alter posts schema", "error", err)
		}
	}

	slog.Info("engagement schema ensured")
}
