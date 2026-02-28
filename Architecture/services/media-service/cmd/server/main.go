package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mediaEvents "github.com/facebook-like/media-service/internal/events"
	mediaHttp "github.com/facebook-like/media-service/internal/http"
	"github.com/facebook-like/media-service/internal/service"
	"github.com/facebook-like/media-service/internal/store/blob"
	"github.com/facebook-like/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. Config
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8087"
	}
	pgDSN := os.Getenv("POSTGRES_DSN")
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("JWT_SECRET environment variable is required")
	}

	minioEndpoint := os.Getenv("MINIO_ENDPOINT")
	minioAccessKey := os.Getenv("MINIO_ACCESS_KEY")
	minioSecretKey := os.Getenv("MINIO_SECRET_KEY")
	minioBucket := os.Getenv("MINIO_BUCKET")
	minioUseSSL := os.Getenv("MINIO_USE_SSL") == "true"
	minioPublicEndpoint := os.Getenv("MINIO_PUBLIC_ENDPOINT") // e.g. http://localhost:9000
	kafkaBrokers := os.Getenv("KAFKA_BROKERS")

	if minioEndpoint == "" {
		minioEndpoint = "minio:9000"
		minioAccessKey = "minioadmin"
		minioSecretKey = "minioadmin"
		minioBucket = "media"
	}
	if kafkaBrokers == "" {
		kafkaBrokers = "kafka:9092"
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

	// 3. Blob Store (MinIO)
	blobStore, err := blob.NewWithPublicEndpoint(minioEndpoint, minioAccessKey, minioSecretKey, minioBucket, minioUseSSL, minioPublicEndpoint)
	if err != nil {
		log.Fatalf("Unable to connect to MinIO: %v\n", err)
	}
	log.Println("Connected to MinIO")

	// 4. Dependencies
	pgStore := postgres.New(dbPool)
	mediaSvc := service.New(pgStore, blobStore)

	// 5. Kafka producer for video transcode events
	brokers := strings.Split(kafkaBrokers, ",")
	producer := mediaEvents.NewProducer(brokers, "media.events")
	defer producer.Close()
	mediaSvc.SetProducer(producer)
	log.Println("Kafka producer initialized")

	// 6. HTTP Server with middleware
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(mediaHttp.RequestLoggerMiddleware())

	authMW := mediaHttp.AuthMiddleware(jwtSecret)
	optionalAuthMW := mediaHttp.OptionalAuthMiddleware(jwtSecret)

	mediaHandler := mediaHttp.New(mediaSvc)
	mediaHandler.RegisterRoutes(r, authMW, optionalAuthMW)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// 7. Start server in goroutine
	go func() {
		log.Printf("Starting media-service on port %s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// 8. Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down media-service...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("Media-service stopped")
}
