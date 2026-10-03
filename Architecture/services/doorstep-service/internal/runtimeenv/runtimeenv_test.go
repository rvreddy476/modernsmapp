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
		{"unset is production (fail closed)", nil, true},
		{"ENV prod", map[string]string{"ENV": "prod"}, true},
		{"APP_ENV production", map[string]string{"APP_ENV": "production"}, true},
		{"staging is production", map[string]string{"ENV": "staging"}, true},
		{"qa is production", map[string]string{"ENV": "qa"}, true},
		{"typo is production", map[string]string{"ENV": "devv"}, true},
		{"ENV dev", map[string]string{"ENV": "dev"}, false},
		{"APP_ENV development", map[string]string{"APP_ENV": "development"}, false},
		{"ENVIRONMENT local padded upper", map[string]string{"ENVIRONMENT": "  LOCAL "}, false},
		{"two development signals", map[string]string{"ENV": "dev", "APP_ENV": "local"}, false},
		// A stale production value must not be talked down by a development one.
		{"any other signal wins", map[string]string{"APP_ENV": "development", "ENV": "prod"}, true},
		{"staging beside dev wins", map[string]string{"DEPLOY_ENV": "staging", "ENV": "dev"}, true},
	} {
		if got := IsProduction(envOf(tc.env)); got != tc.want {
			t.Errorf("%s: IsProduction = %v, want %v", tc.name, got, tc.want)
		}
		if IsDevelopment(envOf(tc.env)) == tc.want {
			t.Errorf("%s: IsDevelopment must be the negation", tc.name)
		}
	}
}

// The detector is only as good as the manifests it reads. Drive it from the
// Helm values that deploy doorstep-service (platform lane L-F). Until they
// exist the test skips; from then on every values file present must resolve
// to production (QA and staging included: Doorstep is fail-closed).
const deployRoot = "../../../../../deploy/services/doorstep-service"

func helmEnv(t *testing.T, path string) map[string]string {
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
		t.Fatalf("read %s: %v", path, err)
	}
	return out
}

func TestHelmValuesResolveProduction(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(deployRoot, "values-*.yaml"))
	if len(files) == 0 {
		t.Skip("deploy/services/doorstep-service values not created yet (platform lane L-F)")
	}
	for _, f := range files {
		if !IsProduction(envOf(helmEnv(t, f))) {
			t.Errorf("%s: IsProduction = false; every deployed environment must be production to doorstep-service", filepath.Base(f))
		}
	}
}
