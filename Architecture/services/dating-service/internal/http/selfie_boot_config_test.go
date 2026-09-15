package http

import (
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/service"
)

func TestResolveSelfieConfig(t *testing.T) {
	cfg, err := ResolveSelfieConfig(envOf(nil))
	if err != nil || cfg != service.DefaultSelfieConfig() || cfg.PassThreshold != 90 || cfg.ReviewThreshold != 80 ||
		cfg.MaxAttemptsPerDay != 5 || cfg.RequiredBlinks != 2 || cfg.MaxVideoDurationMs != 4000 {
		t.Fatalf("unset = %+v, %v; want 90/80/5/2/4000", cfg, err)
	}
	cfg, err = ResolveSelfieConfig(envOf(map[string]string{
		"DATING_SELFIE_PASS_THRESHOLD": "92", "DATING_SELFIE_REVIEW_THRESHOLD": " 75.5 ", "DATING_SELFIE_MAX_ATTEMPTS_PER_DAY": "3",
		"DATING_SELFIE_REQUIRED_BLINKS": "3", "DATING_SELFIE_MAX_VIDEO_MS": "3500",
	}))
	if err != nil || cfg.PassThreshold != 92 || cfg.ReviewThreshold != 75.5 || cfg.MaxAttemptsPerDay != 3 ||
		cfg.RequiredBlinks != 3 || cfg.MaxVideoDurationMs != 3500 {
		t.Fatalf("set = %+v, %v", cfg, err)
	}
	for name, env := range map[string]map[string]string{
		"pass above 100":      {"DATING_SELFIE_PASS_THRESHOLD": "101"},
		"pass not a number":   {"DATING_SELFIE_PASS_THRESHOLD": "high"},
		"review at pass":      {"DATING_SELFIE_REVIEW_THRESHOLD": "90"},
		"review above pass":   {"DATING_SELFIE_PASS_THRESHOLD": "85", "DATING_SELFIE_REVIEW_THRESHOLD": "86"},
		"attempts zero":       {"DATING_SELFIE_MAX_ATTEMPTS_PER_DAY": "0"},
		"attempts too many":   {"DATING_SELFIE_MAX_ATTEMPTS_PER_DAY": "51"},
		"attempts fractional": {"DATING_SELFIE_MAX_ATTEMPTS_PER_DAY": "2.5"},
		"blinks zero":         {"DATING_SELFIE_REQUIRED_BLINKS": "0"},
		"blinks too many":     {"DATING_SELFIE_REQUIRED_BLINKS": "6"},
		"video too short":     {"DATING_SELFIE_MAX_VIDEO_MS": "500"},
		"video too long":      {"DATING_SELFIE_MAX_VIDEO_MS": "60000"},
	} {
		if _, err := ResolveSelfieConfig(envOf(env)); err == nil || !strings.Contains(err.Error(), "DATING_SELFIE_") {
			t.Fatalf("%s: err=%v, want a DATING_SELFIE_* error", name, err)
		}
	}
}

func TestResolveMediaServiceURL(t *testing.T) {
	if u, err := ResolveMediaServiceURL(envOf(nil)); err != nil || u != "http://media-service:8087" {
		t.Fatalf("unset = %q, %v", u, err)
	}
	if u, err := ResolveMediaServiceURL(envOf(map[string]string{"MEDIA_SERVICE_URL": "http://media-service.atpost.svc.cluster.local:8087/"})); err != nil ||
		u != "http://media-service.atpost.svc.cluster.local:8087" {
		t.Fatalf("set = %q, %v", u, err)
	}
	for _, raw := range []string{"media-service:8087", "ftp://x", "http://"} {
		if _, err := ResolveMediaServiceURL(envOf(map[string]string{"MEDIA_SERVICE_URL": raw})); err == nil {
			t.Fatalf("%q accepted", raw)
		}
	}
}

// The DigiLocker mock never runs outside local/dev (food-service's rule).
func TestResolveDigiLockerMode(t *testing.T) {
	for _, env := range []string{"", "prod", "production", "staging", "test"} {
		if _, _, err := ResolveDigiLockerMode(envOf(map[string]string{"ENV": env, "DIGILOCKER_MODE": "mock"})); err == nil ||
			!strings.Contains(err.Error(), "mock is refused") {
			t.Fatalf("ENV=%q mock: err=%v, want refused", env, err)
		}
		if _, _, err := ResolveDigiLockerMode(envOf(map[string]string{"ENV": env})); err == nil ||
			!strings.Contains(err.Error(), "DIGILOCKER_MODE is required") {
			t.Fatalf("ENV=%q unset: err=%v, want required", env, err)
		}
	}
	for _, env := range []string{"local", "dev", "development"} {
		if mode, warn, err := ResolveDigiLockerMode(envOf(map[string]string{"ENV": env, "DIGILOCKER_MODE": "mock"})); err != nil || mode != DigiLockerModeMock || warn == "" {
			t.Fatalf("ENV=%s mock = %q %q %v", env, mode, warn, err)
		}
		if mode, warn, err := ResolveDigiLockerMode(envOf(map[string]string{"ENV": env})); err != nil || mode != DigiLockerModeMock || warn == "" {
			t.Fatalf("ENV=%s unset = %q %q %v; want mock with a warning", env, mode, warn, err)
		}
	}
	if mode, _, err := ResolveDigiLockerMode(envOf(map[string]string{"ENV": "prod", "DIGILOCKER_MODE": "Disabled"})); err != nil || mode != DigiLockerModeDisabled {
		t.Fatalf("prod disabled = %q %v", mode, err)
	}
	if _, _, err := ResolveDigiLockerMode(envOf(map[string]string{"ENV": "prod", "DIGILOCKER_MODE": "http"})); err == nil ||
		!strings.Contains(err.Error(), "DIGILOCKER_BASE_URL") {
		t.Fatalf("prod http without credentials: err=%v", err)
	}
	if mode, _, err := ResolveDigiLockerMode(envOf(map[string]string{"ENV": "prod", "DIGILOCKER_MODE": "http",
		"DIGILOCKER_BASE_URL": "https://partner.example", "DIGILOCKER_API_KEY": "k"})); err != nil || mode != DigiLockerModeHTTP {
		t.Fatalf("prod http = %q %v", mode, err)
	}
	if _, _, err := ResolveDigiLockerMode(envOf(map[string]string{"ENV": "dev", "DIGILOCKER_MODE": "sandbox"})); err == nil {
		t.Fatalf("unknown mode accepted")
	}
}
