package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/commerce-service/database"
	"github.com/atpost/commerce-service/internal/consumers"
	"github.com/atpost/commerce-service/internal/courier"
	commercehttp "github.com/atpost/commerce-service/internal/http"
	"github.com/atpost/commerce-service/internal/identity"
	"github.com/atpost/commerce-service/internal/kmsclient"
	"github.com/atpost/commerce-service/internal/kyc"
	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/obs"
	"github.com/atpost/commerce-service/internal/payments"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/blob"
	pgstore "github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/commerce-service/internal/workers"
	"github.com/atpost/shared/counters"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	tracepkg "github.com/atpost/shared/o11y/trace"
	"github.com/atpost/shared/outbox"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. Structured logging
	logging.Init(logging.Config{ServiceName: "commerce-service"})

	// 1a. Tracing (Phase F3.5) — OTLP/gRPC exporter to Jaeger. Falls
	// back to a no-op provider if the collector is unreachable so the
	// service still boots in environments without observability infra.
	tracerProvider, _ := tracepkg.InitTracer("commerce-service", env("OTEL_EXPORTER_OTLP_ENDPOINT", "http://jaeger:4317"))
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tracerProvider.Shutdown(shutdownCtx)
	}()

	// 2. Config
	port := env("HTTP_PORT", "8109")
	pgDSN := os.Getenv("POSTGRES_DSN")
	redisAddr := os.Getenv("REDIS_ADDR")
	kafkaBrokers := strings.Split(env("KAFKA_BROKERS", "redpanda:9092"), ",")
	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")

	// 3. Database
	ctx := context.Background()
	poolCfg, err := pgxpool.ParseConfig(pgDSN)
	if err != nil {
		slog.Error("parse db config", "error", err)
		os.Exit(1)
	}
	// HS1: pool sizing is env-tunable so prod can scale beyond the
	// 25/5 dev defaults without a code change. Tracks the same pattern
	// notification-service uses; both services contend on app_db.
	poolCfg.MaxConns = int32(envInt("POSTGRES_MAX_CONNS", 25))
	poolCfg.MinConns = int32(envInt("POSTGRES_MIN_CONNS", 5))
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

	// 4. Redis
	rdb, err := transport.NewRedisClientFromEnv(redisAddr)
	if err != nil {
		slog.Error("failed to configure redis client", "error", err)
		os.Exit(1)
	}
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping failed", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()
	slog.Info("connected to redis")

	// 5. Kafka dialer
	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

	// 6. Bootstrap schema + run migrations
	if err := pgstore.BootstrapSchema(ctx, dbPool, database.SetupSQL, database.Migrations); err != nil {
		slog.Error("failed to bootstrap commerce schema", "error", err)
		os.Exit(1)
	}
	slog.Info("commerce schema ready")

	// 7. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("commerce-service")
	// §13 — the commerce money-integrity instrument set. Four of these page.
	commerceMetrics := obs.New()
	dbMetrics := metrics.NewDBPoolMetrics("commerce-service", "postgres")
	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 8. Health checker
	checker := health.New("commerce-service")
	checker.Register("postgres", health.PingCheck(dbPool))
	checker.Register("redis", health.RedisPingCheck(func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}))

	// 9. Service (+ courier + invoice blob store)
	store := pgstore.New(dbPool)
	svc := service.NewWithDialer(store, rdb, strings.Join(kafkaBrokers, ","), kafkaDialer)
	defer svc.Close()

	// Courier provider (stub in dev, shiprocket in prod). Env COURIER_PROVIDER selects.
	svc.WithCourier(courier.New())

	// Cross-service plumbing for the affiliate-redirect resolver. The
	// /v1/commerce/affiliate/:linkId endpoint calls monetization-service
	// via the internal-key channel; both env vars are wired here so a
	// missing one degrades gracefully (handler returns 503).
	svc.WithMonetizationServiceURL(env("MONETIZATION_SERVICE_URL", "http://monetization-service:8099"))
	svc.WithInternalServiceKey(env("INTERNAL_SERVICE_KEY", ""))

	// MinIO for invoice HTML storage.
	minioEndpoint := env("MINIO_ENDPOINT", "minio:9000")
	minioAccess := env("MINIO_ACCESS_KEY", "minioadmin")
	minioSecret := env("MINIO_SECRET_KEY", "minioadmin")
	minioBucket := env("COMMERCE_INVOICE_BUCKET", "commerce-invoices")
	minioSSL := env("MINIO_USE_SSL", "false") == "true"
	minioPublic := os.Getenv("MINIO_PUBLIC_ENDPOINT")
	if blobStore, err := blob.New(minioEndpoint, minioAccess, minioSecret, minioBucket, minioSSL, minioPublic); err != nil {
		slog.Warn("invoice blob store unavailable; invoices will fail until fixed", "error", err)
	} else {
		svc.WithBlob(blobStore)
		slog.Info("invoice blob store ready", "bucket", minioBucket)
	}

	// Auth-service client for resolving buyer email in commerce events.
	authURL := env("AUTH_SERVICE_URL", "http://auth-service:8081")
	svc.WithIdentity(identity.New(authURL, internalKey))
	slog.Info("identity client ready", "auth_url", authURL)

	// Media-service client, used to verify that a media id a client supplies
	// actually belongs to that client.
	//
	// Twelve columns in this service store a media id that arrived in a
	// request body, and none of them was checked: a product's primary image, a
	// seller's logo and banner, and every row of seller_documents — which is
	// where a seller's PAN, Aadhaar and cancelled cheque live. Without this
	// client a seller can point their KYC at somebody else's identity document
	// and be approved on it.
	//
	// Same closed environment list as the KMS and gateway-trust decisions, and
	// the same posture: a deployed environment that cannot verify media does
	// not start.
	mediaURL := env("MEDIA_SERVICE_URL", "http://media-service:8087")
	mediaClient := media.New(mediaURL, internalKey)
	switch classifyPIIEnvironment(os.Getenv("ENV")) {
	case piiEnvManaged:
		if mediaClient == nil {
			slog.Error("commerce: MEDIA_SERVICE_URL is required in a deployed environment — " +
				"without it any seller can attach another person's identity documents to their own KYC")
			os.Exit(1)
		}
		svc.WithMedia(mediaClient)
		slog.Info("media ownership verification enabled", "media_url", mediaURL)
	default:
		// Local: an absent media-service must not stop the product flows, but
		// the gap is stated rather than implied. classifyPIIEnvironment has
		// already rejected an unrecognised ENV further down.
		if mediaClient != nil {
			svc.WithMedia(mediaClient)
			slog.Info("media ownership verification enabled", "media_url", mediaURL)
		} else {
			slog.Warn("commerce: MEDIA_SERVICE_URL is not set — media ownership is NOT verified; " +
				"any caller may attach any media id, including another person's KYC documents")
		}
	}

	// A2 — the payments client authenticates with commerce's OWN Ed25519
	// key, not the cluster-wide internal-service key the gateway injects
	// into every proxied request. That header was the only thing standing
	// between an end user and payments; it is no longer used here, and the
	// gateway no longer proxies /v1/payments at all (LB-1).
	//
	// Every call lands on payments' /v1/payments/internal family. A
	// deployment that has not issued commerce a signing key yet (the dev
	// stack) falls back to the shared internal key on the SAME routes —
	// payments admits that only while its own legacy fallback is on — and
	// a deployed environment refuses to start that way.
	paymentsURL := env("PAYMENTS_SERVICE_URL", "http://payments-service:8102")
	var pmClient *payments.Client
	if signingKey := os.Getenv("COMMERCE_SERVICE_TOKEN_KEY"); signingKey != "" {
		pmClient, err = payments.NewP0Client(paymentsURL, env("COMMERCE_SERVICE_TOKEN_KID", ""), signingKey)
		if err != nil {
			slog.Error("commerce: payments service-token client could not be built", "error", err)
			os.Exit(1)
		}
		slog.Info("payments client ready (service-token auth)", "payments_url", paymentsURL)
	} else {
		if classifyPIIEnvironment(os.Getenv("ENV")) != piiEnvLocal {
			slog.Error("commerce: COMMERCE_SERVICE_TOKEN_KEY is required in a deployed environment — " +
				"the shared internal key is not a service identity (Commerce P0 A2)")
			os.Exit(1)
		}
		pmClient, err = payments.NewInternalKeyClient(paymentsURL, internalKey)
		if err != nil {
			slog.Error("commerce: payments legacy client could not be built", "error", err)
			os.Exit(1)
		}
		slog.Warn("commerce: COMMERCE_SERVICE_TOKEN_KEY not set — payments calls carry the shared internal key "+
			"(legacy mode, local only). Issue a signing key and register it in payments' SERVICE_CALLERS.",
			"payments_url", paymentsURL)
	}
	svc.WithPayments(pmClient)

	// LB-24 / B2 — address PII encryption.
	//
	// Production AND staging require KMS through IRSA; the static provider
	// exists only for local development, because a data key baked into a
	// process is not a key-management story. buildPIICipher proves both
	// scopes can seal and open before this returns, so a service that starts
	// is a service whose address writes will work.
	piiCipher, err := buildPIICipher(ctx, store)
	if err != nil {
		slog.Error("commerce: PII cipher configuration is invalid", "error", err)
		os.Exit(1)
	}
	svc.WithPII(piiCipher)

	// B4/B5 — which half of the two-deploy PII cutover this image is running.
	//
	// "dual" writes both copies and can read either, so it is safe against a
	// database whose backfill has not finished and safe to roll back from.
	// "ciphertext" is deployed only once the backfill proves every row has
	// ciphertext; from then on a row without it is an error rather than a
	// silent fallback. An unrecognised value refuses to start.
	cutover, err := pii.ParseMode(os.Getenv("COMMERCE_PII_CUTOVER"))
	if err != nil {
		slog.Error("commerce: PII cutover mode is invalid", "error", err)
		os.Exit(1)
	}
	svc.WithPIICutover(cutover)
	slog.Info("address PII cipher ready", "cutover", cutover.String())

	// Stub gateway opt-in for ConfirmPayment(gateway="stub"). Must match
	// payments-service's PAYMENTS_ALLOW_STUB — docker-compose sets both;
	// production leaves it unset so a client can never name the stub.
	allowStub := env("PAYMENTS_ALLOW_STUB", "") == "true"
	svc.WithAllowStubGateway(allowStub)
	if allowStub {
		slog.Warn("commerce: PAYMENTS_ALLOW_STUB=true — ConfirmPayment accepts gateway=stub. Never enable in production.")
	}

	// KYC validator (Phase 3.2). The stub does format-only checks and tags
	// every verdict with Source="stub" so admins know they're approving on
	// incomplete verification. Wire a vendor (Karza/Signzy/Hyperverge)
	// adapter here once the commercial integration is signed.
	svc.WithKYC(kyc.StubValidator{})
	slog.Info("kyc validator ready", "adapter", "stub")

	// Payout fee schedule (Phase 4.1) — env-overridable so finance can
	// change commission / platform fee / TDS without a code change. Bad
	// values are clamped to the historical defaults inside WithPayoutConfig.
	svc.WithPayoutConfig(service.PayoutConfig{
		CommissionPct:  envFloat("COMMERCE_COMMISSION_PCT", 5.0),
		PlatformFeePct: envFloat("COMMERCE_PLATFORM_FEE_PCT", 2.0),
		TDSPct:         envFloat("COMMERCE_TDS_PCT", 1.0),
	})
	slog.Info("payout config ready",
		"commission_pct", env("COMMERCE_COMMISSION_PCT", "5.0"),
		"platform_fee_pct", env("COMMERCE_PLATFORM_FEE_PCT", "2.0"),
		"tds_pct", env("COMMERCE_TDS_PCT", "1.0"))

	// 10. Kafka consumer: react to payment lifecycle events from payments-service.
	// Confirms orders on payment.succeeded; releases stock reservations on
	// payment.failed. Started in a goroutine; cancelled via consumerCtx on shutdown.
	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer consumerCancel()
	kafkaMetrics := metrics.NewKafkaConsumerMetrics("commerce-service")

	// A3 / R-1 — the durable consumer replaces the one that treated a Redis
	// SETNX as a transaction log. The dedupe row, the amount/currency/payer
	// verification and the order effect now commit in ONE PostgreSQL
	// transaction, so a crash between "seen" and "applied" is impossible.
	paymentsConsumer := consumers.NewP0PaymentsConsumer(
		store, kafkaBrokers, rdb, kafkaMetrics, commerceMetrics)
	go paymentsConsumer.Start(consumerCtx)
	slog.Info("payments consumer started (durable inbox, full-tuple verification)")

	// LB-8 — durable refund delivery. The cancel path no longer calls
	// payments inline and swallows the error; it writes a command, and this
	// worker owns getting it delivered.
	go svc.RunRefundWorker(consumerCtx, 20*time.Second)

	// LB-22 / M-5 — terminal reservation expiry. The old sweeper released
	// the hold and left the order payment_pending, so a late capture could
	// still pay for stock that had already been sold to someone else.
	go svc.RunReservationExpiry(consumerCtx, time.Minute)

	// Phase 6.1 — durable fulfillment worker. Replaces the old
	// `go s.fulfillPaidOrder()` goroutines that disappeared on restart.
	fulfillmentWorker := workers.NewFulfillmentWorker(store, svc)
	go fulfillmentWorker.Run(consumerCtx)
	slog.Info("fulfillment worker started")

	// HP1 — outbox publisher. Replaces synchronous kafka.Writer.WriteMessages
	// on the request path. service.publish() now inserts into
	// outbox_events; this worker drains that table to Kafka with retries
	// + at-least-once delivery. Polling cadence is intentionally tight
	// (500 ms default) so user-facing latency on emits stays sub-second.
	outboxPublisher := outbox.New(dbPool, outbox.Config{
		KafkaBrokers: strings.Join(kafkaBrokers, ","),
		DefaultTopic: "social.events.v1",
	})
	go outboxPublisher.Run(consumerCtx)
	slog.Info("commerce outbox publisher started", "topic", "social.events.v1")

	// The old `runInventoryExpiry` sweeper is GONE — replaced by
	// svc.RunReservationExpiry above (LB-22 / M-5).
	//
	// It released the reservation and left the order `payment_pending`, so a
	// capture arriving afterwards still applied, with its stock errors
	// merely logged: A's hold expires, B buys the last unit, A's delayed
	// capture lands, and two orders exist against one unit with A charged.
	// Expiry is now terminal, and a late capture becomes an automatic refund
	// rather than a stockless fulfilment.

	// §13 gauges. Sampled rather than computed per request: these are
	// "is anything stuck" signals, and the queries behind them are aggregate
	// scans that have no business running on a checkout.
	go sampleIntegrityGauges(consumerCtx, store, commerceMetrics)

	// Sharded product-view counter flush. At trending-product scale
	// (100k+ views/hour) every UPDATE products SET view_count=… on the
	// same row was a contention point; the counter spreads across 32
	// Redis shards and materialises back to PG every ~10s.
	if pvc := svc.ProductViewCounter(); pvc != nil {
		flush := func(ctx context.Context, productIDStr string, total int64) error {
			id, err := uuid.Parse(productIDStr)
			if err != nil {
				return err
			}
			return store.SetProductViewCount(ctx, id, total)
		}
		go counters.NewWorker(pvc, flush, counters.WorkerOptions{}).Start(consumerCtx)
		slog.Info("commerce product view-count sharded flush worker started")
	}

	// 11. HTTP handler
	handler := commercehttp.New(svc).WithInternalKey(internalKey)

	// 11. Gin engine
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	// Phase F3 — tracing middleware runs FIRST so the span context is
	// available to RequestID + Logger for correlation. Order matters:
	// otel → request-id → logger so logs carry trace_id + span_id.
	r.Use(middleware.OtelTracing("commerce-service"))
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	// §4 / A5 / LB-11 — the fence, as a default-deny middleware ahead of
	// routing. Registering it here rather than relying on "we did not add
	// the route" means a later edit that re-adds one is still refused, and
	// the same list is exported so the reachability proof enumerates what
	// the server actually enforces instead of a copy that can drift.
	r.Use(commercehttp.FenceMiddleware())

	// Every authenticated route in this service reads the caller's identity
	// from the X-User-Id header. Nothing verified that header, so anything
	// that could open a TCP connection to this pod could name any user and be
	// believed — a neighbouring pod, a port-forward, a second ingress. The
	// gateway strips and re-derives X-User-Id correctly; it is simply not the
	// only thing that can reach here.
	//
	// The gateway already injects X-Internal-Service-Key on every proxied
	// request, so possession of it is proof the request came through the edge.
	//
	// The environment classifier is the SAME closed list the KMS decision
	// uses. Two lists would eventually disagree, and the disagreement would
	// be an environment that handles real customer data with one protection
	// installed and the other not.
	switch classifyPIIEnvironment(os.Getenv("ENV")) {
	case piiEnvManaged:
		if internalKey == "" {
			// Fail to start. Running without it would serve every user's
			// cart, orders and addresses to anything inside the network,
			// which is not a degraded mode worth having.
			slog.Error("commerce: INTERNAL_SERVICE_KEY is required in a deployed environment — " +
				"without it X-User-Id is unauthenticated and any in-cluster caller can act as any user")
			os.Exit(1)
		}
		r.Use(commercehttp.RequireGatewayTrust(internalKey))
		slog.Info("commerce: /v1/commerce requires the gateway's internal service key")
	case piiEnvLocal:
		if internalKey != "" {
			r.Use(commercehttp.RequireGatewayTrust(internalKey))
			slog.Info("commerce: /v1/commerce requires the gateway's internal service key")
		} else {
			// Said in as many words. A quiet "disabled" is how a protection
			// gets assumed present for a year.
			slog.Warn("commerce: INTERNAL_SERVICE_KEY is not set — X-User-Id is UNAUTHENTICATED " +
				"and any caller that can reach this process may act as any user. " +
				"Acceptable on a developer machine, never in a deployed environment.")
		}
	default:
		slog.Error("commerce: ENV is not a recognised environment; refusing to guess whether " +
			"this process handles real users")
		os.Exit(1)
	}

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)
	handler.RegisterP0Routes(r)

	// 12. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			consumerCancel()
			_ = paymentsConsumer.Close()
			svc.Close()
			rdb.Close()
			dbPool.Close()
			slog.Info("commerce-service shutdown complete")
		},
	}); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

