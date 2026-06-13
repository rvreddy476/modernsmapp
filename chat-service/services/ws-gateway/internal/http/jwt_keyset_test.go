package http

import "testing"

// C7 Tier 2 — kid-based secret resolution. Mirrors the auth-service and
// api-gateway tests so this per-service copy can't silently drift.
// Note: ws-gateway's empty-active-secret semantics differ from peers —
// secretFor("") returns false when the active secret is empty (rather
// than returning ([]byte{}, true)) because every JWT verifier in this
// service goes through here and an empty active secret means "auth not
// configured", which the upstream authenticate helper handles by
// short-circuiting with "jwt secret not configured".
func TestJWTKeySetSecretFor(t *testing.T) {
	full := JWTKeySet{
		ActiveKID:      "v2",
		ActiveSecret:   "new",
		PreviousKID:    "v1",
		PreviousSecret: "old",
	}
	cases := []struct {
		name    string
		keys    JWTKeySet
		kid     string
		wantOK  bool
		wantSec string
	}{
		{"empty kid falls back to active", full, "", true, "new"},
		{"matching active kid picks active", full, "v2", true, "new"},
		{"matching previous kid picks previous", full, "v1", true, "old"},
		{"unknown kid is rejected", full, "v9", false, ""},
		{"single-key set accepts legacy (no kid)", JWTKeySet{ActiveSecret: "only"}, "", true, "only"},
		{"single-key set rejects unknown kid", JWTKeySet{ActiveSecret: "only"}, "v1", false, ""},
		{"empty previous secret disables previous", JWTKeySet{ActiveKID: "v2", ActiveSecret: "new", PreviousKID: "v1"}, "v1", false, ""},
		{"empty active secret refuses all (auth disabled)", JWTKeySet{}, "", false, ""},
		{"whitespace-only active secret refuses all", JWTKeySet{ActiveSecret: "   "}, "", false, ""},
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
