package config

import (
	"errors"
	"strings"
	"testing"
)

func TestCallerApplicationsParsesEachCaller(t *testing.T) {
	got := CallerApplications(envMap(map[string]string{
		"SERVICE_CALLERS": " commerce-service , food-service,,",
		"SERVICE_CALLER_COMMERCE_SERVICE_APPLICATIONS": "mstore",
		"SERVICE_CALLER_FOOD_SERVICE_APPLICATIONS":     " feast , ",
	}))
	if len(got) != 2 || strings.Join(got["commerce-service"], ",") != "mstore" || strings.Join(got["food-service"], ",") != "feast" {
		t.Fatalf("allowlist = %v", got)
	}
	if got := CallerApplications(envMap(map[string]string{"SERVICE_CALLERS": "wallet-service"})); len(got["wallet-service"]) != 0 {
		t.Fatalf("a caller with no list = %v, want empty", got)
	}
}

// Boot: a caller allowlist that names an application the registry does not hold,
// or names none, refuses to start in production.
func TestValidateCallerApplications(t *testing.T) {
	registry := map[string]string{"mstore": "active", "feast": "active", "vtube": "disabled"}

	t.Run("a registered, active allowlist is valid everywhere", func(t *testing.T) {
		for _, prod := range []bool{true, false} {
			w, err := ValidateCallerApplications(map[string][]string{"commerce-service": {"mstore"}, "food-service": {"feast"}}, registry, prod)
			if err != nil || len(w) != 0 {
				t.Fatalf("production=%v: warnings=%v err=%v", prod, w, err)
			}
		}
	})
	t.Run("production refuses an unknown application key", func(t *testing.T) {
		_, err := ValidateCallerApplications(map[string][]string{"food-service": {"feast", "dating"}}, registry, true)
		if !errors.Is(err, ErrCallerApplications) || !strings.Contains(err.Error(), `"dating"`) {
			t.Fatalf("err = %v, want ErrCallerApplications naming dating", err)
		}
	})
	t.Run("production refuses a malformed key", func(t *testing.T) {
		_, err := ValidateCallerApplications(map[string][]string{"food-service": {"Feast"}}, map[string]string{"Feast": "active"}, true)
		if !errors.Is(err, ErrCallerApplications) {
			t.Fatalf("err = %v, want ErrCallerApplications", err)
		}
	})
	t.Run("production refuses an empty list", func(t *testing.T) {
		_, err := ValidateCallerApplications(map[string][]string{"commerce-service": nil}, registry, true)
		if !errors.Is(err, ErrCallerApplications) {
			t.Fatalf("err = %v, want ErrCallerApplications", err)
		}
	})
	t.Run("outside production the same problems are warnings", func(t *testing.T) {
		w, err := ValidateCallerApplications(map[string][]string{"commerce-service": nil, "food-service": {"dating"}}, registry, false)
		if err != nil || len(w) != 2 {
			t.Fatalf("warnings=%v err=%v, want two warnings and no error", w, err)
		}
	})
	t.Run("a disabled application is a warning even in production", func(t *testing.T) {
		w, err := ValidateCallerApplications(map[string][]string{"tube-service": {"vtube"}}, registry, true)
		if err != nil || len(w) != 1 || !strings.Contains(w[0], "disabled") {
			t.Fatalf("warnings=%v err=%v, want one disabled warning and no error", w, err)
		}
	})
}
