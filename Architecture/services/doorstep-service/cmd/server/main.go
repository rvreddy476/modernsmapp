// Command doorstep-service runs Doorstep (home services, Urban Company
// style): a fixed-price catalogue booked into a time slot, delivered by gig
// professionals. Plan: the "Doorstep" section of the programme plan; contract:
// contracts/doorstep/openapi.yaml and asyncapi.yaml.
//
// A1: catalogue, zones/serviceability, quotes and the admin-internal catalogue
// and config CRUD. A2: professional onboarding and verification (/pro, the
// admin professional and document review, service_professional roles). A3:
// addresses, calendar-derived slots, holds, bookings, payments (confirmed only
// from the signed payments-service event), refunds, cancel and reschedule, and
// the admin booking pages. A4: offers and dispatch (accept/decline, the
// no-show and not-on-duty workers), presence (duty, location, Redis GEO)
// and realtime (Redis Streams + scoped tokens). B1 (4 Oct 2026):
// professionals price their own services (admin-approved), the customer
// picks the professional (scheduled or same-day ASAP), and a booking whose
// professional is gone waits in pro_unavailable for the customer's choice.
// A5 adds the visit.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/database"
	"github.com/atpost/doorstep-service/internal/bgv"
	"github.com/atpost/doorstep-service/internal/config"
	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/atpost/doorstep-service/internal/digilocker"
	"github.com/atpost/doorstep-service/internal/facecompare"
	doorstephttp "github.com/atpost/doorstep-service/internal/http"
	"github.com/atpost/doorstep-service/internal/mediaclient"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/presence"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/slotcache"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/identityroles"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/outbox"
	"github.com/atpost/shared/realtime"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/shared/transport"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// The pgx store is the service's store.
var (
	_ service.Store         = (*store.Store)(nil)
	_ service.ProStore      = (*store.Store)(nil)
	_ service.BookingStore  = (*store.Store)(nil)
	_ service.DispatchStore = (*store.Store)(nil)
	_ service.PricingStore  = (*store.Store)(nil)
	_ payments.Applier      = (*store.Store)(nil)
)

