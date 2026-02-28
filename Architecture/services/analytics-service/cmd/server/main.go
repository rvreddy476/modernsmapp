package main

import (
	"context"
	"log"
	"os"

	"github.com/facebook-like/analytics-service/internal/http"
	"github.com/facebook-like/analytics-service/internal/service"
	"github.com/facebook-like/analytics-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. Config
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8093"
	}
	pgDSN := os.Getenv("POSTGRES_DSN")

	// 2. Database
	ctx := context.Background()
	dbPool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		log.Fatalf("Unable to connect to Postgres: %v", err)
	}
	defer dbPool.Close()

	// 3. Dependencies
	store := postgres.New(dbPool)
	svc := service.New(store)
	handler := http.New(svc)

	// 4. Server
	r := gin.Default()
	handler.RegisterRoutes(r)

	log.Printf("Starting analytics-service on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}
