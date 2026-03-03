package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type route struct {
	prefix string
	target *url.URL
	proxy  *httputil.ReverseProxy
}

func main() {
	port := env("HTTP_PORT", "8080")
	allowedOrigins := strings.Split(env("CORS_ORIGINS", "http://localhost:3000"), ",")

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

	routeDefs := []struct {
		prefix string
		target string
	}{
		// Longest prefixes first for correct matching
		{"/v1/admin/flags", env("FLAGS_SERVICE_URL", "http://feature-flag-service:8095")},
		{"/v1/auth", env("AUTH_SERVICE_URL", "http://identity-auth:8081")},
		{"/v1/profiles", env("PROFILE_SERVICE_URL", "http://identity-profile:8098")},
		// User service: users, channels, pages, links
		{"/v1/channels", env("USER_SERVICE_URL", "http://user-service:8082")},
		{"/v1/pages", env("USER_SERVICE_URL", "http://user-service:8082")},
		{"/v1/links", env("USER_SERVICE_URL", "http://user-service:8082")},
		{"/v1/users", env("USER_SERVICE_URL", "http://user-service:8082")},
		{"/v1/graph", env("GRAPH_SERVICE_URL", "http://graph-service:8083")},
		// Post service: posts, comments, stories, reactions, saved, hashtags
		{"/v1/stories", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/saved", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/hashtags", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/comments", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/posts", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/feed", env("FEED_SERVICE_URL", "http://feed-service:8086")},
		{"/v1/media", env("MEDIA_SERVICE_URL", "http://media-service:8087")},
		{"/v1/notifications", env("NOTIFY_SERVICE_URL", "http://notification-service:8088")},
		// Search service: search, discover
		{"/v1/discover", env("SEARCH_SERVICE_URL", "http://search-service:8089")},
		{"/v1/search", env("SEARCH_SERVICE_URL", "http://search-service:8089")},
		{"/v1/groups", env("GROUP_SERVICE_URL", "http://group-service:8090")},
		{"/v1/reports", env("TRUST_SAFETY_SERVICE_URL", "http://trust-safety-service:8091")},
		{"/v1/chat", env("MESSAGE_SERVICE_URL", "http://message-service:8092")},
		{"/v1/analytics", env("ANALYTICS_SERVICE_URL", "http://analytics-service:8094")},
		{"/v1/flags", env("FLAGS_SERVICE_URL", "http://feature-flag-service:8095")},
		{"/v1/admin", env("ADMIN_SERVICE_URL", "http://admin-service:8096")},
		// Monetization service
		{"/v1/monetization", env("MONETIZATION_SERVICE_URL", "http://monetization-service:8099")},
		// Suggestion service
		{"/v1/suggestions", env("SUGGESTION_SERVICE_URL", "http://suggestion-service:8100")},
		// Orders / Bookings service (v2.1)
		{"/v1/orders", env("ORDERS_SERVICE_URL", "http://localhost:8101")},
		{"/v1/bookings", env("ORDERS_SERVICE_URL", "http://localhost:8101")},
		// Payments service (v2.1)
		{"/v1/payments", env("PAYMENTS_SERVICE_URL", "http://localhost:8102")},
		// Shop / Live / Memories services
		{"/v1/shop",     env("SHOP_SERVICE_URL",     "http://localhost:8105")},
		{"/v1/live",     env("LIVE_SERVICE_URL",      "http://localhost:8103")},
		{"/v1/memories", env("MEMORIES_SERVICE_URL",  "http://localhost:8104")},
	}

	var routes []route
	for _, rd := range routeDefs {
		target, err := url.Parse(rd.target)
		if err != nil {
			log.Fatalf("invalid target URL %q: %v", rd.target, err)
		}
		routes = append(routes, route{
			prefix: rd.prefix,
			target: target,
			proxy:  httputil.NewSingleHostReverseProxy(target),
		})
		log.Printf("  %s -> %s", rd.prefix, rd.target)
	}

	coreHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS
		origin := r.Header.Get("Origin")
		if origin != "" && isAllowedOrigin(origin, allowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-User-Id, X-Requested-With, Authorization, X-Admin-Api-Key, X-CSRF-Token")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Health check
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
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

	handler := rateLimitMiddleware(injectInternalKeyMiddleware(internalKey, jwtExtractMiddleware(jwtSecret, coreHandler)))

	log.Printf("API Gateway listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}

// jwtExtractMiddleware inspects the Authorization: Bearer <token> header,
// verifies the JWT signature using jwtSecret (HMAC-SHA256), and propagates the
// trusted identity headers X-User-Id, X-Verified-User-Id, X-Scopes, and
// X-Device-Id to the proxied request.
// Requests without a Bearer token are passed through unchanged so that
// unauthenticated (public) endpoints continue to work normally.
// Requests with an invalid or expired token receive HTTP 401.
func jwtExtractMiddleware(jwtSecret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			// No token present — pass through for public endpoints.
			next.ServeHTTP(w, r)
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		userID, scopes, deviceID, err := verifyJWT(token, jwtSecret)
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

// verifyJWT validates an HS256 JWT against secret, checks expiry, and returns
// the user ID, scopes, and device ID from the claims.
func verifyJWT(tokenStr, secret string) (userID, scopes, deviceID string, err error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return "", "", "", &jwtError{"malformed token"}
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
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if idx := strings.Index(xff, ","); idx != -1 {
				ip = strings.TrimSpace(xff[:idx])
			} else {
				ip = strings.TrimSpace(xff)
			}
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

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
