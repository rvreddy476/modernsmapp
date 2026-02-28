package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/facebook-like/notification-service/internal/events"
	"github.com/facebook-like/notification-service/internal/http"
	"github.com/facebook-like/notification-service/internal/service"
	"github.com/facebook-like/notification-service/internal/store/postgres"
	"github.com/facebook-like/notification-service/internal/store/scylla"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Config
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8088"
	}

	scyllaHosts := os.Getenv("SCYLLA_HOSTS")
	if scyllaHosts == "" {
		scyllaHosts = "scylla"
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "redis:6379"
	}

	kafkaBrokers := os.Getenv("KAFKA_BROKERS")
	if kafkaBrokers == "" {
		kafkaBrokers = "redpanda:9092"
	}

	// 2. Database (Scylla)
	cluster := gocql.NewCluster(strings.Split(scyllaHosts, ",")...)
	cluster.Keyspace = "social_notify"
	cluster.Consistency = gocql.Quorum
	session, err := cluster.CreateSession()
	if err != nil {
		log.Fatalf("Unable to connect to Scylla: %v", err)
	}
	defer session.Close()
	log.Println("Connected to Scylla")

	// 3. Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Unable to connect to Redis: %v", err)
	}
	log.Println("Connected to Redis")

	// 3b. Database (Postgres — for preferences & devices)
	pgDSN := os.Getenv("POSTGRES_DSN")
	var pgStore *postgres.Store
	if pgDSN != "" {
		ctx := context.Background()
		dbPool, err := pgxpool.New(ctx, pgDSN)
		if err != nil {
			log.Printf("Warning: Unable to connect to Postgres (preferences disabled): %v", err)
		} else {
			defer dbPool.Close()
			if err := dbPool.Ping(ctx); err != nil {
				log.Printf("Warning: Postgres ping failed: %v", err)
			} else {
				log.Println("Connected to Postgres")
				pgStore = postgres.New(dbPool)
				ensureNotifSchema(ctx, dbPool)
			}
		}
	}

	// 4. Dependencies
	scyllaStore := scylla.New(session)
	notifSvc := service.New(scyllaStore, rdb)
	if pgStore != nil {
		notifSvc.SetPGStore(pgStore)
	}
	notifHandler := http.New(notifSvc, rdb)

	// 5. Kafka Consumer
	consumer := events.NewConsumer(
		strings.Split(kafkaBrokers, ","),
		"notification-service-group",
		"social.events.v1",
		notifSvc,
	)
	go consumer.Start(context.Background())
	log.Println("Started Kafka Consumer")

	// 6. Server
	r := gin.Default()
	notifHandler.RegisterRoutes(r)

	log.Printf("Starting notification-service on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}

// ensureNotifSchema creates Postgres tables for notification preferences and devices.
func ensureNotifSchema(ctx context.Context, db *pgxpool.Pool) {
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS notification_preferences (
			user_id          UUID PRIMARY KEY,
			email_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
			push_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
			sms_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
			quiet_hours_start TIME,
			quiet_hours_end   TIME,
			muted_types      JSONB NOT NULL DEFAULT '[]',
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS user_devices (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id    UUID NOT NULL,
			platform   TEXT NOT NULL CHECK (platform IN ('ios', 'android', 'web')),
			push_token TEXT NOT NULL,
			is_active  BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(user_id, platform, push_token)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_devices_user ON user_devices (user_id) WHERE is_active = TRUE`,
	}
	for _, stmt := range ddl {
		if _, err := db.Exec(ctx, stmt); err != nil {
			log.Printf("Warning: notification schema migration: %v", err)
		}
	}
	log.Println("Notification preferences schema ensured")
}