// sampleIntegrityGauges publishes the §13 "is anything stuck" gauges.
//
// Each is an aggregate scan, so it runs on a timer rather than on a request
// path. All three should sit at zero in steady state; a rising value means
// a worker has stopped doing its job, which is exactly the failure that
// used to be invisible.
func sampleIntegrityGauges(ctx context.Context, store *pgstore.Store, m *obs.Metrics) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Above twice the reservation TTL means the expiry sweeper is
			// stuck and stock is being held against nothing.
			if age, err := store.OldestLiveReservationAge(ctx); err == nil {
				m.ReservationAgeSeconds.Set(age.Seconds())
			}
			// Money we owe a customer and have not returned.
			if age, err := store.OldestUnsettledRefundAge(ctx); err == nil {
				m.RefundPendingSeconds.Set(age.Seconds())
			}
			// A rising backlog means domain events are not reaching
			// consumers — notifications, search, analytics all go quiet.
			if n, err := store.UnpublishedOutboxCount(ctx); err == nil {
				m.OutboxUnpublished.Set(float64(n))
			}
		}
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		slog.Warn("invalid float env, using fallback", "key", key, "value", raw, "fallback", fallback)
		return fallback
	}
	return v
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("invalid int env, using fallback", "key", key, "value", raw, "fallback", fallback)
		return fallback
	}
	return v
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

