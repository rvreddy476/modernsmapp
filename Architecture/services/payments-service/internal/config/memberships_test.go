package config

// Creator memberships (migration 016): the caller entry the deploy values
// declare (SERVICE_CALLER_MONETIZATION_SERVICE_APPLICATIONS=memberships)
// parses to exactly `memberships`, and production refuses to boot with it
// until 016 has seeded the registry row — the order the values files spell out.

import (
	"errors"
	"strings"
	"testing"
)

func TestMembershipsCallerApplications(t *testing.T) {
	allow := CallerApplications(envMap(map[string]string{
		"SERVICE_CALLERS": "commerce-service,food-service,doorstep-service,monetization-service",
		"SERVICE_CALLER_COMMERCE_SERVICE_APPLICATIONS":     "mstore",
		"SERVICE_CALLER_FOOD_SERVICE_APPLICATIONS":         "feast",
		"SERVICE_CALLER_DOORSTEP_SERVICE_APPLICATIONS":     "doorstep",
		"SERVICE_CALLER_MONETIZATION_SERVICE_APPLICATIONS": "memberships",
	}))
	if got := strings.Join(allow["monetization-service"], ","); got != "memberships" {
		t.Fatalf("monetization-service applications = %q, want memberships", got)
	}
	for caller, apps := range allow {
		if caller != "monetization-service" && Allows(apps, "memberships") {
			t.Fatalf("%s may open memberships payments: %v", caller, allow)
		}
	}
	if Allows(allow["monetization-service"], "feast") || Allows(allow["monetization-service"], "doorstep") {
		t.Fatalf("allowlist leaks across callers: %v", allow)
	}

	before016 := map[string]string{"mstore": "active", "feast": "active", "dating": "active", "mopedu": "active", "doorstep": "active"}
	if _, err := ValidateCallerApplications(allow, before016, true); !errors.Is(err, ErrCallerApplications) || !strings.Contains(err.Error(), `"memberships"`) {
		t.Fatalf("production before 016: err = %v, want ErrCallerApplications naming memberships", err)
	}
	after016 := map[string]string{"mstore": "active", "feast": "active", "dating": "active", "mopedu": "active", "doorstep": "active", "memberships": "active"}
	if w, err := ValidateCallerApplications(allow, after016, true); err != nil || len(w) != 0 {
		t.Fatalf("production after 016: warnings=%v err=%v", w, err)
	}
}
