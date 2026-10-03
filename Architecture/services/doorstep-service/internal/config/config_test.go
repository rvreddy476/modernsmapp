package config

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/atpost/shared/kyc"
	"github.com/atpost/shared/servicetoken"
)

func validGSTIN(t *testing.T) string {
	t.Helper()
	base := "36ZZZCZ0000Z1Z"
	d, err := kyc.GSTINCheckDigit(base)
	if err != nil {
		t.Fatal(err)
	}
	return base + string(d)
}

func testKey(b byte) string {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b + byte(i)
	}
	return base64.StdEncoding.EncodeToString(k)
}

// prodEnv is a complete production environment (test values only).
func prodEnv(t *testing.T) map[string]string {
	t.Helper()
	_, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"ENV":                            "prod",
		"POSTGRES_DSN":                   "postgres://u:p@db:5432/app",
		"REDIS_ADDR":                     "redis:6379",
		"KAFKA_BROKERS":                  "b1:9092,b2:9092",
		"INTERNAL_SERVICE_KEY":           "k",
		"REALTIME_TOKEN_SECRET":          "rt",
		"IDENTITY_AUTH_URL":              "http://identity-auth:8081",
		"MEDIA_SERVICE_URL":              "http://media-service:8087",
		"PAYMENTS_SERVICE_URL":           "http://payments-service:8102",
		"DOORSTEP_SERVICE_TOKEN_KEY":     priv,
		"DOORSTEP_SERVICE_TOKEN_KID":     "d1",
		"DOORSTEP_PII_KEYS":              "v1:" + testKey(1),
		"DOORSTEP_PII_LOOKUP_SALT":       "0123456789abcdef-salt",
		"DOORSTEP_PLATFORM_GSTIN":        validGSTIN(t),
		"DIGILOCKER_MODE":                "disabled",
		"DOORSTEP_FACE_COMPARE_MODE":     "http",
		"DOORSTEP_BACKGROUND_CHECK_MODE": "uploaded_document",
		"DOORSTEP_PUBLIC_BASE_URL":       "https://api.example.test",
	}
}

func load(m map[string]string) (Config, error) {
	return FromEnv(func(k string) string { return m[k] })
}

