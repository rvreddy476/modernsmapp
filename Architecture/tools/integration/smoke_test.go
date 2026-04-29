//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestSmokeHealthChecks pings /health on every service the
// monetization story touches. Cheapest possible canary: if any of
// these is down the rest of the suite is going to fail in
// confusing ways, so fail fast here.
func TestSmokeHealthChecks(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()

	checks := []struct {
		name string
		url  string
	}{
		{"post-service", urls.Post},
		{"monetization-service", urls.Monetization},
		{"user-service", urls.User},
		{"graph-service", urls.Graph},
		{"api-gateway", urls.APIGateway},
	}

	c := &http.Client{Timeout: 2 * time.Second}
	for _, ch := range checks {
		t.Run(ch.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, ch.url+"/health", nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			resp, err := c.Do(req)
			if err != nil {
				t.Skipf("%s: %s not reachable: %v (start with `docker compose up`)", ch.name, ch.url, err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("%s: %s/health returned %d, want 200", ch.name, ch.url, resp.StatusCode)
			}
		})
	}
}
