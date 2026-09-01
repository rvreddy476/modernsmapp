package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/payments-service/database"
	"github.com/atpost/payments-service/internal/gateway"
	nethttp "github.com/atpost/payments-service/internal/http"
	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	tracepkg "github.com/atpost/shared/o11y/trace"
	"github.com/atpost/shared/outbox"
	sharedserver "github.com/atpost/shared/server"
	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/shared/transport"
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

	// Phase F3.5 — tracing init. See commerce-service for the rationale.
	tracerProvider, _ := tracepkg.InitTracer("payments-service", env("OTEL_EXPORTER_OTLP_ENDPOINT", "http://jaeger:4317"))
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tracerProvider.Shutdown(shutdownCtx)
	}()

	port := env("HTTP_PORT", "8102")
	pgDSN := os.Getenv("POSTGRES_DSN")
	kafkaBrokers := env("KAFKA_BROKERS", "localhost:9092")

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
		slog.Error("failed to bootstrap payments schema", "error", err)
		os.Exit(1)
	}
	slog.Info("payments schema ready")

	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

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

	// Select payment gateway. Audit P8: previously a missing
	// RAZORPAY_KEY_ID silently selected the stub — production deploys
	// that forgot the env ran with a stub that never moved real money
	// (matches media-service H8 stub-in-prod pattern). Now require
	// PAYMENTS_ALLOW_STUB=true to opt into the stub explicitly; if
	// neither real creds nor the opt-in are set, refuse to start so
	// the misconfiguration is visible at boot.
	// LB-24 / M-10: the production configuration contract. Every branch
	// below either yields a fully-configured dependency or exits. A service
	// that boots with a stub gateway, a real key and an empty secret, or no
	// caller allowlist is worse than one that refuses to start, because the
	// misconfiguration is silent and money-shaped.
	allowStub := os.Getenv("PAYMENTS_ALLOW_STUB") == "true"
	isProd := env("ENV", "dev") == "prod"
	if allowStub && isProd {
		slog.Error("payments: PAYMENTS_ALLOW_STUB must never be set when ENV=prod")
		os.Exit(1)
	}

	var (
		gw       gateway.PaymentGateway
		provider gateway.Provider
	)
	keyID := os.Getenv("RAZORPAY_KEY_ID")
	keySecret := os.Getenv("RAZORPAY_KEY_SECRET")
	webhookSecret := os.Getenv("RAZORPAY_WEBHOOK_SECRET")

	switch {
	case keyID != "":
		// M-10: the old code accepted a key id with an EMPTY secret and
		// then failed every provider call at runtime. Refuse at boot.
		if keySecret == "" {
			slog.Error("payments: RAZORPAY_KEY_SECRET is required whenever RAZORPAY_KEY_ID is set")
			os.Exit(1)
		}
		if webhookSecret == "" {
			slog.Error("payments: RAZORPAY_WEBHOOK_SECRET is required with the Razorpay gateway; " +
				"without it webhook verification cannot fail closed")
			os.Exit(1)
		}
		gw = gateway.NewRazorpayGateway(keyID, keySecret)
		provider = gateway.NewRazorpayProvider(keyID, keySecret, webhookSecret)
		slog.Info("payments: Razorpay provider active")
	case allowStub:
		gw = &gateway.StubGateway{}
		provider = gateway.NewRazorpayProvider("stub", "stub", env("RAZORPAY_WEBHOOK_SECRET", "stub-webhook-secret"))
		slog.Warn("payments: STUB GATEWAY ACTIVE — no real money will move")
	default:
		slog.Error("payments: RAZORPAY_KEY_ID is required; set PAYMENTS_ALLOW_STUB=true for dev/test only")
		os.Exit(1)
	}

	// N3: the money path gets the RECOVERABLE provider port, not just the
	// legacy gateway. `provider` was already constructed above and handed
	// only to the HTTP handler, so an ambiguous CreateOrder timeout had no
	// FetchByIdempotencyKey recovery available to it.
	svc := service.NewWithDialer(store, kafkaBrokers, gw, kafkaDialer).WithProvider(provider)

	// A2: build the caller allowlist. Each calling service has its OWN
	// public key and its OWN permitted operations and reference types, so
	// food-service cannot act on a commerce order and vice versa. There is
	// no shared secret and no "trusted because it is inside the cluster".
	verifier, err := buildServiceTokenVerifier()
	if err != nil {
		slog.Error("payments: service-token configuration is invalid", "error", err)
		os.Exit(1)
	}

	handler := nethttp.New(svc).WithServiceAuth(verifier).WithProvider(provider)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.OtelTracing("payments-service"))
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))
	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	if err := handler.RegisterRoutes(r); err != nil {
		slog.Error("payments: refusing to serve", "error", err)
		os.Exit(1)
	}

	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	// LB-7 / R-2: every domain event now leaves through the transactional
	// outbox, with RequireAll acks. Writing to Kafka directly after a commit
	// meant a broker outage lost a captured payment permanently, and
	// leader-only acks meant an unreplicated leader failure did the same
	// while the row was already marked published.
	outboxPublisher := outbox.New(dbPool, outbox.Config{
		DBSchema:     "payments",
		KafkaBrokers: kafkaBrokers,
		DefaultTopic: "social.events.v1",
	})
	go outboxPublisher.Run(workerCtx)
	slog.Info("payments: outbox publisher started (RequireAll acks)")

	// A6 / LB-8: refunds are durable commands. This worker is what actually
	// contacts the provider, using the command's deterministic idempotency
	// key so a retry after an ambiguous timeout yields one refund.
	go svc.RunRefundWorker(workerCtx, 15*time.Second)

	// LB-9: resolve payments the webhook never told us about, and surface
	// ledger-vs-provider drift instead of letting it accumulate silently.
	go svc.RunReconciler(workerCtx,
		time.Duration(envInt("PAYMENTS_RECONCILE_INTERVAL_SEC", 60))*time.Second,
		time.Duration(envInt("PAYMENTS_PENDING_AGE_SEC", 600))*time.Second)

	sharedserver.Run(r, sharedserver.Config{
		Port: port,
		OnShutdown: func() {
			workerCancel()
			dbPool.Close()
		},
	})
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		slog.Warn("payments: invalid integer env, using default", "key", key, "default", def)
	}
	return def
}

