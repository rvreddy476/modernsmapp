package config

// Momentum Premium (migration 019): monetization-service is allowed both of
// its applications, no other caller may open premium, and production refuses
// to boot with premium allowed until 019 has seeded the registry row.

import (
	"errors"
	"strings"
	"testing"
)

func TestPremiumCallerApplications(t *testing.T) {
	allow := CallerApplications(envMap(map[string]string{
		"SERVICE_CALLERS":                                  "food-service,monetization-service",
		"SERVICE_CALLER_FOOD_SERVICE_APPLICATIONS":         "feast",
		"SERVICE_CALLER_MONETIZATION_SERVICE_APPLICATIONS": "memberships,premium",
	}))
	if !Allows(allow["monetization-service"], "premium") || !Allows(allow["monetization-service"], "memberships") {
		t.Fatalf("monetization-service applications = %v", allow["monetization-service"])
	}
	if Allows(allow["food-service"], "premium") {
		t.Fatalf("food-service may open premium: %v", allow)
	}
	before019 := map[string]string{"feast": "active", "memberships": "active"}
	if _, err := ValidateCallerApplications(allow, before019, true); !errors.Is(err, ErrCallerApplications) || !strings.Contains(err.Error(), `"premium"`) {
		t.Fatalf("production before 019: err = %v, want ErrCallerApplications naming premium", err)
	}
	after019 := map[string]string{"feast": "active", "memberships": "active", "premium": "active"}
	if w, err := ValidateCallerApplications(allow, after019, true); err != nil || len(w) != 0 {
		t.Fatalf("production after 019: warnings=%v err=%v", w, err)
	}
}
