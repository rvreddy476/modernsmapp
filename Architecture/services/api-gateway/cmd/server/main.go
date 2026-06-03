package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	tracepkg "github.com/atpost/shared/o11y/trace"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"golang.org/x/time/rate"
)

type route struct {
	prefix string
	target *url.URL
	proxy  *httputil.ReverseProxy
}

func main() {
	port := env("HTTP_PORT", "8080")

	// Phase F3.5 — tracing init before anything else so the proxy
	// Director can call otel.GetTextMapPropagator().Inject() below.
	tracerProvider, _ := tracepkg.InitTracer("api-gateway", env("OTEL_EXPORTER_OTLP_ENDPOINT", "http://jaeger:4317"))
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tracerProvider.Shutdown(shutdownCtx)
	}()

	allowedOrigins := strings.Split(env("CORS_ORIGINS", "http://localhost:3000"), ",")
	for _, o := range allowedOrigins {
		if strings.TrimSpace(o) == "*" {
			slog.Warn("CORS_ORIGINS contains wildcard '*' — this allows any origin and must not be used in production")
		}
	}

	// Validate JWT_SECRET at startup; downstream services rely on it for full
	// signature verification so the gateway must ensure it is configured.
	jwtSecret := env("JWT_SECRET", "")
	if jwtSecret == "" {
		slog.Error("JWT_SECRET env var is required")
		os.Exit(1)
	}
	if jwtSecret == "dev_secret_change_me" {
		slog.Warn("JWT_SECRET is set to the development default — do not use in production")
	}
	// C7 — accept the previous secret too during a kid rotation window.
	// See aws_prep_sprint_2026_06.md for the rotation runbook.
	jwtKeys := jwtKeySet{
		activeKID:      env("JWT_KID", "v1"),
		activeSecret:   jwtSecret,
		previousKID:    env("JWT_KID_PREVIOUS", ""),
		previousSecret: env("JWT_SECRET_PREVIOUS", ""),
	}

	routeDefs := []struct {
		prefix string
		target string
	}{
		// Longest prefixes first for correct matching
		{"/v1/admin/flags", env("FLAGS_SERVICE_URL", "http://feature-flag-service:8095")},
		{"/v1/auth", env("AUTH_SERVICE_URL", "http://identity-auth:8081")},
		{"/v1/profiles", env("PROFILE_SERVICE_URL", "http://identity-profile:8098")},
		// User service: onboarding, users, channels, pages, links
		{"/v1/onboarding", env("USER_SERVICE_URL", "http://user-service:8082")},
		{"/v1/channels", env("USER_SERVICE_URL", "http://user-service:8082")},
		{"/v1/pages", env("USER_SERVICE_URL", "http://user-service:8082")},
		{"/v1/links", env("USER_SERVICE_URL", "http://user-service:8082")},
		{"/v1/users", env("USER_SERVICE_URL", "http://user-service:8082")},
		{"/v1/graph", env("GRAPH_SERVICE_URL", "http://graph-service:8083")},
		// Post service: videos, reels, posts, comments, stories, reactions, saved, hashtags, uploads
		{"/v1/uploads", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/videos", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/reels", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/stories", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/saved", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/hashtags", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/comments", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/posts", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/feed", env("FEED_SERVICE_URL", "http://feed-service:8086")},
		{"/v1/audio", env("MEDIA_SERVICE_URL", "http://media-service:8087")},
		{"/v1/media", env("MEDIA_SERVICE_URL", "http://media-service:8087")},
		{"/v1/notifications", env("NOTIFY_SERVICE_URL", "http://notification-service:8088")},
		// Search service: search, discover
		{"/v1/discover", env("SEARCH_SERVICE_URL", "http://search-service:8089")},
		{"/v1/search", env("SEARCH_SERVICE_URL", "http://search-service:8089")},
		{"/v1/groups", env("GROUP_SERVICE_URL", "http://group-service:8090")},
		{"/v1/reports", env("TRUST_SAFETY_SERVICE_URL", "http://trust-safety-service:8091")},
		{"/v1/grievances", env("TRUST_SAFETY_SERVICE_URL", "http://trust-safety-service:8091")},
		{"/v1/ws", env("WS_GATEWAY_URL", "http://ws-gateway:8093")},
		{"/v1/calls", env("CALL_SERVICE_URL", "http://call-service:8097")},
		// Canonical message-service is chat-service/services/message-service.
		// The legacy Architecture/services/message-service was archived
		// 2026-05-25 — docker-compose builds the chat-service variant as
		// the chat-message-service container.
		{"/v1/chat", env("MESSAGE_SERVICE_URL", "http://chat-message-service:8092")},
		{"/v1/analytics", env("ANALYTICS_SERVICE_URL", "http://analytics-service:8094")},
		{"/v1/ai", env("AI_SERVICE_URL", "http://ai-service:8117")},
		{"/v1/flags", env("FLAGS_SERVICE_URL", "http://feature-flag-service:8095")},
		{"/v1/admin", env("ADMIN_SERVICE_URL", "http://admin-service:8096")},
		{"/v1/apps", env("ADMIN_SERVICE_URL", "http://admin-service:8096")},
		{"/v1/oauth", env("ADMIN_SERVICE_URL", "http://admin-service:8096")},
		// Monetization service
		{"/v1/monetization", env("MONETIZATION_SERVICE_URL", "http://monetization-service:8099")},
		// Suggestion service
		{"/v1/suggestions", env("SUGGESTION_SERVICE_URL", "http://suggestion-service:8100")},
		// Phase F1.4 — `/v1/orders` and `/v1/shop` routes RETIRED. Both
		// commerce-domain surfaces now live under `/v1/commerce/*` in
		// commerce-service; mobile and web have re-pointed. The
		// orders-service and shop-service source directories are
		// deleted in this same PR. `/v1/bookings` is a non-commerce
		// surface — it had nowhere to migrate to and is dropped with
		// orders-service; if a bookings product reappears it should be
		// its own service.
		// Payments service (v2.1)
		{"/v1/payments", env("PAYMENTS_SERVICE_URL", "http://payments-service:8102")},
		// Live / Memories services
		{"/v1/live", env("LIVE_SERVICE_URL", "http://live-service:8103")},
		// Live-v2 (LiveKit browser-native broadcast) — separate prefix to
		// avoid colliding with v1 RTMP/OBS routes that own /v1/live.
		{"/v1/livestream", env("LIVE_V2_SERVICE_URL", "http://live-service-v2:8117")},
		{"/v1/memories", env("MEMORIES_SERVICE_URL", "http://memories-service:8104")},
		// Broadcast Channels / Communities (GCC Phase 4)
		{"/v1/broadcast-channels", env("CHANNEL_SERVICE_URL", "http://channel-service:8106")},
		{"/v1/communities", env("COMMUNITY_SERVICE_URL", "http://community-service:8107")},
		// Q&A service
		{"/v1/qa", env("QA_SERVICE_URL", "http://qa-service:8108")},
		// Dating service (Pulse) — see C:\workspace\atpost\dating\PULSE_DATING_SPEC.md
		{"/v1/dating", env("DATING_SERVICE_URL", "http://dating-service:8112")},
		// Food service (FiGo mini app)
		{"/v1/food", env("FOOD_SERVICE_URL", "http://food-service:8113")},
		// Wallet service (BC-of-PPI consumer wallet) — see services/wallet-service.
		{"/v1/wallet", env("WALLET_SERVICE_URL", "http://wallet-service:8114")},
		// Bill-pay service (Setu BBPS aggregator) — see services/bill-pay-service.
		{"/v1/billpay", env("BILL_PAY_SERVICE_URL", "http://bill-pay-service:8115")},
		// Rider service (Mopedu mini-app) — see services/rider-service.
		{"/v1/rider", env("RIDER_SERVICE_URL", "http://rider-service:8116")},
		// Commerce service (full e-commerce rebuild)
		{"/v1/commerce", env("COMMERCE_SERVICE_URL", "http://commerce-service:8109")},
	}

	var routes []route
	for _, rd := range routeDefs {
		target, err := url.Parse(rd.target)
		if err != nil {
			log.Fatalf("invalid target URL %q: %v", rd.target, err)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		// Phase F3.5 — wrap the default Director so every upstream
		// request carries the W3C traceparent header derived from the
		// active server span. The outer handler is wrapped in
		// otelhttp.NewHandler below, which establishes the span; here
		// we just propagate it forward.
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			otel.GetTextMapPropagator().Inject(req.Context(), propagation.HeaderCarrier(req.Header))
		}
		// Otel transport on the proxy gives us a client span per
		// upstream call so Jaeger shows the gateway→upstream hop.
		proxy.Transport = otelhttp.NewTransport(http.DefaultTransport)
		routes = append(routes, route{
			prefix: rd.prefix,
			target: target,
			proxy:  proxy,
		})
		log.Printf("  %s -> %s", rd.prefix, rd.target)
	}

	// Prometheus metrics endpoint. promhttp serves the default registry
	// which all shared/o11y/metrics constructors register against, so
	// scraping /metrics on the gateway also exposes its own HTTP-side
	// counters once we wrap upstream calls.
	promHandler := promhttp.Handler()

	coreHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleProbe(w, r, len(routes)) {
			return
		}
		// Prometheus scrape endpoint.
		if r.URL.Path == "/metrics" {
			promHandler.ServeHTTP(w, r)
			return
		}

		// Route matching — longest prefix wins (routes are pre-sorted)
		for _, rt := range routes {
			if r.URL.Path == rt.prefix || strings.HasPrefix(r.URL.Path, rt.prefix+"/") {
				rt.proxy.ServeHTTP(w, r)
				return
			}
		}

		http.NotFound(w, r)
	})

	internalKey := env("INTERNAL_SERVICE_KEY", "")
	if internalKey == "" {
		slog.Warn("INTERNAL_SERVICE_KEY not set — internal service authentication disabled")
	}

	// H5 — Redis-backed rate limiter so per-IP / per-user limits hold
	// across the whole gateway fleet, not just per-pod. The in-memory
	// limiter still runs in front of this and acts as a fast-path
	// short-circuit + the dev fallback when REDIS_ADDR isn't set.
	rateRDB := connectRedisForRateLimit(env("REDIS_ADDR", ""))
	var rateLimitHandler func(http.Handler) http.Handler
	if rateRDB != nil {
		rl := newRedisRateLimiter(rateRDB, 100, 60, time.Second)
		rateLimitHandler = func(next http.Handler) http.Handler {
			return rateLimitMiddleware(redisRateLimitMiddleware(rl, next))
		}
		slog.Info("rate limiter: redis-backed (fleet-wide) enabled")
	} else {
		rateLimitHandler = rateLimitMiddleware
		slog.Info("rate limiter: in-memory only (per-pod)")
	}

	// CORS middleware is outermost so headers are present on ALL responses (including 401/429).
	// Phase F3.5 — otelhttp wraps the chain inside CORS so a server span
	// is opened on every request. The span name is just the method;
	// downstream services produce the more specific route names.
	tracedCore := otelhttp.NewHandler(
		requestIDMiddleware(jwtExtractMiddleware(jwtKeys, rateLimitHandler(requireAdminForInternalPaths(injectInternalKeyMiddleware(internalKey, coreHandler))))),
		"api-gateway",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)
	handler := corsMiddleware(allowedOrigins, tracedCore)

	// Add a recovery middleware wrapper
	recoveryHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				if abortErr, ok := err.(error); ok && errors.Is(abortErr, http.ErrAbortHandler) {
					// ReverseProxy uses ErrAbortHandler to stop streaming handlers when
					// the client disconnects. That is not an application error and we
					// must not overwrite an already-started SSE response with a 500.
					return
				}
				slog.Error("panic recovered", "error", err, "path", r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"internal server error"}`))
			}
		}()
		handler.ServeHTTP(w, r)
	})

	log.Printf("API Gateway listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, recoveryHandler))
}

// jwtExtractMiddleware inspects the Authorization: Bearer <token> header,
// verifies the JWT signature using jwtSecret (HMAC-SHA256), and propagates the
// trusted identity headers X-User-Id, X-Verified-User-Id, X-Scopes, and
// X-Device-Id to the proxied request.
// Requests without a Bearer token are passed through unchanged so that
// unauthenticated (public) endpoints continue to work normally.
// Requests with an invalid or expired token receive HTTP 401.
// jwtKeySet — C7. Active is used for signing (auth-service only); both are
// accepted on verify so a kid rotation has a window where prior tokens stay
// valid. A token with no `kid` header (legacy, pre-C7) falls back to active.
type jwtKeySet struct {
	activeKID      string
	activeSecret   string
	previousKID    string
	previousSecret string
}

func (k jwtKeySet) secretFor(kid string) (string, bool) {
	if kid == "" || kid == k.activeKID {
		return k.activeSecret, true
	}
	if k.previousSecret != "" && kid == k.previousKID {
		return k.previousSecret, true
	}
	return "", false
}

func jwtExtractMiddleware(keys jwtKeySet, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Resolve JWT from one of (in priority order):
		//   1. Authorization: Bearer header   — mobile + REST callers
		//   2. access_token cookie            — browser EventSource
		//      (and anywhere XHR can't set Authorization)
		//   3. access_token / token query     — explicit ?token=foo
		//      override; kept for legacy WebSocket clients that pass
		//      auth in the query string.
		// Falling through with no token leaves the request
		// unauthenticated; downstream handlers may 401 if they care.
		var token string
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		} else if c, err := r.Cookie("access_token"); err == nil && c.Value != "" {
			token = c.Value
		} else if q := r.URL.Query().Get("access_token"); q != "" {
			token = q
		} else if q := r.URL.Query().Get("token"); q != "" {
			token = q
		}
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		userID, scopes, deviceID, err := verifyJWT(token, keys)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"code":"UNAUTHORIZED","message":"Invalid or expired token"}}`))
			return
		}
		if userID != "" {
			r.Header.Set("X-User-Id", userID)
			r.Header.Set("X-Verified-User-Id", userID)
		}
		if scopes != "" {
			r.Header.Set("X-Scopes", scopes)
		}
		if deviceID != "" {
			r.Header.Set("X-Device-Id", deviceID)
		}
		next.ServeHTTP(w, r)
	})
}

