package config

import (
	"errors"
	"strings"
	"testing"
)

// prodMintingConfig is a Config that passes the token-minting half of
// ValidateForProduction, so the tests below isolate the OTP bypass gate.
func prodMintingConfig() *Config {
	return &Config{
		Production:               true,
		JWTIssuer:                "auth-service",
		JWTAudience:              "atpost-api",
		AccessTokenPrivateKeyPEM: "-----BEGIN RSA PRIVATE KEY-----\nnot-a-real-key\n-----END RSA PRIVATE KEY-----",
	}
}

func TestOTPBypass_ProductionRefusesToBoot(t *testing.T) {
	cfg := prodMintingConfig()
	cfg.OTPBypassCode = "123456"

	err := cfg.ValidateOTPBypass()
	if !errors.Is(err, ErrOTPBypassInProduction) {
		t.Fatalf("ValidateOTPBypass() = %v, want ErrOTPBypassInProduction", err)
	}
	if !strings.Contains(err.Error(), "OTP_BYPASS_CODE") || !strings.Contains(err.Error(), "refusing to start") {
		t.Fatalf("message must name the variable and say it refuses to start: %q", err.Error())
	}
	if strings.Contains(err.Error(), "123456") {
		t.Fatalf("message must not echo the bypass code: %q", err.Error())
	}

	// The single boot gate main calls must surface the same refusal.
	if err := cfg.ValidateForProduction(); !errors.Is(err, ErrOTPBypassInProduction) {
		t.Fatalf("ValidateForProduction() = %v, want ErrOTPBypassInProduction", err)
	}
}

func TestOTPBypass_ProductionWhitespaceOnlyIsUnset(t *testing.T) {
	cfg := prodMintingConfig()
	cfg.OTPBypassCode = "   "
	if err := cfg.ValidateForProduction(); err != nil {
		t.Fatalf("whitespace-only bypass code should count as unset: %v", err)
	}
	if cfg.OTPBypassActive() {
		t.Fatal("whitespace-only bypass code reported active")
	}
}

func TestOTPBypass_ProductionWithoutCodeBoots(t *testing.T) {
	cfg := prodMintingConfig()
	if err := cfg.ValidateForProduction(); err != nil {
		t.Fatalf("production without OTP_BYPASS_CODE must boot: %v", err)
	}
	if msg, ok := cfg.OTPBypassBootWarning(); ok || msg != "" {
		t.Fatalf("no warning expected when unset, got %q", msg)
	}
}

func TestOTPBypass_NonProductionWarnsLoudly(t *testing.T) {
	cfg := &Config{Production: false, OTPBypassCode: "000000"}
	if err := cfg.ValidateOTPBypass(); err != nil {
		t.Fatalf("non-production must not refuse: %v", err)
	}
	if err := cfg.ValidateForProduction(); err != nil {
		t.Fatalf("non-production ValidateForProduction must be nil: %v", err)
	}
	msg, ok := cfg.OTPBypassBootWarning()
	if !ok {
		t.Fatal("expected a boot warning when OTP_BYPASS_CODE is set outside production")
	}
	if !strings.Contains(msg, "OTP_BYPASS_CODE") {
		t.Fatalf("warning must name the variable: %q", msg)
	}
	if strings.Contains(msg, "000000") {
		t.Fatalf("warning must not echo the code: %q", msg)
	}
}

func TestOTPBypass_NonProductionUnsetIsSilent(t *testing.T) {
	cfg := &Config{Production: false}
	if msg, ok := cfg.OTPBypassBootWarning(); ok || msg != "" {
		t.Fatalf("no warning expected when unset, got %q", msg)
	}
}

// TestOTPBypass_LoadWiresProductionFromEnv: the refusal must trigger from
// the real environment variables, not only from a hand-built Config.
func TestOTPBypass_LoadWiresProductionFromEnv(t *testing.T) {
	for _, k := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		t.Setenv(k, "")
	}
	t.Setenv("APP_ENV", "production")
	t.Setenv("OTP_BYPASS_CODE", "424242")
	t.Setenv("JWT_ISSUER", "auth-service")
	t.Setenv("JWT_AUDIENCE", "atpost-api")
	t.Setenv("JWT_PRIVATE_KEY_PEM", "pem")

	cfg := Load()
	if !cfg.Production {
		t.Fatal("APP_ENV=production should mark the config as production")
	}
	if err := cfg.ValidateForProduction(); !errors.Is(err, ErrOTPBypassInProduction) {
		t.Fatalf("Load()+ValidateForProduction = %v, want ErrOTPBypassInProduction", err)
	}

	t.Setenv("APP_ENV", "development")
	cfg = Load()
	if err := cfg.ValidateForProduction(); err != nil {
		t.Fatalf("development must boot with the bypass set: %v", err)
	}
	if _, ok := cfg.OTPBypassBootWarning(); !ok {
		t.Fatal("development must warn when the bypass is set")
	}
}