// ─── PII key management (B2) ─────────────────────────────────────────

// piiEnvironment classifies ENV into the two things that matter here: does
// this environment handle real customer PII, or not.
//
// B2. The previous version tested `ENV == "prod"` and treated everything else
// as development. Staging is deployed with a real CMK and real customer-shaped
// data, and that check silently gave it the static-key branch — so the one
// environment meant to prove the KMS path never exercised it.
//
// The classification is a CLOSED list on both sides. An unknown or blank ENV
// is neither production nor development: it is a misconfiguration, and it
// fails closed. Defaulting an unrecognised value to "dev" is how a typo in a
// deployment manifest ends up encrypting real addresses under a key that dies
// with the pod.
type piiEnvironment int

const (
	piiEnvUnknown piiEnvironment = iota
	piiEnvManaged                // real PII: KMS required, no fallback
	piiEnvLocal                  // development only: static keys permitted
)

// managedPIIEnvironments handle real customer data. Both require KMS.
var managedPIIEnvironments = map[string]bool{
	"prod":       true,
	"production": true,
	"staging":    true,
	"stage":      true,
}

// localPIIEnvironments are developer machines and ephemeral test rigs.
// Nothing here ever sees a real customer address.
var localPIIEnvironments = map[string]bool{
	"dev":         true,
	"development": true,
	"local":       true,
	"test":        true,
	"ci":          true,
}

