package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/atpost/media-service/database"
	"github.com/atpost/media-service/internal/config"
	"github.com/atpost/media-service/internal/delivery"
	mediaEvents "github.com/atpost/media-service/internal/events"
	mediaHttp "github.com/atpost/media-service/internal/http"
	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
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
	logging.Init(logging.Config{ServiceName: "media-service"})

	// 2. Config
	port := env("HTTP_PORT", "8087")
	pgDSN := os.Getenv("POSTGRES_DSN")
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		slog.Error("JWT_SECRET environment variable is required")
		os.Exit(1)
	}

	minioEndpoint := os.Getenv("MINIO_ENDPOINT")
	minioAccessKey := os.Getenv("MINIO_ACCESS_KEY")
	minioSecretKey := os.Getenv("MINIO_SECRET_KEY")
	minioBucket := os.Getenv("MINIO_BUCKET")
	minioUseSSL := os.Getenv("MINIO_USE_SSL") == "true"
	minioPublicEndpoint := os.Getenv("MINIO_PUBLIC_ENDPOINT") // e.g. http://localhost:9000
	kafkaBrokers := env("KAFKA_BROKERS", "kafka:9092")

	if minioEndpoint == "" {
		minioEndpoint = "minio:9000"
		minioAccessKey = "minioadmin"
		minioSecretKey = "minioadmin"
		minioBucket = "media"
	}

	// 3. Database (Postgres)
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
		slog.Error("failed to bootstrap media schema", "error", err)
		os.Exit(1)
	}
	slog.Info("media schema ready")

	// 4. Blob store. Production is AWS-only and accepts IRSA web identity;
	// MinIO/static credentials remain a local-development path only.
	blobStore, err := buildBlobStore(minioEndpoint, minioAccessKey, minioSecretKey,
		minioBucket, minioUseSSL, minioPublicEndpoint)
	if err != nil {
		slog.Error("failed to configure blob store", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to blob store")

	// 4b. Redis (for upload rate limiting)
	redisAddr := env("REDIS_ADDR", "redis:6379")
	rdb, err := transport.NewRedisClientFromEnv(redisAddr)
	if err != nil {
		slog.Warn("redis transport config invalid, upload rate limiting disabled", "error", err)
		rdb = nil
	} else if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Warn("redis not available, upload rate limiting disabled", "error", err)
		_ = rdb.Close()
		rdb = nil
	} else {
		slog.Info("connected to redis", "addr", redisAddr)
	}

	kafkaDialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("failed to configure kafka dialer", "error", err)
		os.Exit(1)
	}

	// 5. Dependencies
	pgStore := postgres.New(dbPool)
	mediaCfg := config.Load()
	mediaScanner, err := buildMediaScanner(ctx)
	if err != nil {
		slog.Error("failed to configure image scanner", "error", err)
		os.Exit(1)
	}
	mediaSvc := service.NewWithConfig(pgStore, blobStore, mediaCfg, mediaScanner)
	if rdb != nil {
		mediaSvc.SetRedis(rdb)
	}

	// M4-P0-5 — byte-delivery authorization.
	//
	// The gate signs protected media with a bounded TTL after asking
	// post-service whether this viewer may have it. If it cannot be built the
	// service starts WITHOUT it, and every protected read then fails closed
	// with 503 rather than falling back to the stable CDN URL this item exists
	// to remove. In production that is a boot failure instead (below), because
	// a production media service that cannot serve protected media is broken
	// and should say so loudly rather than 503 every story image.
	if gate, gerr := buildDeliveryGate(blobStore); gerr != nil {
		if isProductionEnv() {
			slog.Error("delivery gate could not be configured; refusing to start in production",
				"err", gerr)
			os.Exit(1)
		}
		slog.Warn("delivery gate not configured — ALL protected media reads will fail closed. "+
			"Set MEDIA_CDN_BASE_URL, MEDIA_CLOUDFRONT_KEY_PAIR_ID, MEDIA_CLOUDFRONT_PRIVATE_KEY "+
			"and POST_SERVICE_URL to enable protected delivery.", "err", gerr)
	} else {
		mediaSvc.WithDeliveryGate(gate)
		slog.Info("delivery gate configured", "cdn", env("MEDIA_CDN_BASE_URL", ""))
	}

	// Audit H9: sweep media_assets stuck at `pending_upload` past
	// 24 h and reclaim the row + blob. Without this an upload that
	// never reached /v1/media/confirm (client crash, network drop)
	// stayed in the table forever; storage grew unbounded.
	service.NewOrphanGCWorker(mediaSvc).Start(ctx)
	slog.Info("orphan media GC worker started")

	// 6. Kafka producer for video transcode events
	brokers := strings.Split(kafkaBrokers, ",")
	producer := mediaEvents.NewProducerWithDialer(brokers, "media.events", kafkaDialer)
	defer producer.Close()
	mediaSvc.SetProducer(producer)
	mediaSvc.StartMediaEventOutboxRelay(ctx)
	slog.Info("kafka producer initialized")

	// Module 1 fixes-v1: durable caption worker. Claims media_caption_jobs
	// with FOR UPDATE SKIP LOCKED (safe in every replica), persists the
	// transcript to media_subtitles, and releases the voice safety gate on
	// completion (or routes to manual review on terminal failure).
	mediaSvc.StartCaptionWorker(ctx)

	// LB-1 requirement 7: retry blob deletions whose object keys were
	// durably recorded before the media rows were removed.
	mediaSvc.StartBlobReclaimWorker(ctx)

	// 7. Prometheus metrics
	httpMetrics := metrics.NewHTTPMetrics("media-service")
	dbMetrics := metrics.NewDBPoolMetrics("media-service", "postgres")

	go collectDBPoolStats(ctx, dbPool, dbMetrics)

	// 8. Health checker
	checker := health.New("media-service")
	checker.Register("postgres", health.PingCheck(dbPool))

	// 9. HTTP Server with middleware
	// C7 — accept the previous secret too during a kid rotation window.
	jwtKeys := mediaHttp.JWTKeySet{
		ActiveKID:      env("JWT_KID", "v1"),
		ActiveSecret:   jwtSecret,
		PreviousKID:    os.Getenv("JWT_KID_PREVIOUS"),
		PreviousSecret: os.Getenv("JWT_SECRET_PREVIOUS"),
	}
	// Optional RS256 verification (additive): load auth-service's public key.
	if pubPEM := os.Getenv("JWT_PUBLIC_KEY_PEM"); pubPEM != "" {
		pub, perr := mediaHttp.ParseRSAPublicKeyPEM(pubPEM)
		if perr != nil {
			slog.Error("failed to parse JWT_PUBLIC_KEY_PEM", "err", perr)
			os.Exit(1)
		}
		jwtKeys.RSAKeys = map[string]*rsa.PublicKey{env("JWT_RS256_KID", "rsa-1"): pub}
		slog.Info("RS256 token verification enabled", "kid", env("JWT_RS256_KID", "rsa-1"))
	}
	authMW := mediaHttp.AuthMiddlewareWithKeys(jwtKeys)
	optionalAuthMW := mediaHttp.OptionalAuthMiddlewareWithKeys(jwtKeys)
	// Module 1 fixes-v3 / LB-1: wire the internal service key.
	//
	// This was declared on the handler but never called, so the internal
	// orphan-delete route was registered with NO authentication at all —
	// an unauthenticated destructive endpoint. Startup now refuses in
	// production when the key is absent, rather than silently running
	// without service-to-service authentication.
	//
	// Local development / tests: leaving INTERNAL_SERVICE_KEY empty is
	// permitted OUTSIDE production, and in that case the destructive
	// internal route is not registered at all (see RegisterRoutes). An
	// empty credential therefore never produces a permissive endpoint.
	internalServiceKey := os.Getenv("INTERNAL_SERVICE_KEY")
	if internalServiceKey == "" {
		if env("DEPLOY_ENV", "") == "production" {
			slog.Error("media-service: INTERNAL_SERVICE_KEY is required in production (service-to-service authentication for destructive internal routes)")
			os.Exit(1)
		}
		slog.Warn("media-service: INTERNAL_SERVICE_KEY not set — internal routes disabled (non-production only)")
	}
	mediaHandler := mediaHttp.New(mediaSvc).WithInternalKey(internalServiceKey)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger())
	r.Use(middleware.Metrics(httpMetrics))

	checker.RegisterRoutes(r)
	r.GET("/metrics", metrics.Handler())
	mediaHandler.RegisterRoutes(r, authMW, optionalAuthMW)
	mediaHandler.RegisterAudioRoutes(r, authMW)
	mediaHandler.RegisterClipsRoutes(r, authMW)
	mediaHandler.RegisterRenditionRoutes(r, authMW)
	mediaHandler.RegisterResumableRoutes(r, authMW)
	mediaHandler.RegisterSlotRoutes(r, authMW)
	mediaHandler.RegisterStudioRoutes(r, authMW)

	// 10. Graceful shutdown
	if err := server.Run(r, server.Config{
		Port:            port,
		ShutdownTimeout: 10 * time.Second,
		OnShutdown: func() {
			if rdb != nil {
				_ = rdb.Close()
			}
			producer.Close()
			dbPool.Close()
			slog.Info("cleanup completed")
		},
	}); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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

