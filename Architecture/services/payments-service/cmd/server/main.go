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
	"github.com/atpost/payments-service/internal/config"
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

	// Kafka dialer config is validated up front so a bad TLS/SASL env
	// fails at boot rather than inside the outbox publisher goroutine
	// (shared/outbox builds its own dialer from the same env). Nothing in
	// the request path writes to Kafka any more — see service.New.
	if _, err := transport.KafkaDialerFromEnv(); err != nil {
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

	// LB-24 / M-10: the production configuration contract. Every branch
	// below either yields a fully-configured dependency or exits. A service
	// that boots with a stub gateway, a real key and an empty secret, or no
	// caller credential is worse than one that refuses to start, because the
	// misconfiguration is silent and money-shaped.
	//
	// config.Resolve owns the gateway rules (see its tests): real credentials
	// always require RAZORPAY_WEBHOOK_SECRET (the old check was keyed on the
	// stub flag, so creds + PAYMENTS_ALLOW_STUB=true booted with signature
	// checks off), the stub needs an explicit PAYMENTS_ALLOW_STUB=true, and
	// anything else refuses to start.
	cfg, err := config.Resolve(os.Getenv)
	if err != nil {
		slog.Error("payments: invalid boot configuration", "error", err)
		os.Exit(1)
	}
	isProd := env("ENV", "dev") == "prod"
	if cfg.Mode == config.ModeStub && isProd {
		slog.Error("payments: PAYMENTS_ALLOW_STUB must never be set when ENV=prod")
		os.Exit(1)
	}

	var (
		gw       gateway.PaymentGateway
		provider gateway.Provider
	)
	switch cfg.Mode {
	case config.ModeRazorpay:
		gw = gateway.NewRazorpayGateway(cfg.RazorpayKeyID, cfg.RazorpayKeySecret)
		provider = gateway.NewRazorpayProvider(cfg.RazorpayKeyID, cfg.RazorpayKeySecret, cfg.WebhookSecret)
		slog.Info("payments: Razorpay provider active (production credentials detected)")
	case config.ModeStub:
		// The stub accepts every signature and never contacts a PSP. It is
		// wired as the legacy gateway only: a RazorpayProvider built on
		// "stub" credentials would make real HTTP calls to Razorpay and
		// fail every checkout, and a stub cannot verify a webhook anyway.
		//
		// So `provider` stays nil here, and POST /v1/payments/webhook
		// answers 503 — deliberately, because there is nothing to verify a
		// signature against. The reconciler is not started either, for the
		// same reason. Those are the only two authorities A1 permits, so on
		// this deployment a stub intent had NOTHING that could ever make it
		// terminal, and the order behind it could never be paid.
		//
		// The settlement path in stub mode, and only in stub mode, is
		// therefore:
		//
		//	commerce POST /orders/:id/payment/confirm  {gateway:"stub"}
		//	  └─ payments POST /internal/intents/:id/verify
		//	       └─ Service.VerifyIntent, with stub settlement on, applies
		//	          the SAME atomic ApplyWebhook transaction a real
		//	          provider event would (inbox + effect + outbox)
		//
		// Both halves are wired from their own service's configuration:
		// WithStubSettlement below from cfg.Mode here, and commerce's route
		// registration + fence exemption from its own PAYMENTS_ALLOW_STUB —
		// and commerce additionally asks payments which provider is live
		// before settling, so a stack with the flag left on next to real
		// credentials refuses rather than settling on a callback. With real
		// credentials this branch is not taken at all, and the
		// signature-verified webhook is the only settlement path there is.
		gw = &gateway.StubGateway{}
		slog.Warn("payments: STUB GATEWAY ACTIVE — no real money will move, and no provider webhook can be verified " +
			"(POST /v1/payments/webhook answers 503 because there is no provider adapter). Intents settle through " +
			"VerifyIntent instead, which is permitted ONLY in this mode. " +
			"Set RAZORPAY_KEY_ID + RAZORPAY_KEY_SECRET + RAZORPAY_WEBHOOK_SECRET in production and remove PAYMENTS_ALLOW_STUB.")
	}

	// N3: the money path gets the RECOVERABLE provider port, not just the
	// legacy gateway, so an ambiguous CreateOrder timeout has
	// FetchByIdempotencyKey recovery available to it.
	// The stub-settlement exception is wired from THIS service's own resolved
	// mode, not from any caller's claim: ModeStub is selected only when there
	// are no Razorpay credentials, and ENV=prod refuses it outright above.
	svc := service.New(store, gw).WithProvider(provider).
		WithStubSettlement(cfg.Mode == config.ModeStub)

	// A2: build the caller allowlist. Each calling service has its OWN
	// public key and its OWN permitted operations and reference types, so
	// food-service cannot act on a commerce order and vice versa. There is
	// no shared secret and no "trusted because it is inside the cluster".
	//
	// SERVICE_CALLERS may be absent on a deployment whose callers still use
	// the internal key (the dev stack today); the handler then admits the
	// /internal family on the key alone and RegisterRoutes refuses to serve
	// if neither credential exists.
	var verifier *servicetoken.Verifier
	if strings.TrimSpace(os.Getenv("SERVICE_CALLERS")) != "" {
		verifier, err = buildServiceTokenVerifier()
		if err != nil {
			slog.Error("payments: service-token configuration is invalid", "error", err)
			os.Exit(1)
		}
	}

	handler := nethttp.New(svc).WithProvider(provider)
	if verifier != nil {
		handler.WithServiceAuth(verifier)
	}
	// Audit P-internal: the user-facing /v1/payments/* family is gated by
	// the shared internal-service-key (sibling services forward it with
	// X-User-Id), and the same key is the LEGACY credential on
	// /v1/payments/internal/* for callers not yet issuing service tokens.
	// /webhook is registered outside both gates inside RegisterRoutes
	// (audit P5).
	switch {
	case cfg.InternalKey != "" && verifier == nil:
		handler.WithInternalKey(cfg.InternalKey)
		level := slog.LevelWarn
		if isProd {
			level = slog.LevelError
		}
		slog.Log(ctx, level, "payments-service: SERVICE_CALLERS not set — /v1/payments/internal accepts the shared "+
			"INTERNAL_SERVICE_KEY from any caller (Commerce P0 A2 not in force). Register callers to close this.")
	case cfg.InternalKey != "":
		handler.WithInternalKey(cfg.InternalKey)
		slog.Info("payments-service: internal-service-key gate enabled for user-facing routes; " +
			"legacy internal-key callers admitted on /internal alongside service tokens")
	default:
		slog.Warn("payments-service: INTERNAL_SERVICE_KEY not set — the user-facing /v1/payments family is closed " +
			"and only service-token callers can reach /v1/payments/internal.")
	}

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

	// LB-7 / R-2: every domain event leaves through the transactional
	// outbox, with RequireAll acks. Every payment.* event is INSERTed into
	// payments.outbox_events by the service (same DB as the intent row) and
	// drained here with at-least-once delivery. Writing to Kafka directly
	// after a commit meant a broker outage lost a captured payment
	// permanently and left the order unpaid forever.
	outboxPublisher := outbox.New(dbPool, outbox.Config{
		DBSchema:     "payments",
		KafkaBrokers: kafkaBrokers,
		DefaultTopic: "social.events.v1",
	})
	go outboxPublisher.Run(workerCtx)
	slog.Info("payments: outbox publisher started (RequireAll acks)", "topic", "social.events.v1")

	// A6 / LB-8: refunds are durable commands. This worker is what actually
	// contacts the provider, using the command's deterministic idempotency
	// key so a retry after an ambiguous timeout yields one refund.
	go svc.RunRefundWorker(workerCtx, 15*time.Second)

	// LB-9: resolve payments the webhook never told us about, and surface
	// ledger-vs-provider drift instead of letting it accumulate silently.
	// With the stub gateway there is no provider to ask, so it stays off.
	if provider != nil {
		go svc.RunReconciler(workerCtx,
			time.Duration(envInt("PAYMENTS_RECONCILE_INTERVAL_SEC", 60))*time.Second,
			time.Duration(envInt("PAYMENTS_PENDING_AGE_SEC", 600))*time.Second)
	}

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
