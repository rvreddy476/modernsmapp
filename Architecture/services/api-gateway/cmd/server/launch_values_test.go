package main

// NOTE: new files under cmd/server/ were git-ignored by the root `server` rule
// before the `!server/` exception; add with `git add -f` if in doubt.

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atpost/api-gateway/pkg/disabledprefixes"
)

// Security sweep #13 (21 Oct 2026): parked products that were NOT closed at
// the edge in production. Groups was OPEN (GROUPS_PUBLIC_ENABLED "true"), and
// Feast, Q&A, business pages and broadcast channels had no edge gate at all.
// Launch is Social, Shorts, Long video and Commerce; everything else stays
// closed until it launches. This test reads the real deploy values the way
// the gateway boots from them, so a values edit that reopens one fails here.

const gatewayValuesDir = "../../../../../deploy/services/api-gateway"

// parkedPrefixes must be in GATEWAY_DISABLED_PREFIXES in every launch
// environment (AWS prod, staging and qa; the Azure overlays are not deployed).
var parkedPrefixes = []string{"/v1/food", "/v1/qa", "/v1/pages", "/v1/broadcast-channels"}

// gatewayEnv reads the flat `env:` block of a values file (same dependency-
// free reader as pkg/tokenpolicy/deployconfig_test.go).
func gatewayEnv(t *testing.T, name string) map[string]string {
	t.Helper()
	path := filepath.Join(gatewayValuesDir, name)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	out := map[string]string{}
	inEnv := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") {
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
		if i := strings.Index(value, " #"); i >= 0 {
			value = value[:i]
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return out
}

func TestLaunchValuesCloseParkedProducts(t *testing.T) {
	known := prefixesOf(routeDefinitions())
	for _, tc := range []struct {
		file       string
		production bool
	}{
		{"values-prod.yaml", true},
		{"values-staging.yaml", false},
		{"values-qa.yaml", false},
	} {
		t.Run(tc.file, func(t *testing.T) {
			env := gatewayEnv(t, tc.file)

			// Groups: the flag says false, and the gate built from these
			// values actually closes /v1/groups.
			if got, ok := env["GROUPS_PUBLIC_ENABLED"]; !ok || got != "false" {
				t.Fatalf("GROUPS_PUBLIC_ENABLED = %q (set: %v), want \"false\"", got, ok)
			}
			products, err := dormantProductsFromEnv(envOf(env), tc.production)
			if err != nil {
				t.Fatalf("dormantProductsFromEnv: %v", err)
			}
			for _, path := range []string{"/v1/groups", "/v1/groups/abc/posts"} {
				if !gated(t, products, path) {
					t.Errorf("%s is open with these values", path)
				}
			}
			// Each parked product's own gate is closed too, independently
			// of GATEWAY_DISABLED_PREFIXES (parked_products_gate_test.go).
			for flag, paths := range parkedProductFlags {
				if got, ok := env[flag]; !ok || got != "false" {
					t.Errorf("%s = %q (set: %v), want \"false\"", flag, got, ok)
				}
				for _, path := range paths {
					if !gated(t, products, path) {
						t.Errorf("%s is open at its own gate with these values", path)
					}
				}
			}

			// The disabled list parses against the REAL route table (a typo
			// is a boot failure) and closes every parked prefix.
			raw, ok := env[disabledprefixes.EnvVar]
			if !ok {
				t.Fatalf("%s is not set", disabledprefixes.EnvVar)
			}
			set, err := disabledprefixes.Parse(raw, known)
			if err != nil {
				t.Fatalf("%s does not boot: %v", disabledprefixes.EnvVar, err)
			}
			for _, prefix := range parkedPrefixes {
				for _, path := range []string{prefix, prefix + "/anything"} {
					rec := httptest.NewRecorder()
					served := set.Serve(rec, httptest.NewRequest(http.MethodGet, path, nil))
					if !served || rec.Code != http.StatusNotFound {
						t.Errorf("%s is not closed (served=%v status=%d)", path, served, rec.Code)
					}
				}
			}
			// The launch products stay routed.
			for _, path := range []string{"/v1/posts", "/v1/media", "/v1/commerce", "/v1/graph", "/v1/chat"} {
				if set.Matches(path) {
					t.Errorf("%s (a launch product) is closed by %s", path, disabledprefixes.EnvVar)
				}
			}
		})
	}
}

// GET /v1/features built from each launch environment's real values: every
// parked product reads as disabled, broadcast channels included (they have no
// gate of their own and are closed by GATEWAY_DISABLED_PREFIXES there), so
// clients hide them. A values edit that opens one changes this map and fails
// here until the expectation is updated on purpose.
func TestLaunchValuesFeaturesMap(t *testing.T) {
	known := prefixesOf(routeDefinitions())
	want := map[string]bool{
		"broadcast_channels": false,
		"communities":        false,
		"groups":             false,
		"qa":                 false,
		"pages":              false,
		"memories":           false,
		"food":               false,
		"rider":              false,
		"dating":             false,
		"doorstep":           false,
	}
	for _, tc := range []struct {
		file       string
		production bool
	}{
		{"values-prod.yaml", true},
		{"values-staging.yaml", false},
		{"values-qa.yaml", false},
	} {
		t.Run(tc.file, func(t *testing.T) {
			env := gatewayEnv(t, tc.file)
			products, err := dormantProductsFromEnv(envOf(env), tc.production)
			if err != nil {
				t.Fatalf("dormantProductsFromEnv: %v", err)
			}
			disabled, err := disabledprefixes.Parse(env[disabledprefixes.EnvVar], known)
			if err != nil {
				t.Fatalf("%s does not boot: %v", disabledprefixes.EnvVar, err)
			}
			core := newCoreHandler(upstreamRoutes(t), nil, false, products, disabled)
			_, got := getFeatures(t, core, nil)
			if len(got) != len(want) {
				t.Fatalf("features keys = %v, want %d keys", sortedKeys(got), len(want))
			}
			for key, enabled := range want {
				if g, ok := got[key]; !ok || g != enabled {
					t.Errorf("%s: %s enabled = %v (present %v), want %v", tc.file, key, g, ok, enabled)
				}
			}
		})
	}
}

// Every parked prefix is a real route in the table, so the list above is
// closing something rather than a name that never routed.
func TestParkedPrefixesAreRoutes(t *testing.T) {
	table := map[string]bool{}
	for _, rd := range routeDefinitions() {
		table[rd.prefix] = true
	}
	for _, prefix := range parkedPrefixes {
		if !table[prefix] {
			t.Errorf("route table has no %s; update parkedPrefixes and the values files", prefix)
		}
	}
}