// buildDeliveryGate assembles protected-media delivery from the environment.
//
// M4-P0-5. The private key comes from MEDIA_CLOUDFRONT_PRIVATE_KEY, which is
// populated from Secrets Manager through the pod's IRSA identity — never from
// a manifest. There is deliberately no default and no fallback: a signer that
// degrades to unsigned delivery when misconfigured reintroduces the exact hole
// this replaces.
func buildDeliveryGate(blobStore *blob.Store) (*delivery.Gate, error) {
	postURL := env("POST_SERVICE_URL", "")
	if postURL == "" {
		// Without a content authority there is nobody to ask, and "nobody to
		// ask" must not mean "yes".
		return nil, fmt.Errorf("POST_SERVICE_URL is required: protected media " +
			"cannot be authorized without its content authority")
	}
	chatURL := env("CHAT_MESSAGE_SERVICE_URL", "")
	if chatURL == "" {
		return nil, fmt.Errorf("CHAT_MESSAGE_SERVICE_URL is required: protected chat media cannot be authorized")
	}
	internalKey := os.Getenv("INTERNAL_SERVICE_KEY")
	authz := delivery.AnyContentAuthorizer{
		delivery.NewHTTPContentAuthorizer(postURL, internalKey, nil),
		delivery.NewHTTPChatAuthorizer(chatURL, internalKey, nil),
	}

	var signer delivery.URLSigner
	if isProductionEnv() || os.Getenv("MEDIA_CLOUDFRONT_KEY_PAIR_ID") != "" || os.Getenv("MEDIA_CLOUDFRONT_PRIVATE_KEY") != "" {
		cloudFrontSigner, err := delivery.NewSigner(delivery.Config{
			CDNBaseURL:    env("MEDIA_CDN_BASE_URL", ""),
			KeyPairID:     env("MEDIA_CLOUDFRONT_KEY_PAIR_ID", ""),
			PrivateKeyPEM: []byte(os.Getenv("MEDIA_CLOUDFRONT_PRIVATE_KEY")),
		})
		if err != nil {
			return nil, err
		}
		signer = cloudFrontSigner
	} else {
		// Local Docker has no CloudFront key pair. It still uses the exact same
		// content-authorization gate, then issues bounded MinIO signatures.
		// Production cannot select this branch.
		signer = localBlobSigner{store: blobStore}
	}
	return delivery.NewGate(signer, authz), nil
}

