package main

import (
	"context"
	"log"
	"os"

	"github.com/facebook-like/group-service/internal/http"
	"github.com/facebook-like/group-service/internal/service"
	"github.com/facebook-like/group-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Config
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8090"
	}
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	msgURL := os.Getenv("MESSAGE_SERVICE_URL")
	if msgURL == "" {
		msgURL = "http://chat-message-service:8092"
	}
	postURL := os.Getenv("POST_SERVICE_URL")
	if postURL == "" {
		postURL = "http://post-service:8084"
	}
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "dev_secret_change_me"
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
	groupStore := store.New(dbPool)
	groupSvc := service.New(groupStore, rdb, msgURL, postURL, jwtSecret)
	groupHandler := http.New(groupSvc)

	// 5. Server
	r := gin.Default()
	groupHandler.RegisterRoutes(r)

	log.Printf("Starting group-service on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}
