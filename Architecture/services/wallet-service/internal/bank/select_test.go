package bank

import (
	"strings"
	"testing"
)

func envOf(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestFromEnv_MockOnlyOnDevEnvironments(t *testing.T) {
	for _, env := range []string{"local", "dev", "development", "test", "DEV"} {
		c, name, err := FromEnv(envOf(map[string]string{"BANK_PARTNER": "mock", "ENV": env}))
		if err != nil || name != "mock" {
			t.Fatalf("ENV=%s: mock refused: %v", env, err)
		}
		if _, ok := c.(*MockClient); !ok {
			t.Fatalf("ENV=%s: got %T, want *MockClient", env, c)
		}
	}
	// An empty BANK_PARTNER means mock, and only on dev.
	if _, name, err := FromEnv(envOf(map[string]string{"ENV": "dev"})); err != nil || name != "mock" {
		t.Fatalf("empty BANK_PARTNER on dev: %q %v", name, err)
	}
	if _, _, err := FromEnv(envOf(map[string]string{"APP_ENV": "test"})); err != nil {
		t.Fatalf("APP_ENV=test should count as dev: %v", err)
	}
}

func TestFromEnv_RefusesMockOutsideDev(t *testing.T) {
	cases := []map[string]string{
		{"BANK_PARTNER": "mock"},                                  // blank env is production
		{"BANK_PARTNER": "mock", "ENV": "prod"},                   // the prod/QA deploy values
		{"BANK_PARTNER": "mock", "ENV": "staging"},
		{"BANK_PARTNER": "mock", "ENV": "qa"},
		{"BANK_PARTNER": "mock", "ENV": "dev", "APP_ENV": "production"}, // any non-dev value wins
		{"BANK_PARTNER": "", "ENV": "prod"},
		{"BANK_PARTNER": ""},
	}
	for _, kv := range cases {
		c, _, err := FromEnv(envOf(kv))
		if err == nil || c != nil {
			t.Errorf("%v: mock must be refused, got client %T err %v", kv, c, err)
		}
	}
}

func TestFromEnv_RefusesUnknownPartner(t *testing.T) {
	for _, p := range []string{"hdfc", "icic", "Mock2", "none"} {
		c, _, err := FromEnv(envOf(map[string]string{"BANK_PARTNER": p, "ENV": "dev"}))
		if err == nil || c != nil {
			t.Errorf("BANK_PARTNER=%s must refuse to boot, got %T", p, c)
		}
		if err != nil && !strings.Contains(err.Error(), "BANK_PARTNER") {
			t.Errorf("BANK_PARTNER=%s: error should name the variable: %v", p, err)
		}
	}
}

func TestFromEnv_ICICI(t *testing.T) {
	c, name, err := FromEnv(envOf(map[string]string{"BANK_PARTNER": "icici", "ENV": "prod", "ICICI_BASE_URL": "https://bank.example"}))
	if err != nil || name != "icici" {
		t.Fatalf("icici refused: %v", err)
	}
	if _, ok := c.(*HTTPClient); !ok {
		t.Fatalf("got %T, want *HTTPClient", c)
	}
	if _, _, err := FromEnv(envOf(map[string]string{"BANK_PARTNER": "icici", "ENV": "prod"})); err == nil {
		t.Fatalf("icici without ICICI_BASE_URL must refuse to boot")
	}
}
