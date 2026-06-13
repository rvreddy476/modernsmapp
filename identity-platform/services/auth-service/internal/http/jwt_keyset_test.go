package http

import "testing"

// C7 — kid-based secret resolution underlies the zero-downtime rotation
// story for JWT_SECRET. These tests pin the semantics so a refactor can't
// silently regress to "ignore kid, use active secret unconditionally".
func TestJWTKeySetSecretFor(t *testing.T) {
	full := JWTKeySet{
		ActiveKID:      "v2",
		ActiveSecret:   "new",
		PreviousKID:    "v1",
		PreviousSecret: "old",
	}
	cases := []struct {
		name     string
		keys     JWTKeySet
		kid      string
		wantOK   bool
		wantSec  string
	}{
		{"empty kid falls back to active", full, "", true, "new"},
		{"matching active kid picks active", full, "v2", true, "new"},
		{"matching previous kid picks previous", full, "v1", true, "old"},
		{"unknown kid is rejected", full, "v9", false, ""},
		{"single-key set still accepts legacy (no kid)", JWTKeySet{ActiveSecret: "only"}, "", true, "only"},
		{"single-key set rejects an unknown kid", JWTKeySet{ActiveSecret: "only"}, "v1", false, ""},
		{"previous secret empty disables previous", JWTKeySet{ActiveKID: "v2", ActiveSecret: "new", PreviousKID: "v1"}, "v1", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.keys.secretFor(tc.kid)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v", ok, tc.wantOK)
			}
			if ok && string(got) != tc.wantSec {
				t.Fatalf("secret=%q want %q", string(got), tc.wantSec)
			}
		})
	}
}
