package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/facebook-like/graph-service/internal/events"
	"github.com/facebook-like/graph-service/internal/http"
	"github.com/facebook-like/graph-service/internal/service"
	"github.com/facebook-like/graph-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Config
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8083"
	}
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := os.Getenv("KAFKA_BROKERS")
	if kafkaBrokers == "" {
		kafkaBrokers = "redpanda:9092"
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

	// 4. Kafka Producer
	producer := events.NewProducer(strings.Split(kafkaBrokers, ","), "social.events.v1")
	defer producer.Close()
	log.Println("Kafka producer ready")

	// 5. Dependencies
	graphStore := store.New(dbPool)
	graphSvc := service.New(graphStore, rdb, producer)
	graphHandler := http.New(graphSvc)

	// 6. Server
	r := gin.Default()
	graphHandler.RegisterRoutes(r)

	log.Printf("Starting graph-service on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}