func main() {
	logging.Init(logging.Config{ServiceName: "doorstep-service"})

	// Fail closed before touching any dependency: a production process with
	// no internal key serves forged identities; without the platform GSTIN
	// it cannot price the s.9(5) categories; the dev seed never runs there.
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}
	if err := doorstephttp.CheckInternalKey(cfg.Production, cfg.InternalKey); err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}
	if cfg.InternalKey == "" {
		slog.Warn("INTERNAL_SERVICE_KEY is empty; /v1/doorstep accepts forged X-User-Id from anything that can reach this port (allowed outside production only)")
	}
	// Admin console: SERVICE_CALLERS=admin-service plus
	// SERVICE_CALLER_ADMIN_SERVICE_{KID,PUBKEY,OPS}. Unset: no token is
	// accepted and /v1/doorstep/internal/admin answers 401. A named caller
	// with a missing key or empty ops refuses to start.
	serviceVerifier, err := doorstephttp.ServiceCallersFromEnv(os.Getenv)
	if err != nil {
		slog.Error("refusing to start: SERVICE_CALLERS", "error", err)
		os.Exit(1)
	}
	taxComputer, err := tax.NewGST(nil, cfg.PlatformGSTIN)
	if err != nil {
		slog.Error("refusing to start", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	poolCfg, err := pgxpool.ParseConfig(cfg.PostgresDSN)
	if err != nil {
		slog.Error("parse db config", "error", err)
		os.Exit(1)
	}
	// Every statement names the doorstep schema; the path only has to reach
	// the PostGIS types and schema_migrations in public. Pinning it keeps
	// migrationrunner's bookkeeping in public.schema_migrations whatever
	// search_path the DSN carries.
	poolCfg.ConnConfig.RuntimeParams["search_path"] = "public"
	poolCfg.MaxConns = 20
	poolCfg.MinConns = 2
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

	if err := database.BootstrapSchema(ctx, dbPool); err != nil {
		slog.Error("failed to bootstrap doorstep schema", "error", err)
		os.Exit(1)
	}
	slog.Info("doorstep schema ready")

	if cfg.DevSeed {
		if err := devseed.Run(ctx, dbPool, os.Getenv); err != nil {
			slog.Error("dev seed failed", "error", err)
			os.Exit(1)
		}
		cats, svcs, opts, addons, rates := devseed.Counts()
		slog.Info("doorstep dev seed applied (Hyderabad)", "categories", cats, "services", svcs, "options", opts, "addons", addons, "rate_cards", rates)
	}

	httpMetrics := metrics.NewHTTPMetrics("doorstep-service")
	dbMetrics := metrics.NewDBPoolMetrics("doorstep-service", "postgres")
	bgCtx, bgCancel := context.WithCancel(ctx)
	defer bgCancel()
	go collectDBPoolStats(bgCtx, dbPool, dbMetrics)

	checker := health.New("doorstep-service")
	checker.Register("postgres", health.PingCheck(dbPool))

	// Durable outbox: every doorstep.events message is enqueued in the same
	// transaction as its state change (doorstep.outbox_events, schema-local
	// because public.outbox_events belongs to another service) and drained
	// here. A2 enqueues doorstep.pro.* (store.enqueueProEvent); A3+ add the rest.
	outboxPublisher := outbox.New(dbPool, outbox.Config{
		DBSchema:     "doorstep",
		KafkaBrokers: strings.Join(cfg.KafkaBrokers, ","),
		DefaultTopic: cfg.KafkaTopic,
	})
	go outboxPublisher.Run(bgCtx)
	slog.Info("outbox publisher started", "topic", cfg.KafkaTopic)

	// Identity role worker (the food-service wiring). service_professional
	// intents commit with the professional row in doorstep.
	// identity_role_intents and are drained here into identity-auth's
	// internal role API. The default host is identity-auth:8081 (compose).
	roleOutbox := identityroles.NewOutbox("doorstep", "doorstep-service")
	roleWorker := identityroles.NewWorker(
		identityroles.NewClient(cfg.IdentityAuthURL, cfg.InternalKey, "doorstep-service"),
		roleOutbox, dbPool, slog.Default(), identityroles.WorkerConfig{},
	)
	go roleWorker.Run(bgCtx)
	slog.Info("identity role worker started", "identity_auth_url", cfg.IdentityAuthURL)
	if cfg.InternalKey == "" {
		slog.Warn("INTERNAL_SERVICE_KEY is empty; identity role grants will be rejected")
	}
	pgStore := store.New(dbPool).WithRoleIntents(roleOutbox)

	proDeps, err := professionalDeps(ctx, cfg, pgStore)
	if err != nil {
		slog.Error("refusing to start: professional onboarding", "error", err)
		os.Exit(1)
	}
	slog.Info("doorstep-service: professional onboarding",
		"digilocker", cfg.DigiLockerMode, "face_compare", cfg.FaceCompareMode,
		"background_check", proDeps.BGV.Name(), "pii_sealing", proDeps.PII.Configured())

	// B1: professionals price their own work; only admin-approved prices
	// are listed, quoted or booked.
	svc := service.New(pgStore, taxComputer, cfg.QuoteTTL).WithPro(proDeps).WithPricing(pgStore)
	slog.Info("doorstep-service: pricing", "gst_computed", taxComputer.Computed(), "quote_ttl", cfg.QuoteTTL)

	// Bookings and payments (A3). The payments client is doorstep's own
	// Ed25519 token (application doorstep, reference doorstep_booking); in
	// local/dev without one the shared internal key stands in (payments'
	// legacy path). Without either the payment routes answer 503.
	bookingDeps := service.BookingDeps{Store: pgStore, PII: proDeps.PII, DevStubPayments: !cfg.Production}
	payClient, err := payments.NewClient(payments.Config{BaseURL: cfg.PaymentsServiceURL, TokenKey: cfg.ServiceTokenKey,
		TokenKID: cfg.ServiceTokenKID, InternalKey: cfg.InternalKey, LegacyAllowed: !cfg.Production})
	switch {
	case errors.Is(err, payments.ErrNotConfigured):
		slog.Warn("doorstep-service: payments not configured (no DOORSTEP_SERVICE_TOKEN_KEY, no dev internal key); booking and payment routes answer 503")
	case err != nil:
		slog.Error("refusing to start: payments client", "error", err)
		os.Exit(1)
	default:
		bookingDeps.Payments = payClient
		slog.Info("doorstep-service: payments client ready", "legacy_internal_key", payClient.LegacyAuth())
	}
	// The same identity bound to doorstep_extras (B1: a change-of-professional
	// difference). Without it a dearer change answers 503.
	if extrasClient, err := payments.NewExtrasClient(payments.Config{BaseURL: cfg.PaymentsServiceURL, TokenKey: cfg.ServiceTokenKey,
		TokenKID: cfg.ServiceTokenKID, InternalKey: cfg.InternalKey, LegacyAllowed: !cfg.Production}); err == nil {
		bookingDeps.ExtrasPayments = extrasClient
	} else if !errors.Is(err, payments.ErrNotConfigured) {
		slog.Error("refusing to start: payments extras client", "error", err)
		os.Exit(1)
	}
	// Slot answers are cached ≤30 s in Redis; Redis down only means no cache.
	// The same client carries realtime (Redis Streams) and presence (GEO).
	var rdb *redis.Client
	if c, err := transport.NewRedisClientFromEnv(cfg.RedisAddr); err != nil {
		slog.Warn("doorstep-service: redis client not configured; slot answers are not cached, realtime and presence are off", "error", err)
	} else {
		rdb = c
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if err := rdb.Ping(pingCtx).Err(); err != nil {
			slog.Warn("doorstep-service: redis ping failed; the slot cache, realtime and presence retry per call", "error", err)
		}
		cancel()
		defer rdb.Close()
		bookingDeps.Cache = slotcache.New(rdb)
	}
	svc = svc.WithBookings(bookingDeps)

	// Dispatch, presence and realtime (A4). Live frames go onto Redis
	// Streams (shared/realtime StreamPublisher; notification-service's SSE
	// gateway reads them), tokens are signed with REALTIME_TOKEN_SECRET
	// (outside production it falls back to the internal key; config refuses
	// production without it). Without Redis or a secret the token routes
	// answer 503 and nothing is published; dispatch itself never needs Redis.
	dispatchDeps := service.DispatchDeps{Store: pgStore}
	switch {
	case rdb == nil:
		slog.Warn("doorstep-service: realtime DISABLED — no Redis client; POST /realtime/token and /pro/realtime/token answer 503")
	case cfg.RealtimeTokenSecret == "":
		slog.Warn("doorstep-service: realtime DISABLED — neither REALTIME_TOKEN_SECRET nor INTERNAL_SERVICE_KEY is set")
	default:
		dispatchDeps.Realtime = realtime.NewStreamPublisher(rdb)
		dispatchDeps.Signer = realtime.NewTokenSigner([]byte(cfg.RealtimeTokenSecret)).WithTTL(service.RealtimeTokenTTL)
		slog.Info("doorstep-service: realtime wired (Redis Streams)")
	}
	if p := presence.New(rdb); p != nil {
		dispatchDeps.Presence = p
	}
	svc = svc.WithDispatch(dispatchDeps)
	if bookingDeps.DevStubPayments {
		slog.Warn("doorstep-service: POST /bookings/{id}/payment/stub-confirm is enabled (development; refused when payments-service has a real provider)")
	}

	// A booking is confirmed ONLY here, from the signed payments-service
	// event (application doorstep, reference doorstep_booking), applied once
	// through doorstep.payment_inbox in the booking's transaction.
	paymentConsumer := payments.NewConsumer(pgStore, cfg.KafkaBrokers, metrics.NewKafkaConsumerMetrics("doorstep-service"), svc.AfterPaymentEvent)
	defer paymentConsumer.Close()
	go paymentConsumer.Start(bgCtx)
	slog.Info("doorstep payment consumer started", "group", payments.ConsumerGroup, "topic", payments.Topic)
	// Hold sweeper (expire lapsed holds) and refund resubmission.
	go svc.RunWorkers(bgCtx, 30*time.Second)
	// Dispatch: expired offers, T-2 h alerts, T-45 cancels, not-on-duty and
	// no-show reassignment, rescues, retries, stale GPS.
	go svc.RunDispatchWorkers(bgCtx, 15*time.Second)
	if serviceVerifier == nil {
		slog.Warn("doorstep-service: SERVICE_CALLERS not set — admin-service tokens are refused; /v1/doorstep/internal/admin answers 401")
	} else {
		slog.Info("doorstep-service: service-token callers registered", "callers", serviceVerifier.Callers())
	}
	handler := doorstephttp.New(svc, cfg.InternalKey).WithServiceAuth(serviceVerifier)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))
	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	handler.RegisterRoutes(r)

	if err := server.Run(r, server.Config{
		Port:            cfg.HTTPPort,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			bgCancel()
			dbPool.Close()
			slog.Info("cleanup completed")
		},
	}); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

