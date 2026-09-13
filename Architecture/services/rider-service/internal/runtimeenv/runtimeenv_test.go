package runtimeenv

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestIsProduction(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"unset", nil, false},
		{"ENV prod", map[string]string{"ENV": "prod"}, true},
		{"APP_ENV production", map[string]string{"APP_ENV": "production"}, true},
		{"ENVIRONMENT padded upper", map[string]string{"ENVIRONMENT": "  PROD "}, true},
		{"DEPLOY_ENV production", map[string]string{"DEPLOY_ENV": "Production"}, true},
		{"staging", map[string]string{"ENV": "staging"}, false},
		{"development", map[string]string{"APP_ENV": "development"}, false},
		{"prefix is not a match", map[string]string{"ENV": "production-like"}, false},
		// Fail-closed guards harden on any production signal: a stale ENV=prod
		// must not be talked down by a development value elsewhere.
		{"any production signal wins", map[string]string{"APP_ENV": "development", "ENV": "prod"}, true},
	} {
		if got := IsProduction(envOf(tc.env)); got != tc.want {
			t.Errorf("%s: IsProduction = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The detector is only as good as the manifests it reads. Drive it from the
// Helm values that actually deploy rider-service.
const deployRoot = "../../../../../deploy/services/rider-service"

func helmEnv(t *testing.T, file string) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join(deployRoot, file))
	if err != nil {
		t.Fatalf("open %s: %v", file, err)
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
			inEnv = trimmed == "env:"
			continue
		}
		if !inEnv {
			continue
		}
		if key, value, ok := strings.Cut(trimmed, ":"); ok {
			out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return out
}

func TestHelmValuesResolveProduction(t *testing.T) {
	for file, want := range map[string]bool{
		"values-prod.yaml":          true,
		"values-azure-prod.yaml":    true,
		"values-staging.yaml":       false,
		"values-azure-staging.yaml": false,
	} {
		if got := IsProduction(envOf(helmEnv(t, file))); got != want {
			t.Errorf("%s: IsProduction = %v, want %v", file, got, want)
		}
	}
}
