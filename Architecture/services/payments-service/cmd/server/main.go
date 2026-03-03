package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	nethttp "github.com/facebook-like/payments-service/internal/http"
	"github.com/facebook-like/payments-service/internal/service"
	"github.com/facebook-like/payments-service/internal/store/postgres"
	"github.com/facebook-like/shared/health"
	"github.com/facebook-like/shared/middleware"
	"github.com/facebook-like/shared/o11y/logging"
	"github.com/facebook-like/shared/o11y/metrics"
	sharedserver "github.com/facebook-like/shared/server"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	logging.Init(logging.Config{ServiceName: "payments-service"})

	port := env("HTTP_PORT", "8102")
	pgDSN := os.Getenv("POSTGRES_DSN")
	kafkaBrokers := env("KAFKA_BROKERS", "localhost:9092")

	ctx := context.Background()

	dbPool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		slog.Error("postgres ping failed", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to postgres")

	httpMetrics := metrics.NewHTTPMetrics("payments-service")
	dbMetrics := metrics.NewDBPoolMetrics("payments-service", "postgres")
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stat := dbPool.Stat()
				dbMetrics.Update(metrics.PgxPoolStat{
					AcquireCount:  stat.AcquireCount(),
					AcquiredConns: int32(stat.AcquiredConns()),
					IdleConns:     int32(stat.IdleConns()),
					TotalConns:    int32(stat.TotalConns()),
					MaxConns:      stat.MaxConns(),
				})
			}
		}
	}()

	checker := health.New("payments-service")
	checker.Register("postgres", health.PingCheck(dbPool))

	store := postgres.New(dbPool)
	svc := service.New(store, kafkaBrokers)
	handler := nethttp.New(svc)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))
	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)

	sharedserver.Run(r, sharedserver.Config{
		Port: port,
		OnShutdown: func() {
			dbPool.Close()
		},
	})
}
