package config

// Doorstep (migration 015): the caller entry the deploy values declare
// (SERVICE_CALLER_DOORSTEP_SERVICE_APPLICATIONS=doorstep) parses to exactly
// `doorstep`, and production refuses to boot with it until 015 has seeded the
// registry row — the order the values files spell out.

import (
	"errors"
	"strings"
	"testing"
)

func TestDoorstepCallerApplications(t *testing.T) {
	allow := CallerApplications(envMap(map[string]string{
		"SERVICE_CALLERS": "commerce-service,food-service,doorstep-service",
		"SERVICE_CALLER_COMMERCE_SERVICE_APPLICATIONS": "mstore",
		"SERVICE_CALLER_FOOD_SERVICE_APPLICATIONS":     "feast",
		"SERVICE_CALLER_DOORSTEP_SERVICE_APPLICATIONS": "doorstep",
	}))
	if got := strings.Join(allow["doorstep-service"], ","); got != "doorstep" {
		t.Fatalf("doorstep-service applications = %q, want doorstep", got)
	}
	if Allows(allow["doorstep-service"], "feast") || Allows(allow["food-service"], "doorstep") {
		t.Fatalf("allowlist leaks across callers: %v", allow)
	}

	before015 := map[string]string{"mstore": "active", "feast": "active", "dating": "active", "mopedu": "active"}
	if _, err := ValidateCallerApplications(allow, before015, true); !errors.Is(err, ErrCallerApplications) || !strings.Contains(err.Error(), `"doorstep"`) {
		t.Fatalf("production before 015: err = %v, want ErrCallerApplications naming doorstep", err)
	}
	after015 := map[string]string{"mstore": "active", "feast": "active", "dating": "active", "mopedu": "active", "doorstep": "active"}
	if w, err := ValidateCallerApplications(allow, after015, true); err != nil || len(w) != 0 {
		t.Fatalf("production after 015: warnings=%v err=%v", w, err)
	}
}