func TestProductionHappyPath(t *testing.T) {
	cfg, err := load(prodEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Production || cfg.HTTPPort != "8122" || cfg.KafkaTopic != "doorstep.events" || len(cfg.KafkaBrokers) != 2 ||
		len(cfg.PIIKeys) != 1 || cfg.SelfieMinSimilarity != 80 || cfg.DigiLockerMode != "disabled" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

// Each production value, removed alone, refuses boot with a message naming
// it, in every non-development environment (staging and QA included).
func TestProductionRefusesMissingValues(t *testing.T) {
	for _, key := range []string{
		"POSTGRES_DSN", "KAFKA_BROKERS", "REDIS_ADDR", "INTERNAL_SERVICE_KEY", "REALTIME_TOKEN_SECRET",
		"IDENTITY_AUTH_URL", "MEDIA_SERVICE_URL", "PAYMENTS_SERVICE_URL", "DOORSTEP_SERVICE_TOKEN_KEY",
		"DOORSTEP_PII_KEYS", "DOORSTEP_PLATFORM_GSTIN",
	} {
		for _, envName := range []string{"prod", "production", "staging", "qa", ""} {
			env := prodEnv(t)
			env["ENV"] = envName
			env[key] = "   "
			if key == "DOORSTEP_SERVICE_TOKEN_KEY" {
				env["DOORSTEP_SERVICE_TOKEN_KID"] = ""
			}
			if key == "DOORSTEP_PII_KEYS" {
				env["DOORSTEP_PII_LOOKUP_SALT"] = ""
			}
			_, err := load(env)
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("ENV=%q without %s: err = %v, want a refusal naming it", envName, key, err)
			}
		}
	}
}

// The mocks verify everything; production refuses each one.
func TestProductionRefusesMocks(t *testing.T) {
	for key, mock := range map[string]string{
		"DIGILOCKER_MODE": "mock", "DOORSTEP_FACE_COMPARE_MODE": "mock", "DOORSTEP_BACKGROUND_CHECK_MODE": "mock",
	} {
		env := prodEnv(t)
		env[key] = mock
		if _, err := load(env); err == nil || !strings.Contains(err.Error(), key+"=mock is refused") {
			t.Errorf("%s=mock in production: %v", key, err)
		}
	}
}

func TestProductionDefaultsAreSafeModes(t *testing.T) {
	env := prodEnv(t)
	delete(env, "DIGILOCKER_MODE")
	delete(env, "DOORSTEP_FACE_COMPARE_MODE")
	delete(env, "DOORSTEP_BACKGROUND_CHECK_MODE")
	cfg, err := load(env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DigiLockerMode != "disabled" || cfg.FaceCompareMode != "http" || cfg.BackgroundCheckMode != "uploaded_document" {
		t.Fatalf("defaults %s/%s/%s", cfg.DigiLockerMode, cfg.FaceCompareMode, cfg.BackgroundCheckMode)
	}
}

func TestDevelopmentRelaxesProductionValues(t *testing.T) {
	cfg, err := load(map[string]string{"ENV": "dev", "POSTGRES_DSN": "postgres://x/app", "INTERNAL_SERVICE_KEY": "devkey"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Production || cfg.PlatformGSTIN != "" || cfg.KafkaBrokers[0] != "redpanda:9092" || cfg.RedisAddr != "redis:6379" ||
		cfg.PaymentsServiceURL != "http://payments-service:8102" || cfg.RealtimeTokenSecret != "devkey" ||
		cfg.DigiLockerMode != "mock" || cfg.FaceCompareMode != "mock" || cfg.BackgroundCheckMode != "mock" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if _, err := load(map[string]string{"ENV": "dev"}); err == nil || !strings.Contains(err.Error(), "POSTGRES_DSN") {
		t.Fatalf("dev without DSN: %v", err)
	}
}

func TestInvalidValuesRefusedEverywhere(t *testing.T) {
	for _, envName := range []string{"dev", "prod"} {
		for key, bad := range map[string]string{
			"DOORSTEP_PLATFORM_GSTIN":        "36ABCDE1234F1Z0",
			"DOORSTEP_SERVICE_TOKEN_KEY":     "not-a-key",
			"DOORSTEP_PII_KEYS":              "v1:short",
			"DOORSTEP_PII_LOOKUP_SALT":       "short",
			"DIGILOCKER_MODE":                "maybe",
			"DOORSTEP_FACE_COMPARE_MODE":     "maybe",
			"DOORSTEP_BACKGROUND_CHECK_MODE": "provider",
			"DOORSTEP_SELFIE_MIN_SIMILARITY": "30",
			"PAYMENTS_SERVICE_URL":           "payments-service:8102",
			"DOORSTEP_PRO_APP_LINK_URL":      "not a url",
			"DOORSTEP_QUOTE_TTL_MINUTES":     "0",
		} {
			env := prodEnv(t)
			env["ENV"] = envName
			env[key] = bad
			if _, err := load(env); err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("ENV=%s %s=%q: %v, want a refusal naming it", envName, key, bad, err)
			}
		}
	}
	// Half a pair is refused.
	env := prodEnv(t)
	env["ENV"] = "dev"
	env["DOORSTEP_SERVICE_TOKEN_KID"] = ""
	if _, err := load(env); err == nil || !strings.Contains(err.Error(), "set together") {
		t.Fatalf("half token pair: %v", err)
	}
	// DigiLocker http needs its credentials.
	env = prodEnv(t)
	env["DIGILOCKER_MODE"] = "http"
	if _, err := load(env); err == nil || !strings.Contains(err.Error(), "DIGILOCKER_CLIENT_ID") {
		t.Fatalf("digilocker http without credentials: %v", err)
	}
	// Production links must be https.
	env = prodEnv(t)
	env["DOORSTEP_PUBLIC_BASE_URL"] = "http://api.example.test"
	if _, err := load(env); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("http public base URL in production: %v", err)
	}
}

func TestErrorsNeverEchoSecrets(t *testing.T) {
	env := prodEnv(t)
	env["DOORSTEP_PII_KEYS"] = "v1:SECRETSECRETSECRET"
	env["DOORSTEP_SERVICE_TOKEN_KEY"] = "SECRETTOKENVALUE"
	_, err := load(env)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error echoes a secret or is nil: %v", err)
	}
}

func TestDevSeedOnlyInDevelopment(t *testing.T) {
	for _, envName := range []string{"prod", "staging", "qa", ""} {
		env := prodEnv(t)
		env["ENV"] = envName
		env["DOORSTEP_DEV_SEED"] = "true"
		if _, err := load(env); err == nil || !strings.Contains(err.Error(), "DOORSTEP_DEV_SEED") {
			t.Errorf("ENV=%q with the dev seed: %v, want a refusal", envName, err)
		}
	}
	for _, envName := range []string{"dev", "local", "development"} {
		cfg, err := load(map[string]string{"ENV": envName, "POSTGRES_DSN": "x", "DOORSTEP_DEV_SEED": "true"})
		if err != nil || !cfg.DevSeed {
			t.Errorf("ENV=%s dev seed: cfg=%+v err=%v", envName, cfg, err)
		}
	}
}

func TestQuoteTTL(t *testing.T) {
	cfg, err := load(map[string]string{"ENV": "dev", "POSTGRES_DSN": "x", "DOORSTEP_QUOTE_TTL_MINUTES": "30"})
	if err != nil || cfg.QuoteTTL.Minutes() != 30 {
		t.Fatalf("ttl: %v %v", cfg.QuoteTTL, err)
	}
}
