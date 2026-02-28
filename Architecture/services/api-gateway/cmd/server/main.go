package main

import (
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

	routeDefs := []struct {
		prefix string
		target string
	}{
		// Longest prefixes first for correct matching
		{"/v1/admin/flags", env("FLAGS_SERVICE_URL", "http://feature-flag-service:8095")},
		{"/v1/auth", env("AUTH_SERVICE_URL", "http://identity-auth:8081")},
		{"/v1/profiles", env("PROFILE_SERVICE_URL", "http://identity-profile:8098")},
		{"/v1/users", env("USER_SERVICE_URL", "http://user-service:8082")},
		{"/v1/graph", env("GRAPH_SERVICE_URL", "http://graph-service:8083")},
		{"/v1/comments", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/posts", env("POST_SERVICE_URL", "http://post-service:8084")},
		{"/v1/feed", env("FEED_SERVICE_URL", "http://feed-service:8086")},
		{"/v1/media", env("MEDIA_SERVICE_URL", "http://media-service:8087")},
		{"/v1/notifications", env("NOTIFY_SERVICE_URL", "http://notification-service:8088")},
		{"/v1/search", env("SEARCH_SERVICE_URL", "http://search-service:8089")},
		{"/v1/groups", env("GROUP_SERVICE_URL", "http://group-service:8090")},
		{"/v1/reports", env("TRUST_SAFETY_SERVICE_URL", "http://trust-safety-service:8091")},
		{"/v1/chat", env("MESSAGE_SERVICE_URL", "http://message-service:8092")},
		{"/v1/analytics", env("ANALYTICS_SERVICE_URL", "http://analytics-service:8093")},
		{"/v1/flags", env("FLAGS_SERVICE_URL", "http://feature-flag-service:8095")},
		{"/v1/admin", env("ADMIN_SERVICE_URL", "http://admin-service:8096")},
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

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS
		origin := r.Header.Get("Origin")
		if origin != "" && isAllowedOrigin(origin, allowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-User-Id, X-Requested-With, Authorization, X-Admin-Api-Key")
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

	log.Printf("API Gateway listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))
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
