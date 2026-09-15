package http

import (
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveOpenMatchDedupePolicy(t *testing.T) {
	cases := []struct {
		env, flag   string
		wantAllowed bool
		wantErr     bool
	}{
		{env: "dev", wantAllowed: true},
		{env: "local", flag: "false", wantAllowed: true},
		{env: "development", wantAllowed: true},
		{env: "prod", wantAllowed: false},
		{env: "staging", flag: "false", wantAllowed: false},
		{env: "", wantAllowed: false},
		{env: "prod", flag: "true", wantAllowed: true},
		{env: "staging", flag: "TRUE", wantAllowed: true},
		{env: "prod", flag: "yes", wantErr: true},
	}
	for _, tc := range cases {
		policy, err := ResolveOpenMatchDedupePolicy(envOf(map[string]string{"ENV": tc.env, "DATING_DEDUPE_OPEN_MATCHES": tc.flag}))
		if tc.wantErr {
			if err == nil || !strings.Contains(err.Error(), "DATING_DEDUPE_OPEN_MATCHES") {
				t.Fatalf("ENV=%q flag=%q: err=%v, want a DATING_DEDUPE_OPEN_MATCHES error", tc.env, tc.flag, err)
			}
			continue
		}
		if err != nil || policy.CloseAllowed() != tc.wantAllowed {
			t.Fatalf("ENV=%q flag=%q: allowed=%v err=%v, want %v", tc.env, tc.flag, policy.CloseAllowed(), err, tc.wantAllowed)
		}
	}
}

func TestResolveDeclineCooldown(t *testing.T) {
	day := 24 * time.Hour
	if d, err := ResolveDeclineCooldown(envOf(nil)); err != nil || d != store.DeclineCooldown || d != 30*day {
		t.Fatalf("unset = %v, %v; want the 30-day default", d, err)
	}
	for raw, want := range map[string]time.Duration{"1": day, "7": 7 * day, " 45 ": 45 * day, "365": 365 * day} {
		if d, err := ResolveDeclineCooldown(envOf(map[string]string{"DATING_DECLINE_COOLDOWN_DAYS": raw})); err != nil || d != want {
			t.Fatalf("%q = %v, %v; want %v", raw, d, err, want)
		}
	}
	for _, raw := range []string{"0", "-1", "366", "abc", "1.5", "30d"} {
		if _, err := ResolveDeclineCooldown(envOf(map[string]string{"DATING_DECLINE_COOLDOWN_DAYS": raw})); err == nil ||
			!strings.Contains(err.Error(), "DATING_DECLINE_COOLDOWN_DAYS") {
			t.Fatalf("%q: err=%v, want a validation error", raw, err)
		}
	}
}