func classifyPIIEnvironment(raw string) piiEnvironment {
	e := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case managedPIIEnvironments[e]:
		return piiEnvManaged
	case localPIIEnvironments[e]:
		return piiEnvLocal
	default:
		return piiEnvUnknown
	}
}

// canonicalPIIEnvironment normalises ENV into the value that goes into the KMS
// encryption context.
//
// It must be STABLE: the context is stored with each wrapped key and KMS
// verifies it byte-for-byte on Decrypt, so renaming "production" to "prod"
// after keys exist would make every one of them unopenable. The aliases fold
// into one canonical form precisely so a manifest that says "production" and
// one that says "prod" cannot produce two incompatible key populations.
func canonicalPIIEnvironment(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "prod", "production":
		return "prod"
	case "staging", "stage":
		return "staging"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

// piiReadinessTimeout bounds the whole startup probe. Long enough for a cold
// KMS call plus a first-use key mint, short enough that a hung KMS fails the
// pod rather than leaving it stuck forever.
const piiReadinessTimeout = 30 * time.Second

// buildPIICipher constructs the address-encryption cipher (LB-24 / B2).
//
// Managed environments (production AND staging) require a customer-managed
// KMS key reached through the default credential chain — IRSA in the cluster.
// There is no fallback: a data key baked into a process is not key management,
// and encrypting real addresses under a key that disappears with the pod is
// data loss dressed as uptime.
//
// The chain it builds:
//
//	kmsclient (IRSA) -> pii.KMSKeyProvider -> PostgreSQL PIIKeyRing -> pii.Cipher
//
// The ring is what makes rotation survivable: KMS cannot regenerate a data key
// from a version number, so every wrapped blob is persisted against
// (scope, version) and a value sealed under v1 still opens after v2 exists.
func buildPIICipher(ctx context.Context, store *pgstore.Store) (*pii.Cipher, error) {
	salt := os.Getenv("COMMERCE_PII_LOOKUP_SALT")
	if len(salt) < 16 {
		return nil, fmt.Errorf("COMMERCE_PII_LOOKUP_SALT must be at least 16 bytes")
	}

	rawEnv := env("ENV", "")
	switch classifyPIIEnvironment(rawEnv) {

	case piiEnvUnknown:
		// Fails closed. An environment we cannot classify might be
		// production, and guessing wrong is unrecoverable.
		return nil, fmt.Errorf(
			"ENV=%q is not a recognised environment: expected one of prod/production/staging/stage "+
				"(KMS required) or dev/development/local/test/ci (development keys). Refusing to "+
				"start rather than guess which side of the customer-PII boundary this is",
			rawEnv)

	case piiEnvManaged:
		keyID := os.Getenv("COMMERCE_KMS_KEY_ID")
		if keyID == "" {
			return nil, fmt.Errorf(
				"COMMERCE_KMS_KEY_ID is required in %s: address PII must be encrypted under a "+
					"customer-managed KMS key, and a process-local key is not key management", rawEnv)
		}
		if store == nil {
			return nil, fmt.Errorf("commerce: the PII key ring needs a database handle")
		}

		client, err := kmsclient.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("commerce: KMS client: %w", err)
		}
		provider, err := pii.NewKMSKeyProvider(
			client, pgstore.NewPIIKeyRing(store), keyID, canonicalPIIEnvironment(rawEnv))
		if err != nil {
			return nil, fmt.Errorf("commerce: KMS key provider: %w", err)
		}
		cipher, err := pii.New(provider, []byte(salt))
		if err != nil {
			return nil, err
		}

		// Prove the whole chain works BEFORE reporting ready. A service that
		// starts and then fails every address write is worse than one that
		// refuses to start: the failure surfaces at checkout, to a customer.
		if err := verifyPIIReadiness(ctx, cipher); err != nil {
			return nil, err
		}
		return cipher, nil

	default: // piiEnvLocal
		profile := os.Getenv("COMMERCE_PII_DEV_KEY_PROFILE")
		snapshot := os.Getenv("COMMERCE_PII_DEV_KEY_SNAPSHOT")
		if len(profile) != 32 || len(snapshot) != 32 {
			return nil, fmt.Errorf(
				"COMMERCE_PII_DEV_KEY_PROFILE and COMMERCE_PII_DEV_KEY_SNAPSHOT must each be exactly " +
					"32 bytes (development only; prod and staging use KMS)")
		}
		// Separate scopes so a future profile-address shred cannot destroy an
		// order snapshot that GST rules may require us to keep (review §5-D8).
		return pii.New(&pii.StaticKeyProvider{Keys: map[pii.Scope][]byte{
			pii.ScopeProfile:       []byte(profile),
			pii.ScopeOrderSnapshot: []byte(snapshot),
		}}, []byte(salt))
	}
}

