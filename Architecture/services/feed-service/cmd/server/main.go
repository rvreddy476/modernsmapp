package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/facebook-like/feed-service/internal/events"
	"github.com/facebook-like/feed-service/internal/http"
	"github.com/facebook-like/feed-service/internal/pipeline"
	"github.com/facebook-like/feed-service/internal/ranking"
	"github.com/facebook-like/feed-service/internal/service"
	"github.com/facebook-like/feed-service/internal/store/postgres"
	"github.com/facebook-like/feed-service/internal/store/scylla"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Config
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8086"
	}
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	scyllaHosts := os.Getenv("SCYLLA_HOSTS")
	if scyllaHosts == "" {
		scyllaHosts = "localhost"
	}
	kafkaBrokers := os.Getenv("KAFKA_BROKERS")
	if kafkaBrokers == "" {
		kafkaBrokers = "localhost:9092"
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

	// 3. Database (ScyllaDB)
	cluster := gocql.NewCluster(scyllaHosts)
	cluster.Keyspace = "social_feed"
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
	timelineStore := scylla.New(scyllaSession)
	feedSvc := service.New(timelineStore, pgStore, rdb)

	// 6. Ranking middleware (v2.0)
	ranker := ranking.NewRanker(rdb, 20*time.Millisecond)
	feedSvc.SetRanker(ranker)
	log.Println("Ranking middleware initialized (circuit breaker: 20ms)")

	// 7. Data pipelines (v2.0)
	affinityPipeline := pipeline.NewAffinityPipeline(dbPool, rdb)
	velocityTracker := pipeline.NewVelocityTracker(rdb)

	// Warm affinity signals from Postgres into Redis on startup
	go func() {
		if err := affinityPipeline.Run(ctx); err != nil {
			log.Printf("Warning: affinity warmup failed: %v", err)
		} else {
			log.Println("Affinity signals warmed into Redis")
		}
	}()

	// Start velocity tracker (runs every 5 minutes)
	go velocityTracker.Start(ctx)
	log.Println("Velocity tracker started")

	feedHandler := http.New(feedSvc)

	// 8. Kafka Consumer (now also handles PostReacted and CommentCreated)
	consumer := events.NewConsumer(
		[]string{kafkaBrokers},
		"feed-service-group",
		"social.events.v1",
		feedSvc,
		rdb,
	)
	go consumer.Start(ctx)
	defer consumer.Close()

	// 9. Server
	r := gin.Default()
	feedHandler.RegisterRoutes(r)

	log.Printf("Starting feed-service on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}
