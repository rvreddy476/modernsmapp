package http

import "testing"

// Android dating audit M3: screen protection defaults ON in every
// environment, production included; only an explicit false turns it off.
func TestScreenProtectionDefaultsOn(t *testing.T) {
	for _, env := range []string{"", "prod", "production", "staging", "dev", "local"} {
		cfg, err := ResolveMechanicsConfig(envOf(map[string]string{"ENV": env}))
		if err != nil || !cfg.ScreenProtection {
			t.Fatalf("ENV=%q: screen protection = %v err=%v, want on by default", env, cfg.ScreenProtection, err)
		}
	}
	cfg, err := ResolveMechanicsConfig(envOf(map[string]string{"ENV": "prod", "DATING_SCREEN_PROTECTION_ENABLED": "false"}))
	if err != nil || cfg.ScreenProtection {
		t.Fatalf("explicit false = %v err=%v, want off", cfg.ScreenProtection, err)
	}
	if _, err := ResolveMechanicsConfig(envOf(map[string]string{"ENV": "prod", "DATING_SCREEN_PROTECTION_ENABLED": "maybe"})); err == nil {
		t.Fatal("a malformed value must refuse boot, not read as off")
	}
}
