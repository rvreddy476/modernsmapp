package main

import (
	"context"
	"log"
	"os"

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

	// 4. Dependencies
	userStore := store.New(dbPool)
	userSvc := service.New(userStore, rdb)
	userHandler := http.New(userSvc)

	// 5. Kafka Consumer
	// In prod, use split brokers string
	consumer := events.NewConsumer([]string{kafkaBrokers}, "social.events.v1", userSvc)
	go consumer.Start(ctx)

	// 6. Server
	r := gin.Default()
	userHandler.RegisterRoutes(r)

	log.Printf("Starting user-service on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}
