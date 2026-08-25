package tokenpolicy

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Module 3 SR-1 — the deployment files are part of the auth boundary.
//
// The gateway and auth-service each fail closed on their own configuration,
// but neither can see the other's. The failure this guards against is a
// mismatch: JWT_AUDIENCE=atpost-api on one side and atpost-api-staging on the
// other. Both services start happily and every single request 401s. That is a
// total outage discovered in production, and no unit test in either service
// can catch it — only a test that reads both manifests.
//
// It is deliberately dependency-free (no YAML library) so it can run in any
// CI job without touching the module graph. The values files are flat
// `key: value` maps under `env:`, which this handles exactly.

const deployRoot = "../../../../../deploy/services"

// envPairs extracts the `env:` block of a Helm values file as a map.
func envPairs(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	out := map[string]string{}
	inEnv := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			// A new top-level key ends the env block.
			inEnv = trimmed == "env:"
			continue
		}
		if !inEnv {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		// Strip a trailing `# comment` and surrounding quotes.
		if i := strings.Index(value, " #"); i >= 0 {
			value = value[:i]
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		out[strings.TrimSpace(key)] = value
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return out
}

// environments pairs a gateway values file with the auth-service values file
// that must agree with it.
// AWS only. The Azure overlays are generated files this platform does not
// deploy; asserting the token contract against them would mean maintaining
// identity configuration for an environment that never runs, and a drift
// there would look like a real failure.
var environments = []struct {
	name           string
	gateway        string
	auth           string
	wantProduction bool
}{
	{"prod", "api-gateway/values-prod.yaml", "identity-auth-service/values-prod.yaml", true},
	{"staging", "api-gateway/values-staging.yaml", "identity-auth-service/values-staging.yaml", false},
}

func TestDeploymentIssuerAndAudienceMatchAcrossServices(t *testing.T) {
	for _, env := range environments {
		t.Run(env.name, func(t *testing.T) {
			gw := envPairs(t, filepath.Join(deployRoot, env.gateway))
			auth := envPairs(t, filepath.Join(deployRoot, env.auth))

			for _, key := range []string{"JWT_ISSUER", "JWT_AUDIENCE"} {
				g, a := gw[key], auth[key]
				if g == "" {
					t.Errorf("%s: gateway does not set %s; production start will fail", env.gateway, key)
				}
				if a == "" {
					t.Errorf("%s: auth-service does not set %s; production start will fail", env.auth, key)
				}
				if g != a {
					t.Errorf("%s MISMATCH: gateway=%q auth-service=%q.\n"+
						"Every token auth-service mints would be rejected at the edge.", key, g, a)
				}
			}

			// The kid stamped at mint must be the kid the gateway looks up.
			if gw["JWT_RS256_KID"] != auth["JWT_RS256_KID"] {
				t.Errorf("JWT_RS256_KID MISMATCH: gateway=%q auth-service=%q; "+
					"RS256 tokens would fail with \"unknown kid\"",
					gw["JWT_RS256_KID"], auth["JWT_RS256_KID"])
			}
		})
	}
}

func TestDeploymentProductionDetectionResolvesCorrectly(t *testing.T) {
	for _, env := range environments {
		t.Run(env.name, func(t *testing.T) {
			for label, path := range map[string]string{"gateway": env.gateway, "auth-service": env.auth} {
				pairs := envPairs(t, filepath.Join(deployRoot, path))

				// Drive the REAL detector from the manifest's own values, so
				// this cannot drift from what the process will decide at boot.
				for _, key := range ProductionEnvVars {
					t.Setenv(key, pairs[key])
				}
				if got := IsProductionEnv(); got != env.wantProduction {
					t.Errorf("%s (%s): production=%v want %v (APP_ENV=%q ENV=%q). "+
						"A production deployment running development token rules accepts "+
						"HS256 and unpinned issuers.",
						label, path, got, env.wantProduction, pairs["APP_ENV"], pairs["ENV"])
				}
			}
		})
	}
}

// A production gateway must ship a narrow query-token allowlist, never a
// catch-all and never a raw prefix that a sibling route can satisfy.
func TestDeploymentQueryTokenPathsAreNarrow(t *testing.T) {
	for _, env := range environments {
		t.Run(env.name, func(t *testing.T) {
			pairs := envPairs(t, filepath.Join(deployRoot, env.gateway))
			raw := pairs["GATEWAY_QUERY_TOKEN_PATHS"]
			if raw == "" {
				return // no query tokens at all is the safest configuration
			}
			var policy Policy
			for _, p := range strings.Split(raw, ",") {
				if p = strings.TrimSpace(p); p != "" {
					policy.QueryTokenPaths = append(policy.QueryTokenPaths, p)
				}
			}
			for _, p := range policy.QueryTokenPaths {
				if p == "/" || p == "" {
					t.Fatalf("%s: catch-all query-token path %q exposes every route", env.gateway, p)
				}
			}
			// Ordinary REST surfaces must never accept a query token, and
			// neither may a near-prefix sibling of an allowed realtime path.
			for _, path := range []string{
				"/v1/profiles/me", "/v1/graph/follow", "/v1/posts", "/v1/users/me",
				"/v1/ws-evil", "/v1/wsx",
			} {
				if policy.QueryTokenAllowed(path) {
					t.Errorf("%s: %s accepts a token in the query string", env.gateway, path)
				}
			}
		})
	}
}