// verifyJWT validates an HS256 JWT against the configured key set, checks
// expiry, and returns the user ID, scopes, and device ID from the claims.
// C7: the `kid` header selects the secret so a rotation window can accept
// both old and new signatures simultaneously.
func verifyJWT(tokenStr string, keys jwtKeySet) (userID, scopes, deviceID string, err error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return "", "", "", &jwtError{"malformed token"}
	}

	// Pick the secret by `kid` (C7). Tokens without a `kid` fall back to
	// the active secret so legacy tokens minted before C7 still verify.
	headerRaw, hdrErr := base64.RawURLEncoding.DecodeString(parts[0])
	if hdrErr != nil {
		return "", "", "", &jwtError{"invalid header encoding"}
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if jsonErr := json.Unmarshal(headerRaw, &header); jsonErr != nil {
		return "", "", "", &jwtError{"invalid header JSON"}
	}
	if header.Alg != "" && header.Alg != "HS256" {
		return "", "", "", &jwtError{"unsupported jwt algorithm"}
	}
	secret, ok := keys.secretFor(header.Kid)
	if !ok {
		return "", "", "", &jwtError{"unknown kid"}
	}

	// Verify HMAC-SHA256 signature.
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	actualSig, decErr := base64.RawURLEncoding.DecodeString(parts[2])
	if decErr != nil {
		return "", "", "", &jwtError{"invalid signature encoding"}
	}
	if !hmac.Equal([]byte(expectedSig), []byte(base64.RawURLEncoding.EncodeToString(actualSig))) {
		return "", "", "", &jwtError{"signature verification failed"}
	}

	// Decode payload.
	data, decErr := base64.RawURLEncoding.DecodeString(parts[1])
	if decErr != nil {
		return "", "", "", &jwtError{"invalid payload encoding"}
	}

	var claims struct {
		Sub      string `json:"sub"`
		UserID   string `json:"user_id"`
		Exp      int64  `json:"exp"`
		Scopes   string `json:"scopes"`
		DeviceID string `json:"device_id"`
	}
	if jsonErr := json.Unmarshal(data, &claims); jsonErr != nil {
		return "", "", "", &jwtError{"invalid payload JSON"}
	}

	// Check expiry when exp claim is present.
	if claims.Exp != 0 && time.Now().Unix() > claims.Exp {
		return "", "", "", &jwtError{"token expired"}
	}

	userID = claims.UserID
	if userID == "" {
		userID = claims.Sub
	}
	return userID, claims.Scopes, claims.DeviceID, nil
}

