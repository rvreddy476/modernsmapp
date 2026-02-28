package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/facebook-like/post-service/internal/engagement"
	"github.com/facebook-like/post-service/internal/engagement/consumers"
	postEvents "github.com/facebook-like/post-service/internal/events"
	"github.com/facebook-like/post-service/internal/http"
	"github.com/facebook-like/post-service/internal/service"
	"github.com/facebook-like/post-service/internal/store/postgres"
	"github.com/facebook-like/post-service/internal/store/scylla"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Config
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8084"
	}
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	scyllaHosts := os.Getenv("SCYLLA_HOSTS") // e.g., "scylla:9042"
	if scyllaHosts == "" {
		scyllaHosts = "localhost"
	}

	// 2. Database (Postgres)
	ctx := context.Background()
	dbPool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		log.Fatalf("Unable to connect to Postgres: %v\n", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		log.Fatalf("Postgres ping failed: %v\n", err)
	}
	log.Println("Connected to Postgres")

	// Auto-migrate engagement tables (idempotent — uses IF NOT EXISTS)
	ensureSchema(ctx, dbPool)

	// 3. Database (ScyllaDB)
	cluster := gocql.NewCluster(scyllaHosts)
	cluster.Keyspace = "social_engagement"
	cluster.Consistency = gocql.Quorum
	scyllaSession, err := cluster.CreateSession()
	if err != nil {
		log.Fatalf("Unable to connect to ScyllaDB: %v\n", err)
	}
	defer scyllaSession.Close()
	log.Println("Connected to ScyllaDB")

	// 4. Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Redis ping failed: %v", err)
	}
	log.Println("Connected to Redis")

	// 5. Dependencies
	pgStore := postgres.New(dbPool)
	scyllaInteractionStore := scylla.New(scyllaSession)
	postSvc := service.New(pgStore, scyllaInteractionStore, rdb)

	// 6. Kafka producer for engagement events
	kafkaBrokers := os.Getenv("KAFKA_BROKERS")
	if kafkaBrokers == "" {
		kafkaBrokers = "kafka:9092"
	}
	brokers := strings.Split(kafkaBrokers, ",")
	legacyProducer := postEvents.NewProducer(brokers, "social.events.v1")
	defer legacyProducer.Close()
	postSvc.SetProducer(legacyProducer)

	engProducer := engagement.NewProducer(brokers, "social.events.v1")
	defer engProducer.Close()
	postSvc.SetEngagementProducer(engProducer)
	postSvc.SetScyllaSession(scyllaSession)
	log.Println("Kafka producers initialized")

	// 7. Start engagement consumers (async Kafka → ScyllaDB / PG / WS)
	consumerCtx, consumerCancel := context.WithCancel(ctx)
	engTopic := "social.events.v1"

	scyllaConsumer := consumers.NewScyllaLikeConsumer(scyllaSession, rdb)
	go scyllaConsumer.Start(consumerCtx, brokers, engTopic)

	pgCounterConsumer := consumers.NewPGCounterConsumer(dbPool, rdb)
	go pgCounterConsumer.Start(consumerCtx, brokers, engTopic)

	wsBroadcaster := consumers.NewWSBroadcasterConsumer(rdb)
	go wsBroadcaster.Start(consumerCtx, brokers, engTopic)

	log.Println("Engagement consumers started")

	// 8. Reconciliation worker (every 5 min)
	reconciler := engagement.NewReconciler(rdb, scyllaSession, dbPool)
	go reconciler.Start(consumerCtx, 5*time.Minute)

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
	log.Println("Reconciler and cleanup workers started")

	// 9. HTTP Server
	postHandler := http.New(postSvc, rdb)
	r := gin.Default()
	postHandler.RegisterRoutes(r)

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		consumerCancel()
	}()

	log.Printf("Starting post-service on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}

// ensureSchema creates engagement-related tables if they don't exist.
// Idempotent — safe to run on every startup.
func ensureSchema(ctx context.Context, db *pgxpool.Pool) {
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS comments (
			id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			post_id        UUID NOT NULL REFERENCES posts(id),
			author_id      UUID NOT NULL,
			parent_id      UUID REFERENCES comments(id),
			body           TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
			like_count     INTEGER NOT NULL DEFAULT 0,
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
			log.Printf("Warning: schema migration: %v", err)
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

	log.Println("Engagement schema ensured")
}
