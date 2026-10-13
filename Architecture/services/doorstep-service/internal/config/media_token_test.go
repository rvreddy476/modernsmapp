package config

import (
	"strings"
	"testing"
)

func TestMediaSigningConfigDoesNotEnablePayments(t *testing.T) {
	env := prodEnv(t)
	env["ENV"] = "local"
	env["DOORSTEP_MEDIA_TOKEN_KEY"] = env["DOORSTEP_SERVICE_TOKEN_KEY"]
	env["DOORSTEP_MEDIA_TOKEN_KID"] = "media1"
	delete(env, "DOORSTEP_SERVICE_TOKEN_KEY")
	delete(env, "DOORSTEP_SERVICE_TOKEN_KID")
	cfg, err := load(env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MediaTokenKey == "" || cfg.MediaTokenKID != "media1" || cfg.ServiceTokenKey != "" {
		t.Fatal("media signing must remain independent of payment signing")
	}
	delete(env, "DOORSTEP_MEDIA_TOKEN_KID")
	if _, err := load(env); err == nil || !strings.Contains(err.Error(), "DOORSTEP_MEDIA_TOKEN_KID") {
		t.Fatal("media key without a key ID must be rejected")
	}
	env["DOORSTEP_MEDIA_TOKEN_KID"] = "media1"
	env["DOORSTEP_MEDIA_TOKEN_KEY"] = "invalid"
	if _, err := load(env); err == nil || !strings.Contains(err.Error(), "DOORSTEP_MEDIA_TOKEN_KEY") {
		t.Fatal("invalid media signing key must be rejected")
	}
}
