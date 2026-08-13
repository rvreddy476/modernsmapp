package edgeauth_test

import (
	"os"
	"testing"

	"github.com/atpost/api-gateway/pkg/tokenpolicy"
	"github.com/atpost/identity-auth-service/pkg/appenv"
)

// Module 3 LB-1 — production detection must be IDENTICAL on both sides.
//
// This moved here from identity-auth-service's own test tree for the same
// reason as the contract test: it imports the gateway, and a production module
// must not.
//
// The failure it guards is not hypothetical. The Helm values set
// `env: ENV: prod`, and both services originally read only APP_ENV and
// ENVIRONMENT — so PRODUCTION RAN DEVELOPMENT TOKEN RULES: HS256 accepted,
// issuer unpinned, in the one environment the hardening exists for.
//
// If the two implementations ever disagree, one hardens and the other does
// not, and every token is rejected at the edge. Neither service can see that
// alone, which is why both are driven here from the same environment.

func TestProductionDetectionMatchesAcrossServices(t *testing.T) {
	cases := []struct {
		name string
		envs map[string]string
		want bool
	}{
		{"nothing set", map[string]string{}, false},
		{"APP_ENV=production", map[string]string{"APP_ENV": "production"}, true},
		{"APP_ENV=prod", map[string]string{"APP_ENV": "prod"}, true},
		{"APP_ENV=PROD mixed case", map[string]string{"APP_ENV": "PROD"}, true},
		{"APP_ENV padded", map[string]string{"APP_ENV": "  production  "}, true},
		{"APP_ENV=development", map[string]string{"APP_ENV": "development"}, false},
		{"APP_ENV=staging", map[string]string{"APP_ENV": "staging"}, false},
		{"ENVIRONMENT=production", map[string]string{"ENVIRONMENT": "production"}, true},
		// What the Helm charts actually set today. This is the case that was
		// broken.
		{"ENV=prod (helm)", map[string]string{"ENV": "prod"}, true},
		{"ENV=staging (helm)", map[string]string{"ENV": "staging"}, false},
		// Precedence: an explicit APP_ENV must beat a stale ENV.
		{"APP_ENV=development beats ENV=prod", map[string]string{"APP_ENV": "development", "ENV": "prod"}, false},
		{"APP_ENV=production beats ENV=staging", map[string]string{"APP_ENV": "production", "ENV": "staging"}, true},
		{"ENVIRONMENT=production beats ENV=dev", map[string]string{"ENVIRONMENT": "production", "ENV": "dev"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
				if v, ok := tc.envs[key]; ok {
					t.Setenv(key, v)
				} else {
					// t.Setenv restores on cleanup; Unsetenv guards against a
					// value inherited from the CI runner.
					t.Setenv(key, "")
					_ = os.Unsetenv(key)
				}
			}

			mint := appenv.IsProduction()
			edge := tokenpolicy.IsProductionEnv()

			if mint != tc.want {
				t.Errorf("auth-service IsProductionEnv()=%v want %v", mint, tc.want)
			}
			if edge != tc.want {
				t.Errorf("gateway IsProductionEnv()=%v want %v", edge, tc.want)
			}
			if mint != edge {
				t.Fatalf("MINT AND EDGE DISAGREE (mint=%v edge=%v): the gateway would "+
					"reject every token this service mints", mint, edge)
			}
		})
	}
}

// The variable list itself must match. A divergence here produces the
// disagreement above for some environment the table does not happen to cover.
func TestProductionEnvVarListIsShared(t *testing.T) {
	want := appenv.ProductionEnvVars
	got := tokenpolicy.ProductionEnvVars
	if len(got) != len(want) {
		t.Fatalf("gateway ProductionEnvVars = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("gateway ProductionEnvVars = %v, want %v (order matters: an "+
				"explicit higher-priority value must win)", got, want)
		}
	}
}
