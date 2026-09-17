package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/admin-service/database"
	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/http"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "admin-service"})

	// 2. Config
	port := env("HTTP_PORT", "8096")
	pgDSN := os.Getenv("POSTGRES_DSN")
	kafkaBrokers := env("KAFKA_BROKERS", "redpanda:9092")

	// 3. Database
	ctx := context.Background()
	poolCfg, err := pgxpool.ParseConfig(pgDSN)
	if err != nil {
		slog.Error("parse db config", "error", err)
		os.Exit(1)
	}
	poolCfg.MaxConns = 25
	poolCfg.MinConns = 5
	poolCfg.MaxConnLifetime = 15 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	dbPool, err := pgxpool.NewWithConfig(ctx, poolCfg)
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

	if err := postgres.BootstrapSchema(ctx, dbPool, database.SetupSQL, database.Migrations); err != nil {
		slog.Error("failed to bootstrap admin schema", "error", err)
		os.Exit(1)
	}
	slog.Info("admin schema ready")

	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

	// 4. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("admin-service")
	dbMetrics := metrics.NewDBPoolMetrics("admin-service", "postgres")
	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 5. Health checker
	checker := health.New("admin-service")
	checker.Register("postgres", health.PingCheck(dbPool))

	// 6. Dependencies
	store := postgres.New(dbPool)

	// CERT-In log retention: purge audit-log rows past the retention
	// window once a day.
	go auditLogRetentionSweep(ctx, store)

	internalKey := env("INTERNAL_SERVICE_KEY", "")
	authURL := env("AUTH_SERVICE_URL", "http://identity-auth:8081")
	authClient := service.NewAuthClient(authURL, internalKey)
	svc := service.NewWithDialer(store, kafkaBrokers, kafkaDialer, authClient)

	// OAuth client secrets were once stored in plaintext; hash any that still
	// are. Idempotent, so it runs on every boot.
	if n, err := svc.HashPlaintextOAuthSecrets(ctx); err != nil {
		slog.Error("failed to hash plaintext oauth client secrets", "error", err)
		os.Exit(1)
	} else if n > 0 {
		slog.Info("hashed plaintext oauth client secrets", "rows", n)
	}

	// Admin access. Permissions come from identity (cached briefly, never on
	// error); ADMIN_REQUIRE_MFA is true unless set false or ENV is local/dev.
	requireMFA, err := adminauth.RequireMFAFromEnv(os.Getenv)
	if err != nil {
		slog.Error("invalid admin MFA configuration", "error", err)
		os.Exit(1)
	}
	if !requireMFA {
		slog.Warn("ADMIN_REQUIRE_MFA is off: admin routes do not require X-Admin-MFA (dev only, until the gateway stamps it)")
	}
	identity := adminauth.NewIdentityClient(authURL, internalKey)
	gate := http.NewGate(adminauth.NewCachedPermissions(identity, adminauth.PermissionCacheTTL), svc, requireMFA)
	approvalSvc := approvals.NewService(store, identity)
	handler := http.New(svc, gate, approvalSvc)

	// Product dashboards. admin-service signs a 60 s service token per call
	// (audience = product, scope = the permission checked, act = the admin).
	// Without ADMIN_SERVICE_TOKEN_KEY the Dating routes answer 503.
	tokenSigner, err := service.SignerFromEnv(os.Getenv)
	if err != nil {
		slog.Error("invalid admin-service token key", "error", err)
		os.Exit(1)
	}
	if tokenSigner == nil {
		slog.Warn("ADMIN_SERVICE_TOKEN_KEY not set: product admin routes (Dating, Feast, MStore, Trust & safety) answer 503 PRODUCT_UNAVAILABLE (also Monetization, Payments, the content apps and Mopedu)")
	}
	refundThreshold, err := refundThresholdFromEnv(os.Getenv)
	if err != nil {
		slog.Error("invalid refund two-person threshold", "error", err)
		os.Exit(1)
	}
	handler.WithDating(service.NewDatingClient(env("DATING_SERVICE_URL", "http://dating-service:8112"), tokenSigner)).
		WithFood(service.NewFoodClient(env("FOOD_SERVICE_URL", "http://food-service:8113"), tokenSigner), refundThreshold).
		WithCommerce(service.NewCommerceClient(env("COMMERCE_SERVICE_URL", "http://commerce-service:8109"), tokenSigner)).
		WithTrustSafety(service.NewTrustSafetyClient(env("TRUST_SAFETY_SERVICE_URL", "http://trust-safety-service:8091"), tokenSigner)).
		WithMonetization(service.NewMonetizationClient(env("MONETIZATION_SERVICE_URL", "http://monetization-service:8099"), tokenSigner)).
		WithPayments(service.NewPaymentsClient(env("PAYMENTS_SERVICE_URL", "http://payments-service:8102"), tokenSigner)).
		// Content apps: Social and Tube (post-service), business pages
		// (user-service), Q&A, and Chat (channel, group, community).
		WithPost(service.NewPostClient(env("POST_SERVICE_URL", "http://post-service:8084"), tokenSigner)).
		WithUserPages(service.NewUserPagesClient(env("USER_SERVICE_URL", "http://user-service:8082"), tokenSigner)).
		WithQA(service.NewQAClient(env("QA_SERVICE_URL", "http://qa-service:8108"), tokenSigner)).
		WithChat(
			service.NewChannelClient(env("CHANNEL_SERVICE_URL", "http://channel-service:8106"), tokenSigner),
			service.NewGroupClient(env("GROUP_SERVICE_URL", "http://group-service:8090"), tokenSigner),
			service.NewCommunityClient(env("COMMUNITY_SERVICE_URL", "http://community-service:8107"), tokenSigner),
		).
		// Mopedu: rider-service.
		WithRider(service.NewRiderClient(env("RIDER_SERVICE_URL", "http://rider-service:8116"), tokenSigner)).
		// Access page: identity's admin console family, at the same
		// AUTH_SERVICE_URL the permission lookups use (audience "identity";
		// identity registers the same public key as ADMIN_SERVICE_TOKEN_PUBKEY).
		WithIdentity(service.NewIdentityConsoleClient(authURL, tokenSigner))
	slog.Info("refund two-person threshold (Feast, monetization, payments)", "paise", refundThreshold)

	// 7. Gin with middleware stack
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))
	r.Use(middleware.RequireInternalKey(env("INTERNAL_SERVICE_KEY", "")))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	if err := handler.RegisterAllRoutes(r); err != nil {
		slog.Error("refusing to boot: admin route table is not fully declared", "error", err)
		os.Exit(1)
	}

	// 8. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			dbPool.Close()
			slog.Info("cleanup completed")
		},
	}); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