// verifyPIIReadiness proves both scopes can seal and open before the service
// reports ready.
//
// It exercises the real path end to end — mint or fetch the current key
// through KMS, seal a probe value, open it again — for each scope
// independently, because they carry separate keys and a ring that has one and
// not the other is a service that works until the first order.
//
// Sealing a probe also forces the FIRST-USE path: on a fresh environment this
// is what mints the key, under the advisory lock and the ring's unique index,
// rather than leaving the first real customer write to discover KMS is
// misconfigured.
func verifyPIIReadiness(ctx context.Context, cipher *pii.Cipher) error {
	ctx, cancel := context.WithTimeout(ctx, piiReadinessTimeout)
	defer cancel()

	for _, scope := range []pii.Scope{pii.ScopeProfile, pii.ScopeOrderSnapshot} {
		// A fixed, non-identifying probe. Never a real address.
		const probe = "pii-readiness-probe"

		sealed, version, err := cipher.Seal(ctx, scope, probe)
		if err != nil {
			return fmt.Errorf("commerce: PII readiness failed sealing scope %q: %w", scope, err)
		}
		opened, err := cipher.Open(ctx, scope, sealed)
		if err != nil {
			return fmt.Errorf(
				"commerce: PII readiness failed opening scope %q at version %d: %w — "+
					"KMS can wrap but not unwrap, which means every existing ciphertext in "+
					"this scope is currently unreadable", scope, version, err)
		}
		if opened != probe {
			return fmt.Errorf(
				"commerce: PII readiness round-trip mismatch for scope %q: the cipher is not "+
					"returning what it sealed", scope)
		}
		slog.Info("pii: scope ready", "scope", scope, "key_version", version)
	}
	return nil
}
