package config

import "testing"

// PAYMENTS_OFFERS_ENABLED: absent is OFF, and only the literal "true" turns
// bank offers on.
func TestResolveOffersEnabled(t *testing.T) {
	for raw, want := range map[string]bool{
		"": false, "true": true, " true ": true, "TRUE": false, "1": false, "yes": false, "false": false,
	} {
		env := map[string]string{"PAYMENTS_ALLOW_STUB": "true"}
		if raw != "" {
			env["PAYMENTS_OFFERS_ENABLED"] = raw
		}
		cfg, err := Resolve(envMap(env))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.OffersEnabled != want {
			t.Errorf("PAYMENTS_OFFERS_ENABLED=%q → %v, want %v", raw, cfg.OffersEnabled, want)
		}
	}
}