// refundThresholdFromEnv reads ADMIN_REFUND_TWO_PERSON_THRESHOLD_PAISE: a
// Feast refund at or above it needs a second approver. Unset means ₹5,000;
// anything but a positive whole number of paise refuses boot.
func refundThresholdFromEnv(getenv func(string) string) (int64, error) {
	v := strings.TrimSpace(getenv("ADMIN_REFUND_TWO_PERSON_THRESHOLD_PAISE"))
	if v == "" {
		return http.DefaultRefundTwoPersonThresholdPaise, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("ADMIN_REFUND_TWO_PERSON_THRESHOLD_PAISE must be a positive whole number of paise")
	}
	return n, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// auditLogRetentionSweep purges audit-log rows past the CERT-In retention
// window once a day. AUDIT_LOG_RETENTION_DAYS lengthens the window; it is
// clamped to a 180-day minimum so the policy can be extended but never
// shortened below the legal floor.
func auditLogRetentionSweep(ctx context.Context, store *postgres.Store) {
	retentionDays := 180
	if v := os.Getenv("AUDIT_LOG_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > retentionDays {
			retentionDays = n
		}
	}
	sweep := func() {
		c, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if n, err := store.PurgeAuditLogsOlderThan(c, retentionDays); err != nil {
			slog.Error("audit log retention sweep failed", "error", err)
		} else if n > 0 {
			slog.Info("audit log retention sweep", "purged", n, "retention_days", retentionDays)
		}
	}
	sweep() // run once on boot
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

func collectDBPoolStats(ctx context.Context, pool *pgxpool.Pool, m *metrics.DBPoolMetrics) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stat := pool.Stat()
			m.Update(metrics.PgxPoolStat{
				AcquireCount:  stat.AcquireCount(),
				AcquiredConns: stat.AcquiredConns(),
				IdleConns:     stat.IdleConns(),
				TotalConns:    stat.TotalConns(),
				MaxConns:      stat.MaxConns(),
			})
		}
	}
}
