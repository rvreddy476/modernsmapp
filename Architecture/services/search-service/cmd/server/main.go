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

	// 2. OpenSearch Store
	searchStore, err := search.New(opensearchURL)
	if err != nil {
		log.Fatalf("Failed to initialize OpenSearch store: %v", err)
	}
	log.Println("Connected to OpenSearch")

	// 3. Kafka Consumer
	consumer := events.NewConsumer(
		strings.Split(kafkaBrokers, ","),
		"search-service-group",
		"social.events.v1",
		searchStore,
	)
	go consumer.Start(context.Background())
	log.Println("Started Kafka Consumer")

	// 4. HTTP Handlers
	handler := http.New(searchStore)

	// 5. Server
	r := gin.Default()
	handler.RegisterRoutes(r)

	log.Printf("Starting search-service on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}
