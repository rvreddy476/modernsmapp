package main

import (
	"errors"
	"testing"
)

func envOf(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

// X-L1: a production boot without the internal key must refuse, matching
// group-service; anything else boots.
func TestInternalKeyForBoot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     map[string]string
		wantKey string
		refused bool
	}{
		{"production without key", map[string]string{"APP_ENV": "production"}, "", true},
		{"prod spelling, any case", map[string]string{"ENVIRONMENT": " PROD "}, "", true},
		{"ENV is the last fallback", map[string]string{"ENV": "production"}, "", true},
		{"production with key", map[string]string{"APP_ENV": "production", "INTERNAL_SERVICE_KEY": "k"}, "k", false},
		{"dev without key", map[string]string{"APP_ENV": "development"}, "", false},
		{"nothing set", nil, "", false},
		{"first set variable decides", map[string]string{"APP_ENV": "staging", "ENV": "production"}, "", false},
	} {
		key, err := internalKeyForBoot(envOf(tc.env))
		if tc.refused {
			if !errors.Is(err, errInternalKeyRequired) {
				t.Errorf("%s: err = %v, want errInternalKeyRequired", tc.name, err)
			}
			continue
		}
		if err != nil || key != tc.wantKey {
			t.Errorf("%s: (%q, %v), want (%q, nil)", tc.name, key, err, tc.wantKey)
		}
	}
}
