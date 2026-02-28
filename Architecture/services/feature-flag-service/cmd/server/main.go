package main

import (
	"context"
	"log"
	"os"

	"github.com/facebook-like/feature-flag-service/internal/http"
	"github.com/facebook-like/feature-flag-service/internal/service"
	"github.com/facebook-like/feature-flag-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Config
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8095"
	}
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")

	// 2. Database
	ctx := context.Background()
	dbPool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		log.Fatalf("Unable to connect to Postgres: %v", err)
	}
	defer dbPool.Close()

	// 3. Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Printf("Warning: Redis not connected: %v", err)
	}

	// 4. Dependencies
	store := postgres.New(dbPool)
	svc := service.New(store, rdb)
	handler := http.New(svc)

	// 5. Server
	r := gin.Default()
	handler.RegisterRoutes(r)

	log.Printf("Starting feature-flag-service on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}
