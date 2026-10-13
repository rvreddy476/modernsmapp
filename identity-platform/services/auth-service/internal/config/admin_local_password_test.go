package config

import (
	"strings"
	"testing"
)

func TestAdminLocalPasswordLoginFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		env, key            string
		production, allowed bool
	}{
		{"local", strings.Repeat("k", 32), false, true},
		{"local", "short", false, false},
		{"dev", strings.Repeat("k", 32), false, false},
		{"staging", strings.Repeat("k", 32), false, false},
		{"production", strings.Repeat("k", 32), true, false},
		{"local", strings.Repeat("k", 32), true, false},
		{"", strings.Repeat("k", 32), false, false},
	} {
		cfg := Config{RuntimeEnvironment: tc.env, AdminLocalPasswordLoginKey: tc.key, Production: tc.production}
		if cfg.AdminLocalPasswordLoginEnabled() != tc.allowed {
			t.Fatalf("env=%q production=%v: incorrect local login policy", tc.env, tc.production)
		}
		if (cfg.ValidateForProduction() == nil) != tc.allowed {
			t.Fatalf("env=%q production=%v: unsafe key accepted at boot", tc.env, tc.production)
		}
	}
	if err := (&Config{}).ValidateAdminLocalPasswordLogin(); err != nil {
		t.Fatalf("disabled by default: %v", err)
	}
}
