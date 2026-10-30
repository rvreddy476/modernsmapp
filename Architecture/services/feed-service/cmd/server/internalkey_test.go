package main

import "testing"

// Audit M5: a deployed feed-service refuses to start without the internal
// key; a local run may.
func TestInternalKeyFromEnv(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{"prod without key", map[string]string{"ENV": "prod"}, true},
		{"production via APP_ENV", map[string]string{"APP_ENV": "Production"}, true},
		{"staging without key", map[string]string{"ENV": "staging"}, true},
		{"qa via DEPLOY_ENV", map[string]string{"DEPLOY_ENV": "qa"}, true},
		{"blank key in prod", map[string]string{"ENV": "prod", "INTERNAL_SERVICE_KEY": "   "}, true},
		{"prod with key", map[string]string{"ENV": "prod", "INTERNAL_SERVICE_KEY": "k"}, false},
		{"local run", map[string]string{}, false},
		{"dev run", map[string]string{"ENV": "dev"}, false},
	}
	for _, tc := range cases {
		key, err := internalKeyFromEnv(func(k string) string { return tc.env[k] })
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
		if err == nil && tc.env["INTERNAL_SERVICE_KEY"] == "k" && key != "k" {
			t.Errorf("%s: key = %q", tc.name, key)
		}
	}
}
