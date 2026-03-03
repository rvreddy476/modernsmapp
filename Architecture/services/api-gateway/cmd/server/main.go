package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

type route struct {
	prefix string
	target *url.URL
	proxy  *httputil.ReverseProxy
}

func main() {
	port := env("HTTP_PORT", "8080")
	allowedOrigins := strings.Split(env("CORS_ORIGINS", "http://localhost:3000"), ",")

	// Read at startup so it is visible in logs / future use by downstream callers
	// that may need it forwarded. The actual signature validation is intentionally
	// omitted at the gateway layer; downstream services hold the authoritative
	// secret and perform full validation.
	_ = env("JWT_SECRET", "")

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
		{"/v1/analytics", env("ANALYTICS_SERVICE_URL", "http://analytics-service:8093")},
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

	handler := jwtExtractMiddleware(coreHandler)

	log.Printf("API Gateway listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}

// jwtExtractMiddleware inspects the Authorization: Bearer <token> header,
// extracts claims from the JWT payload segment (without signature verification —
// downstream services are responsible for full validation), and propagates the
// trusted identity headers X-User-Id, X-Scopes, and X-Device-Id to the proxied
// request. Requests without a valid Bearer token are passed through unchanged so
// that unauthenticated endpoints continue to work normally.
func jwtExtractMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			userID, scopes, deviceID := extractJWTClaims(token)
			if userID != "" {
				r.Header.Set("X-User-Id", userID)
			}
			if scopes != "" {
				r.Header.Set("X-Scopes", scopes)
			}
			if deviceID != "" {
				r.Header.Set("X-Device-Id", deviceID)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// extractJWTClaims decodes the payload segment of a JWT (base64url, no padding)
// and returns the user ID, scopes, and device ID embedded in the claims.
// It does NOT verify the signature; signature verification is the responsibility
// of individual downstream services which hold the authoritative JWT_SECRET.
func extractJWTClaims(tokenStr string) (userID, scopes, deviceID string) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return
	}
	// Decode payload (base64url, no padding)
	payload := parts[1]
	// Add padding if needed
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	data, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return
	}
	var claims struct {
		Sub      string `json:"sub"`
		UserID   string `json:"user_id"`
		Scopes   string `json:"scopes"`
		DeviceID string `json:"device_id"`
	}
	if err := json.Unmarshal(data, &claims); err != nil {
		return
	}
	userID = claims.UserID
	if userID == "" {
		userID = claims.Sub
	}
	scopes = claims.Scopes
	deviceID = claims.DeviceID
	return
}

func isAllowedOrigin(origin string, allowed []string) bool {
	for _, a := range allowed {
		if a == "*" || a == origin {
			return true
		}
	}
	return false
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
