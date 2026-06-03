package main

import "testing"

// C7 — kid-based secret resolution underlies the zero-downtime rotation
// story for JWT_SECRET at the api-gateway. Same contract as the identity
// platform package; tested here so the gateway-tier copy can't silently
// drift.
func TestJWTKeySetSecretFor(t *testing.T) {
	full := jwtKeySet{
		activeKID:      "v2",
		activeSecret:   "new",
		previousKID:    "v1",
		previousSecret: "old",
	}
	cases := []struct {
		name    string
		keys    jwtKeySet
		kid     string
		wantOK  bool
		wantSec string
	}{
		{"empty kid falls back to active", full, "", true, "new"},
		{"matching active kid picks active", full, "v2", true, "new"},
		{"matching previous kid picks previous", full, "v1", true, "old"},
		{"unknown kid is rejected", full, "v9", false, ""},
		{"single-key set accepts legacy (no kid)", jwtKeySet{activeSecret: "only"}, "", true, "only"},
		{"single-key set rejects unknown kid", jwtKeySet{activeSecret: "only"}, "v1", false, ""},
		{"empty previous secret disables previous", jwtKeySet{activeKID: "v2", activeSecret: "new", previousKID: "v1"}, "v1", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.keys.secretFor(tc.kid)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v", ok, tc.wantOK)
			}
			if ok && got != tc.wantSec {
				t.Fatalf("secret=%q want %q", got, tc.wantSec)
			}
		})
	}
}
