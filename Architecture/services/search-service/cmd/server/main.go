package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/facebook-like/search-service/internal/events"
	"github.com/facebook-like/search-service/internal/http"
	"github.com/facebook-like/search-service/internal/store/search"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Config
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8089"
	}

	opensearchURL := os.Getenv("OPENSEARCH_URL")
	if opensearchURL == "" {
		opensearchURL = "http://opensearch:9200"
	}

	kafkaBrokers := os.Getenv("KAFKA_BROKERS")
	if kafkaBrokers == "" {
		kafkaBrokers = "redpanda:9092"
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "redis:6379"
	}

	// 2. OpenSearch Store
	searchStore, err := search.New(opensearchURL)
	if err != nil {
		log.Fatalf("Failed to initialize OpenSearch store: %v", err)
	}
	log.Println("Connected to OpenSearch")

	// 3. Redis
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Redis ping failed: %v", err)
	}
	log.Println("Connected to Redis")

	// 4. Kafka Consumer
	consumer := events.NewConsumer(
		strings.Split(kafkaBrokers, ","),
		"search-service-group",
		"social.events.v1",
		searchStore,
	)
	go consumer.Start(ctx)
	log.Println("Started Kafka Consumer")

	// 5. HTTP Handlers
	handler := http.New(searchStore, rdb)

	// 6. Server
	r := gin.Default()
	handler.RegisterRoutes(r)

	log.Printf("Starting search-service on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}
