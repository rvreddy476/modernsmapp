package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/facebook-like/user-service/internal/events"
	"github.com/facebook-like/user-service/internal/http"
	"github.com/facebook-like/user-service/internal/service"
	"github.com/facebook-like/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Config
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8082"
	}
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := os.Getenv("KAFKA_BROKERS")
	if kafkaBrokers == "" {
		kafkaBrokers = "localhost:9092"
	}

	// 2. Database
	ctx := context.Background()
	dbPool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v\n", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		log.Fatalf("Database ping failed: %v\n", err)
	}
	log.Println("Connected to Postgres")

	// 3. Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Redis ping failed: %v", err)
	}
	log.Println("Connected to Redis")

	// Auto-migrate Phase 6 tables
	ensurePhase6Schema(ctx, dbPool)

	// 4. Dependencies
	userStore := store.New(dbPool)
	userSvc := service.New(userStore, rdb)
	userHandler := http.New(userSvc)

	// 5. Kafka Consumer
	// In prod, use split brokers string
	consumer := events.NewConsumer([]string{kafkaBrokers}, "social.events.v1", userSvc)
	go consumer.Start(ctx)

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
					log.Printf("Status cleanup error: %v", err)
				} else if cleared > 0 {
					log.Printf("Cleared %d expired statuses", cleared)
				}
			}
		}
	}()

	// 6. Server
	r := gin.Default()
	userHandler.RegisterRoutes(r)

	log.Printf("Starting user-service on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
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
	}

	for _, stmt := range ddl {
		if _, err := db.Exec(ctx, stmt); err != nil {
			log.Printf("Warning: phase6 schema migration: %v", err)
		}
	}
	log.Println("Phase 6 schema ensured (channels, business pages, reputation, endorsements, status)")
}
