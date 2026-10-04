// Command doorstep-service runs Doorstep (home services, Urban Company
// style): a fixed-price catalogue booked into a time slot, delivered by gig
// professionals. Plan: the "Doorstep" section of the programme plan; contract:
// contracts/doorstep/openapi.yaml and asyncapi.yaml.
//
// A1: catalogue, zones/serviceability, quotes and the admin-internal catalogue
// and config CRUD. A2: professional onboarding and verification (/pro, the
// admin professional and document review, service_professional roles). Later
// lanes add bookings and payments (A3), dispatch and realtime (A4), the visit
// (A5).
package main

import (
	"context"
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
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/identityroles"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/atpost/shared/outbox"
	"github.com/atpost/shared/server"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The pgx store is the service's store.
var (
	_ service.Store    = (*store.Store)(nil)
	_ service.ProStore = (*store.Store)(nil)
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

	svc := service.New(pgStore, taxComputer, cfg.QuoteTTL).WithPro(proDeps)
	slog.Info("doorstep-service: pricing", "gst_computed", taxComputer.Computed(), "quote_ttl", cfg.QuoteTTL)
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
