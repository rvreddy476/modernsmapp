package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/post-service/database"
	mediaConsumers "github.com/atpost/post-service/internal/consumers"
	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/engagement/consumers"
	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/post-service/internal/http"
	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/atpost/post-service/internal/streamhub"
	"github.com/atpost/post-service/internal/trending"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
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

	if err := postgres.BootstrapSchema(ctx, dbPool, database.SetupSQL); err != nil {
		slog.Error("failed to bootstrap post schema", "error", err)
		os.Exit(1)
	}
	slog.Info("post schema ready")

	// Auto-migrate engagement tables (idempotent -- uses IF NOT EXISTS)
	ensureSchema(ctx, dbPool)

	// 4. Database (ScyllaDB)
	cluster := gocql.NewCluster(scyllaHosts)
	cluster.Keyspace = "social_engagement"
	cluster.Consistency = gocql.Quorum
	cluster.NumConns = 10
	cluster.MaxPreparedStmts = 1000
	scyllaSession, err := cluster.CreateSession()
	if err != nil {
		slog.Error("failed to connect to scylladb", "error", err)
		os.Exit(1)
	}
	defer scyllaSession.Close()
	slog.Info("connected to scylladb")

	// 4b. Ensure reel engagement Scylla tables (Gold Spec §5.6)
	if err := scylla.EnsureReelEngagementSchema(scyllaSession); err != nil {
		slog.Warn("reel engagement schema", "error", err)
	}

	// 5. Redis
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

	// 6. Dependencies
	pgStore := postgres.New(dbPool)
	scyllaInteractionStore := scylla.New(scyllaSession)
	postSvc := service.New(pgStore, scyllaInteractionStore, rdb)
	postSvc.SetGraphServiceURL(env("GRAPH_SERVICE_URL", "http://graph-service:8083"))
	postSvc.SetMonetizationServiceURL(env("MONETIZATION_SERVICE_URL", "http://monetization-service:8099"))
	postSvc.SetInternalServiceKey(os.Getenv("INTERNAL_SERVICE_KEY"))

	// 7. Kafka producers
	brokers := strings.Split(kafkaBrokers, ",")
	legacyProducer := postEvents.NewProducerWithDialer(brokers, "social.events.v1", kafkaDialer)
	defer legacyProducer.Close()
	postSvc.SetProducer(legacyProducer)

	engProducer := engagement.NewProducerWithDialer(brokers, "social.events.v1", kafkaDialer)
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
	go scyllaConsumer.Start(consumerCtx, brokers, engTopic, kafkaDialer)

	pgCounterConsumer := consumers.NewPGCounterConsumer(dbPool, rdb)
	go pgCounterConsumer.Start(consumerCtx, brokers, engTopic, kafkaDialer)

	wsBroadcaster := consumers.NewWSBroadcasterConsumer(rdb)
	go wsBroadcaster.Start(consumerCtx, brokers, engTopic, kafkaDialer)

	reelAnalytics := consumers.NewReelAnalyticsConsumer(dbPool, rdb)
	go reelAnalytics.Start(consumerCtx, brokers, engTopic)

	// Single Prometheus metrics handle shared by every Kafka consumer in
	// this service. Two `NewKafkaConsumerMetrics("post-service")` calls
	// trip a `duplicate metrics collector registration` panic — promauto
	// hates two collectors with the same fully-qualified name.
	consumerMetrics := metrics.NewKafkaConsumerMetrics("post-service")

	// Media transcode consumer: listens to media-service's `media.events`
	// topic and updates video_metadata.playback_url with the HLS master URL
	// once transcoding finishes. Without this the watch screen always falls
	// back to the raw MP4 even though HLS variants exist on storage.
	mediaTranscodeConsumer := mediaConsumers.
		NewMediaTranscodeConsumer(pgStore, brokers, rdb, consumerMetrics).
		WithProducer(legacyProducer) // fan PostContentTypeChanged out to feed-service
	go mediaTranscodeConsumer.Start(consumerCtx)

	// Tier 1a phase 2: invalidate the local entitlement cache the
	// moment monetization-service publishes a subscribe/unsubscribe
	// event. Without this, the TTL (60s) is the floor on how fast a
	// new subscription unlocks gated content.
	entitlementConsumer := mediaConsumers.NewEntitlementChangedConsumer(postSvc, brokers, rdb, consumerMetrics)
	go entitlementConsumer.Start(consumerCtx)

	slog.Info("engagement consumers + media + entitlement consumers started")

	// Real-time trending leaderboard. Single goroutine per replica;
	// only the leader-locked instance actually publishes, so adding
	// more replicas doesn't multiply the work. Subscribers attach via
	// the SSE handler in internal/http/trending_stream.go.
	trendingPub := trending.New(rdb, slog.Default())
	trendingPub.Start(consumerCtx)
	slog.Info("trending publisher started")

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
	// Outbox worker — publishes pending events to Kafka (Gold Spec §5.8)
	postSvc.StartOutboxWorker(consumerCtx)
	postSvc.StartCrossPostWorker(consumerCtx)

	slog.Info("reconciler, outbox, and cleanup workers started")

	// 12. HTTP Server
	// Shared SSE fan-out hub: one Redis SUB per channel, in-memory
	// broadcast to all attached HTTP listeners. Without this, the
	// /v1/hashtags/:tag/stream + /v1/hashtags/trending/stream
	// handlers would each open a Redis SUB per connected client —
	// 50k clients = 50k Redis subs per instance. With the hub it's
	// O(distinct channels) instead. See internal/streamhub.
	sseHub := streamhub.New(rdb, slog.Default())

	postHandler := http.New(postSvc, rdb).WithStreamHub(sseHub)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	postHandler.RegisterRoutes(r)
	postHandler.RegisterDraftRoutes(r)
	postHandler.RegisterReelDiscoveryRoutes(r)
	postHandler.RegisterReelEngagementRoutes(r)
	postHandler.RegisterReelFeedRoutes(r)
	postHandler.RegisterReportRoutes(r)
	postHandler.RegisterCrosspostRoutes(r)
	postHandler.RegisterMyUploadsRoutes(r)
	postHandler.RegisterAudioRoutes(r)
	postHandler.RegisterRepostRoutes(r)

	// 12b. Scheduled draft publish worker (every 60 seconds)
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-consumerCtx.Done():
				return
			case <-ticker.C:
				n, err := postSvc.PublishScheduledDrafts(consumerCtx)
				if err != nil {
					slog.Error("scheduled draft publish error", "error", err)
				} else if n > 0 {
					slog.Info("published scheduled drafts", "count", n)
				}
			}
		}
	}()

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
		// Tier 2b: comment moderation queue
		`ALTER TABLE comments ADD COLUMN IF NOT EXISTS moderation_status TEXT NOT NULL DEFAULT 'visible'
			CHECK (moderation_status IN ('visible','hidden','removed','review'))`,
		`ALTER TABLE comments ADD COLUMN IF NOT EXISTS flagged_count INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_comments_moderation_status ON comments (moderation_status, created_at DESC) WHERE moderation_status IN ('hidden','removed','review')`,
		`CREATE INDEX IF NOT EXISTS idx_comments_flagged ON comments (flagged_count DESC, created_at DESC) WHERE flagged_count > 0`,
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

		// Tier 3c: membership gating. NULL tier_required_id = public.
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS tier_required_id UUID`,
		`CREATE INDEX IF NOT EXISTS idx_posts_tier_required ON posts (tier_required_id) WHERE tier_required_id IS NOT NULL`,
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

	// Reel metadata columns on posts (migration 006)
	reelMetaDDL := []string{
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS title TEXT DEFAULT ''`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS tags TEXT[] DEFAULT '{}'`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS category TEXT DEFAULT ''`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS language TEXT DEFAULT 'en'`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS seo_title TEXT DEFAULT ''`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS paid_promotion BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS altered_content BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS is_made_for_kids BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS license TEXT DEFAULT 'standard'`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS allow_embedding BOOLEAN DEFAULT TRUE`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS publish_to_feed BOOLEAN DEFAULT TRUE`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS remix_setting TEXT DEFAULT 'allow'`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS comment_moderation TEXT DEFAULT 'none'`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS comment_access TEXT DEFAULT 'everyone'`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS recording_date DATE`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS recording_location TEXT DEFAULT ''`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS cover_media_id UUID`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS original_audio_volume REAL DEFAULT 1.0`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS overlay_audio_volume REAL DEFAULT 1.0`,
	}
	for _, stmt := range reelMetaDDL {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("reel meta schema", "error", err)
		}
	}

	// Reel drafts table
	reelDraftsDDL := []string{
		`CREATE TABLE IF NOT EXISTS reel_drafts (
			id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			author_id           UUID NOT NULL,
			media_id            UUID,
			title               TEXT NOT NULL DEFAULT '',
			caption             TEXT NOT NULL DEFAULT '',
			hashtags            TEXT[] DEFAULT '{}',
			tags                TEXT[] DEFAULT '{}',
			visibility          TEXT NOT NULL DEFAULT 'public',
			topic_id            INT,
			category            TEXT DEFAULT '',
			language            TEXT DEFAULT 'en',
			seo_title           TEXT DEFAULT '',
			cross_post_postbook BOOLEAN DEFAULT TRUE,
			cross_post_posttube BOOLEAN DEFAULT FALSE,
			publish_to_feed     BOOLEAN DEFAULT TRUE,
			is_made_for_kids    BOOLEAN DEFAULT FALSE,
			paid_promotion      BOOLEAN DEFAULT FALSE,
			altered_content     BOOLEAN DEFAULT FALSE,
			auto_chapters       BOOLEAN DEFAULT TRUE,
			featured_places     BOOLEAN DEFAULT TRUE,
			auto_concepts       BOOLEAN DEFAULT TRUE,
			license             TEXT DEFAULT 'standard',
			allow_embedding     BOOLEAN DEFAULT TRUE,
			remix_setting       TEXT DEFAULT 'allow',
			likes_enabled       BOOLEAN DEFAULT TRUE,
			comments_enabled    BOOLEAN DEFAULT TRUE,
			comment_moderation  TEXT DEFAULT 'basic',
			comment_access      TEXT DEFAULT 'everyone',
			recording_date      DATE,
			recording_location  TEXT DEFAULT '',
			audio_track_id      TEXT,
			audio_start_ms      INT DEFAULT 0,
			original_audio_volume REAL DEFAULT 1.0,
			overlay_audio_volume  REAL DEFAULT 1.0,
			cover_media_id      UUID,
			schedule_at         TIMESTAMPTZ,
			status              TEXT NOT NULL DEFAULT 'draft',
			moderation_status   TEXT DEFAULT 'pending',
			published_post_id   UUID,
			created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reel_drafts_author ON reel_drafts (author_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_reel_drafts_schedule ON reel_drafts (schedule_at)
			WHERE schedule_at IS NOT NULL AND status = 'draft'`,
	}
	for _, stmt := range reelDraftsDDL {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("reel drafts schema", "error", err)
		}
	}

	// Gold Spec tables (migration 007): reel_crosspost, outbox_events, idempotency_keys, etc.
	goldSpecDDL := []string{
		`CREATE TABLE IF NOT EXISTS reel_hashtags (
			reel_id     UUID NOT NULL,
			hashtag     TEXT NOT NULL,
			position    INT NOT NULL DEFAULT 0,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (reel_id, hashtag)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reel_hashtags_tag ON reel_hashtags(hashtag)`,
		`CREATE INDEX IF NOT EXISTS idx_reel_hashtags_tag_recent ON reel_hashtags(hashtag, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS reel_crosspost (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			source_reel_id  UUID NOT NULL,
			target_type     TEXT NOT NULL,
			target_id       TEXT,
			status          TEXT NOT NULL DEFAULT 'pending',
			idempotency_key TEXT,
			error_message   TEXT,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			published_at    TIMESTAMPTZ,
			UNIQUE (source_reel_id, target_type, target_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reel_crosspost_source ON reel_crosspost(source_reel_id)`,
		`CREATE INDEX IF NOT EXISTS idx_reel_crosspost_status ON reel_crosspost(status) WHERE status = 'pending'`,
		`CREATE TABLE IF NOT EXISTS slug_history (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			reel_id     UUID NOT NULL,
			old_slug    TEXT NOT NULL,
			new_slug    TEXT NOT NULL,
			changed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_slug_history_old ON slug_history(old_slug)`,
		`CREATE INDEX IF NOT EXISTS idx_slug_history_reel ON slug_history(reel_id)`,
		`CREATE TABLE IF NOT EXISTS moderation_reviews (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			reel_id         UUID NOT NULL,
			reviewer_type   TEXT NOT NULL,
			reviewer_id     TEXT,
			decision        TEXT NOT NULL,
			reason          TEXT,
			confidence      FLOAT,
			policy_violated TEXT,
			metadata        JSONB,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_moderation_reviews_reel ON moderation_reviews(reel_id)`,
		`CREATE INDEX IF NOT EXISTS idx_moderation_reviews_decision ON moderation_reviews(decision) WHERE decision IN ('flagged', 'pending_review')`,
		`CREATE TABLE IF NOT EXISTS outbox_events (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			event_type      TEXT NOT NULL,
			aggregate_type  TEXT NOT NULL,
			aggregate_id    UUID NOT NULL,
			payload         JSONB NOT NULL,
			published       BOOLEAN NOT NULL DEFAULT FALSE,
			published_at    TIMESTAMPTZ,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox_events(created_at ASC) WHERE published = FALSE`,
		`CREATE INDEX IF NOT EXISTS idx_outbox_aggregate ON outbox_events(aggregate_type, aggregate_id)`,
		`CREATE TABLE IF NOT EXISTS idempotency_keys (
			key             TEXT PRIMARY KEY,
			result_status   INT NOT NULL,
			result_body     JSONB,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at      TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
		)`,
		`CREATE INDEX IF NOT EXISTS idx_idempotency_expiry ON idempotency_keys(expires_at)`,
		// Additional Gold Spec columns on posts and reel_drafts
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS original_reel_id UUID`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS is_branded BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS topic_id UUID`,
		`CREATE INDEX IF NOT EXISTS idx_posts_not_deleted ON posts(created_at DESC) WHERE deleted_at IS NULL`,
		`ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS original_reel_id UUID`,
		`ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS is_branded BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS topic_id UUID`,
	}
	for _, stmt := range goldSpecDDL {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("gold spec schema", "error", err)
		}
	}

	// Content reports table (user-submitted reports → admin dashboard)
	reportsDDL := []string{
		`CREATE TABLE IF NOT EXISTS content_reports (
			id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			reporter_id   UUID NOT NULL,
			target_type   TEXT NOT NULL,
			target_id     UUID NOT NULL,
			reason        TEXT NOT NULL,
			description   TEXT NOT NULL DEFAULT '',
			status        TEXT NOT NULL DEFAULT 'pending',
			reviewer_id   TEXT,
			review_note   TEXT,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			reviewed_at   TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_content_reports_status ON content_reports(status, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_content_reports_target ON content_reports(target_type, target_id)`,
		`CREATE INDEX IF NOT EXISTS idx_content_reports_reporter ON content_reports(reporter_id)`,
	}
	for _, stmt := range reportsDDL {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("content reports schema", "error", err)
		}
	}

	// Extra engagement counters used by the hashtag "top" sort score.
	// views_count needs a future view-tracking writer to be non-zero — adding
	// the column now so the score formula doesn't have to change later.
	// reports_count is maintained by trg_post_reports below, fired on every
	// insert/delete of a `content_reports` row whose target is a post.
	extraCountersDDL := []string{
		`ALTER TABLE post_engagement_counts ADD COLUMN IF NOT EXISTS views_count   BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE post_engagement_counts ADD COLUMN IF NOT EXISTS reports_count INTEGER NOT NULL DEFAULT 0`,
	}
	for _, stmt := range extraCountersDDL {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("extra engagement counters schema", "error", err)
		}
	}

	// Trigger: keep post_engagement_counts.reports_count in sync with
	// content_reports rows. Idempotent — CREATE OR REPLACE / DROP IF EXISTS.
	db.Exec(ctx, `
		CREATE OR REPLACE FUNCTION sync_post_reports_count()
		RETURNS TRIGGER AS $$
		BEGIN
			IF TG_OP = 'INSERT' AND NEW.target_type = 'post' THEN
				INSERT INTO post_engagement_counts (post_id, reports_count)
				VALUES (NEW.target_id, 1)
				ON CONFLICT (post_id) DO UPDATE
				SET reports_count = post_engagement_counts.reports_count + 1,
				    updated_at    = now();
			ELSIF TG_OP = 'DELETE' AND OLD.target_type = 'post' THEN
				UPDATE post_engagement_counts
				SET reports_count = GREATEST(reports_count - 1, 0),
				    updated_at    = now()
				WHERE post_id = OLD.target_id;
			END IF;
			RETURN NULL;
		END; $$ LANGUAGE plpgsql`)
	db.Exec(ctx, `DROP TRIGGER IF EXISTS trg_post_reports ON content_reports`)
	db.Exec(ctx, `
		CREATE TRIGGER trg_post_reports
			AFTER INSERT OR DELETE ON content_reports
			FOR EACH ROW EXECUTE FUNCTION sync_post_reports_count()`)

	// Backfill reports_count once for posts with existing report rows.
	db.Exec(ctx, `
		UPDATE post_engagement_counts pec
		SET reports_count = sub.cnt
		FROM (
			SELECT target_id AS post_id, COUNT(*)::INT AS cnt
			FROM content_reports
			WHERE target_type = 'post'
			GROUP BY target_id
		) sub
		WHERE pec.post_id = sub.post_id AND pec.reports_count <> sub.cnt`)

	// Topics seed data
	db.Exec(ctx, `CREATE TABLE IF NOT EXISTS topics (
		id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name        TEXT NOT NULL UNIQUE,
		slug        TEXT NOT NULL UNIQUE,
		parent_id   UUID REFERENCES topics(id),
		is_active   BOOLEAN NOT NULL DEFAULT TRUE,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	db.Exec(ctx, `INSERT INTO topics (name, slug) VALUES
		('Entertainment', 'entertainment'),
		('Music', 'music'),
		('Sports', 'sports'),
		('Gaming', 'gaming'),
		('News & Politics', 'news-politics'),
		('Education', 'education'),
		('Science & Technology', 'science-technology'),
		('Comedy', 'comedy'),
		('Fashion & Beauty', 'fashion-beauty'),
		('Food & Cooking', 'food-cooking'),
		('Travel', 'travel'),
		('Fitness & Health', 'fitness-health'),
		('Art & Creativity', 'art-creativity'),
		('Pets & Animals', 'pets-animals'),
		('Business & Finance', 'business-finance')
	ON CONFLICT (name) DO NOTHING`)

	// Audio tracks table (migration 013)
	audioTracksDDL := []string{
		`CREATE TABLE IF NOT EXISTS audio_tracks (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			title TEXT NOT NULL,
			artist TEXT NOT NULL DEFAULT '',
			duration_ms INT NOT NULL DEFAULT 0,
			media_id UUID NOT NULL,
			original_post_id UUID,
			genre TEXT NOT NULL DEFAULT '',
			is_original BOOLEAN NOT NULL DEFAULT true,
			use_count INT NOT NULL DEFAULT 0,
			is_trending BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audio_trending ON audio_tracks(is_trending, use_count DESC) WHERE is_trending = true`,
		`CREATE INDEX IF NOT EXISTS idx_audio_post ON audio_tracks(original_post_id)`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS audio_track_id UUID REFERENCES audio_tracks(id)`,
	}
	for _, stmt := range audioTracksDDL {
		if _, err := db.Exec(ctx, stmt); err != nil {
			slog.Warn("audio tracks schema", "error", err)
		}
	}

	slog.Info("engagement schema ensured")
}