type localBlobSigner struct{ store *blob.Store }

func (s localBlobSigner) PublicURL(key string) (string, error) {
	return s.store.ObjectURL(context.Background(), key, delivery.MaxProtectedTTL)
}

func (s localBlobSigner) SignProtected(key string, ttl time.Duration, _ time.Time) (string, error) {
	u, err := s.store.GeneratePresignedGetURL(context.Background(), key, ttl)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// isProductionEnv mirrors the production detection used elsewhere in this
// service, so the delivery gate's start-up strictness matches the rest of the
// service's fail-closed configuration checks.
func isProductionEnv() bool {
	for _, key := range []string{"DEPLOY_ENV", "APP_ENV", "ENVIRONMENT", "ENV"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "production", "prod":
			return true
		}
	}
	return false
}

func buildBlobStore(endpoint, accessKey, secretKey, bucket string, useSSL bool, publicEndpoint string) (*blob.Store, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("MEDIA_STORAGE_BACKEND")))
	if backend == "s3" {
		return blob.NewS3IRSA(os.Getenv("AWS_REGION"), os.Getenv("S3_BUCKET"))
	}
	if isProductionEnv() {
		return nil, fmt.Errorf("production requires MEDIA_STORAGE_BACKEND=s3 with IRSA; MinIO/static credentials are not allowed")
	}
	return blob.NewWithPublicEndpoint(endpoint, accessKey, secretKey, bucket, useSSL, publicEndpoint)
}

func buildMediaScanner(ctx context.Context) (processing.Scanner, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("MEDIA_SCANNER_BACKEND")))
	if backend == "rekognition" {
		if os.Getenv("AWS_ACCESS_KEY_ID") != "" || os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
			return nil, fmt.Errorf("static AWS credentials are forbidden; use IRSA")
		}
		if isProductionEnv() && (os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE") == "" || os.Getenv("AWS_ROLE_ARN") == "") {
			return nil, fmt.Errorf("production Rekognition requires AWS_WEB_IDENTITY_TOKEN_FILE and AWS_ROLE_ARN; node-role fallback is forbidden")
		}
		return processing.NewRekognitionScanner(ctx, os.Getenv("AWS_REGION"), processing.RekognitionConfig{
			MinConfidence: 80,
		})
	}
	if isProductionEnv() {
		return nil, fmt.Errorf("production requires MEDIA_SCANNER_BACKEND=rekognition")
	}
	return &processing.StubScanner{}, nil
}