// jwtError is a simple error type for JWT validation failures.
type jwtError struct{ msg string }

func (e *jwtError) Error() string { return "jwt: " + e.msg }

func isAllowedOrigin(origin string, allowed []string) bool {
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "*" || a == origin {
			return true
		}
	}
	return false
}

// rateLimiterEntry tracks a token-bucket limiter and the last time it was used.
type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// rateLimiterStore is a thread-safe store of per-key token-bucket limiters.
type rateLimiterStore struct {
	mu       sync.Mutex
	limiters map[string]*rateLimiterEntry
	r        float64
	b        int
}

func newRateLimiterStore(r float64, b int) *rateLimiterStore {
	s := &rateLimiterStore{
		limiters: make(map[string]*rateLimiterEntry),
		r:        r,
		b:        b,
	}
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			s.mu.Lock()
			threshold := time.Now().Add(-10 * time.Minute)
			for k, e := range s.limiters {
				if e.lastSeen.Before(threshold) {
					delete(s.limiters, k)
				}
			}
			s.mu.Unlock()
		}
	}()
	return s
}

func (s *rateLimiterStore) get(key string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.limiters[key]; ok {
		e.lastSeen = time.Now()
		return e.limiter
	}
	l := rate.NewLimiter(rate.Limit(s.r), s.b)
	s.limiters[key] = &rateLimiterEntry{limiter: l, lastSeen: time.Now()}
	return l
}