// buildServiceTokenVerifier constructs the A2 caller allowlist from the
// environment.
//
// Configuration shape, one entry per calling service:
//
//	SERVICE_CALLERS=commerce-service,food-service
//	SERVICE_CALLER_COMMERCE_SERVICE_KID=c1
//	SERVICE_CALLER_COMMERCE_SERVICE_PUBKEY=<base64 ed25519 public key>
//	SERVICE_CALLER_COMMERCE_SERVICE_OPS=payments:intent.create,payments:intent.read,payments:refund.create
//	SERVICE_CALLER_COMMERCE_SERVICE_REFTYPES=order
//
// Note what is NOT here: any private key. payments verifies and can never
// mint, so a compromise of this service cannot forge a caller.
func buildServiceTokenVerifier() (*servicetoken.Verifier, error) {
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	raw := os.Getenv("SERVICE_CALLERS")
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("SERVICE_CALLERS is required: payments must know which services may call it")
	}
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		prefix := "SERVICE_CALLER_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		kid := os.Getenv(prefix + "_KID")
		pub := os.Getenv(prefix + "_PUBKEY")
		ops := splitList(os.Getenv(prefix + "_OPS"))
		refs := splitList(os.Getenv(prefix + "_REFTYPES"))
		if kid == "" || pub == "" {
			return nil, fmt.Errorf("caller %q is missing %s_KID or %s_PUBKEY", name, prefix, prefix)
		}
		if len(ops) == 0 || len(refs) == 0 {
			// An empty allowlist is not "allow everything" — it is a
			// configuration error, and treating it as permissive is how
			// the original shared-key hole was built.
			return nil, fmt.Errorf("caller %q must declare both %s_OPS and %s_REFTYPES", name, prefix, prefix)
		}
		if err := v.RegisterBase64(name, kid, pub, ops, refs); err != nil {
			return nil, fmt.Errorf("caller %q: %w", name, err)
		}
		slog.Info("payments: registered service caller", "caller", name, "kid", kid, "ops", ops, "ref_types", refs)
	}
	if v.Callers() == 0 {
		return nil, fmt.Errorf("SERVICE_CALLERS produced no usable entries")
	}
	return v, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