// professionalDeps builds onboarding's providers. Each mock is refused in
// production here as well as in config: DigiLocker (it verifies every
// Aadhaar and lets anyone pick a gender), the face compare (it passes every
// selfie) and the background check (it clears everyone); the background
// "provider" mode is refused everywhere until a vendor exists.
func professionalDeps(ctx context.Context, cfg config.Config, st *store.Store) (service.ProDeps, error) {
	crypto, err := propii.New(ctx, cfg.PIIKeys)
	if err != nil {
		return service.ProDeps{}, err
	}
	if crypto == nil {
		slog.Warn("DOORSTEP_PII_KEYS unset (development): DigiLocker, bank, PAN and certificate-number routes answer 503")
	}
	redirect := cfg.DigiLockerRedirectURI()
	dlClient, err := digilocker.New(cfg.DigiLockerMode, cfg.Production, digilocker.HTTPConfig{
		TokenURL: cfg.DigiLockerTokenURL, ClientID: cfg.DigiLockerClientID, ClientSecret: cfg.DigiLockerClientSecret, RedirectURI: redirect,
	})
	if err != nil {
		return service.ProDeps{}, err
	}
	faces, err := facecompare.New(cfg.FaceCompareMode, cfg.Production, cfg.MediaServiceURL, cfg.InternalKey)
	if err != nil {
		return service.ProDeps{}, err
	}
	provider, err := bgv.New(cfg.BackgroundCheckMode, cfg.Production)
	if err != nil {
		return service.ProDeps{}, err
	}
	media := mediaclient.New(cfg.MediaServiceURL, cfg.InternalKey)
	if media == nil {
		return service.ProDeps{}, fmt.Errorf("MEDIA_SERVICE_URL is required: uploads are verified with media-service")
	}
	// Document images for the admin console: doorstep's own Ed25519 identity
	// (the payments token key) signs media-service's image-bytes token.
	// Without a key (local stack) media-service accepts the internal key.
	if cfg.ServiceTokenKey != "" {
		signer, err := servicetoken.NewSignerFromBase64(config.ServiceTokenIssuer, cfg.ServiceTokenKID, cfg.ServiceTokenKey)
		if err != nil {
			return service.ProDeps{}, err
		}
		media.WithImageToken(func() (string, error) {
			return signer.Mint(mediaclient.ImageBytesAudience, config.ServiceTokenIssuer, []string{mediaclient.ImageBytesOperation}, nil, time.Minute)
		})
	}
	return service.ProDeps{
		Store: st,
		DigiLocker: digilocker.Settings{Mode: cfg.DigiLockerMode, Client: dlClient, AuthorizeURL: cfg.DigiLockerAuthorizeURL,
			ClientID: cfg.DigiLockerClientID, RedirectURI: redirect},
		Faces:               faces,
		SelfieMinSimilarity: float64(cfg.SelfieMinSimilarity),
		Media:               media,
		Images:              media,
		PII:                 crypto,
		BGV:                 provider,
	}, nil
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
