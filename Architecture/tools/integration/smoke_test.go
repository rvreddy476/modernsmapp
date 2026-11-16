//go:build integration

package integration

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSmokeHealthChecks pings every service the run depends on. Cheapest
// possible canary: if any of these is down the rest of the suite is going to
// fail in confusing ways, so fail fast here.
//
// Which services: ATPOST_E2E_HEALTH_URLS (space-separated base URLs) when set,
// which is how .github/workflows/e2e.yml names the services its matrix entry
// started. Otherwise the five the monetization story touches.
//
// A service that is not listening is a skip on a developer machine and a
// failure where the stack is required (StackRequired).
func TestSmokeHealthChecks(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()

	type check struct{ name, url string }
	var checks []check
	if listed := strings.Fields(os.Getenv("ATPOST_E2E_HEALTH_URLS")); len(listed) > 0 {
		for _, u := range listed {
			checks = append(checks, check{u, strings.TrimRight(u, "/")})
		}
	} else {
		checks = []check{
			{"post-service", urls.Post},
			{"monetization-service", urls.Monetization},
			{"user-service", urls.User},
			{"graph-service", urls.Graph},
			{"api-gateway", urls.APIGateway},
		}
	}

	c := &http.Client{Timeout: 2 * time.Second}
	for _, ch := range checks {
		t.Run(ch.name, func(t *testing.T) {
			// The gateway serves /health and every service on shared/health
			// serves /healthz; this used to ask only /health, which those
			// answer with 404. Services with neither route (identity-auth
			// has /v1/auth/health, admin-service puts the internal key in
			// front of everything) still prove they are listening by
			// answering below 500, the same rule the workflow's wait uses.
			var statuses []string
			for _, path := range []string{"/health", "/healthz"} {
				resp, err := c.Get(ch.url + path)
				if err != nil {
					missingStack(t, "%s: %s not reachable: %v (start with `docker compose up`)", ch.name, ch.url, err)
					return
				}
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return
				}
				if resp.StatusCode >= 500 {
					t.Errorf("%s: %s%s returned %d", ch.name, ch.url, path, resp.StatusCode)
					return
				}
				statuses = append(statuses, path+" "+resp.Status)
			}
			t.Logf("%s: listening at %s, but no liveness route answered 200 (%s)", ch.name, ch.url, strings.Join(statuses, ", "))
		})
	}
}
