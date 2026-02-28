package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	apihttp "github.com/facebook-like/message-service/internal/http"
	"github.com/facebook-like/message-service/internal/kafka"
	"github.com/facebook-like/message-service/internal/service"
	"github.com/facebook-like/message-service/internal/store/postgres"
	"github.com/facebook-like/message-service/internal/store/scylla"
	"github.com/facebook-like/message-service/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Structured Logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// 2. Config
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8092"
	}

	pgDSN := os.Getenv("POSTGRES_DSN")
	if pgDSN == "" {
		pgDSN = "postgres://postgres:postgres@localhost:5432/identity_db?sslmode=disable"
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
		kafkaBrokers = "kafka:9092"
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "dev_secret_change_me"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 3. Database (Postgres)
	pgPool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		slog.Error("failed to connect to Postgres", "error", err)
		os.Exit(1)
	}
	defer pgPool.Close()
	if err := pgPool.Ping(ctx); err != nil {
		slog.Error("postgres ping failed", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to Postgres")

	// 4. Database (ScyllaDB)
	cluster := gocql.NewCluster(strings.Split(scyllaHosts, ",")...)
	cluster.Keyspace = "postbook"
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 5 * time.Second
	session, err := cluster.CreateSession()
	if err != nil {
		slog.Error("failed to connect to ScyllaDB", "error", err, "hosts", scyllaHosts)
		os.Exit(1)
	}
	defer session.Close()
	slog.Info("connected to ScyllaDB", "keyspace", "postbook")

	// 5. Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("failed to connect to Redis", "error", err, "addr", redisAddr)
		os.Exit(1)
	}
	slog.Info("connected to Redis")

	// 6. Kafka Producer
	kp := kafka.NewProducer(strings.Split(kafkaBrokers, ","), "postbook-messages")
	defer kp.Close()
	slog.Info("connected to Kafka", "topic", "postbook-messages")

	// 7. Dependencies
	scyllaStore := scylla.New(session)
	convStore := postgres.New(pgPool)
	msgSvc := service.New(scyllaStore, convStore, rdb, kp)
	msgHandler := apihttp.New(msgSvc)

	// 8. Server
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	// Simple middleware for structured logging
	r.Use(func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration", time.Since(start),
			"ip", c.ClientIP(),
		)
	})

	msgHandler.RegisterRoutes(r)

	// WebSocket endpoint for real-time chat delivery
	wsHub := ws.NewHub(rdb, jwtSecret)
	r.GET("/v1/ws/connect", wsHub.HandleConnect)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// 9. Graceful Shutdown
	go func() {
		slog.Info("starting message-service", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}

	slog.Info("server exited")
}
