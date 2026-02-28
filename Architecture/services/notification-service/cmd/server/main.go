package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/facebook-like/notification-service/internal/events"
	"github.com/facebook-like/notification-service/internal/http"
	"github.com/facebook-like/notification-service/internal/service"
	"github.com/facebook-like/notification-service/internal/store/scylla"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
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

	// 4. Dependencies
	scyllaStore := scylla.New(session)
	notifSvc := service.New(scyllaStore, rdb)
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