// rateLimitMiddleware enforces per-IP (100 rps, burst 200) and per-user
// (60 rps, burst 120) rate limits. Returns HTTP 429 when a limit is exceeded.
func rateLimitMiddleware(next http.Handler) http.Handler {
	ipStore := newRateLimiterStore(100, 200)
	userStore := newRateLimiterStore(60, 120)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			ip = strings.TrimSpace(realIP)
		} else if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if idx := strings.Index(xff, ","); idx != -1 {
				ip = strings.TrimSpace(xff[:idx])
			} else {
				ip = strings.TrimSpace(xff)
			}
		}
		// Strip port if present (RemoteAddr includes port)
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}
		if !ipStore.get(ip).Allow() {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"code":"RATE_LIMITED","message":"too many requests"}}`))
			return
		}
		if userID := r.Header.Get("X-User-Id"); userID != "" {
			if !userStore.get(userID).Allow() {
				w.Header().Set("Retry-After", "1")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":{"code":"RATE_LIMITED","message":"too many requests"}}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// injectInternalKeyMiddleware sets the X-Internal-Service-Key header on every
// proxied request so that backend services can verify the request came from
// the gateway. When secret is empty, the header is not set.
func injectInternalKeyMiddleware(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if secret != "" {
			r.Header.Set("X-Internal-Service-Key", secret)
		}
		next.ServeHTTP(w, r)
	})
}

// requireAdminForInternalPaths is the gate the gateway sits in front of
// any `/v1/<domain>/internal/*` route. These paths exist for admin /
// moderator surfaces (admin queues, payout settlement, etc.) and are
// authenticated by the downstream service on the X-Internal-Service-Key
// header that the gateway injects. Without an explicit scope check
// here, the internal-key injection would effectively turn every
// internal endpoint into a public one — any logged-in user could hit it
// and the downstream would accept because the gateway vouched.
//
// Policy: scopes claim on the JWT must contain "admin" or "moderator".
// Tokens minted by the regular login flow don't include these scopes;
// admin-service issues them only after a verified role lookup. Missing
// or absent scope → 403.
//
// commerce TODO Blocker #3.
func requireAdminForInternalPaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/internal/") {
			next.ServeHTTP(w, r)
			return
		}
		scopes := r.Header.Get("X-Scopes")
		if !scopeAllows(scopes, "admin") && !scopeAllows(scopes, "moderator") && !scopeAllows(scopes, "superadmin") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"FORBIDDEN","message":"admin scope required for internal endpoints"}}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// scopeAllows checks whether a space-separated scopes list contains
// the named scope. Empty list → never allowed.
func scopeAllows(scopes, want string) bool {
	if scopes == "" {
		return false
	}
	for _, s := range strings.Split(scopes, " ") {
		if s == want {
			return true
		}
	}
	return false
}

// requestIDMiddleware ensures every request has an X-Request-Id header for
// end-to-end tracing. It honours an existing X-Request-Id set by an upstream
// load balancer; otherwise it generates one. The ID is also echoed back on the
// response so clients can correlate logs.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")
		if reqID == "" {
			reqID = fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Int63())
		}
		r.Header.Set("X-Request-Id", reqID)
		w.Header().Set("X-Request-Id", reqID)
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware sets CORS headers on every response — including error responses
// from inner middleware (e.g. 401 from JWT validation). Handles OPTIONS preflight.
func corsMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && isAllowedOrigin(origin, allowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-User-Id, X-Requested-With, Authorization, X-Admin-Api-Key, X-CSRF-Token, X-Request-Id, X-Client-Platform, Idempotency-Key")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
