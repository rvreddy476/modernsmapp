package main

import (
	"errors"
	"testing"
)

func TestRequireInternalKey(t *testing.T) {
	cases := []struct {
		name       string
		key        string
		production bool
		wantErr    bool
	}{
		{"prod without key refuses", "", true, true},
		{"prod with blank key refuses", "   ", true, true},
		{"prod with key boots", "k", true, false},
		{"dev without key boots", "", false, false},
	}
	for _, tc := range cases {
		err := requireInternalKey(tc.key, tc.production)
		if tc.wantErr != (err != nil) {
			t.Fatalf("%s: err=%v, wantErr=%v", tc.name, err, tc.wantErr)
		}
		if err != nil && !errors.Is(err, errInternalKeyRequired) {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
	}
}

func TestIsProductionEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		name string
		vars map[string]string
		want bool
	}{
		{"nothing set", map[string]string{}, false},
		{"ENV=prod", map[string]string{"ENV": "prod"}, true},
		{"APP_ENV=production wins", map[string]string{"APP_ENV": "Production", "ENV": "dev"}, true},
		{"APP_ENV=dev wins over ENV=prod", map[string]string{"APP_ENV": "dev", "ENV": "prod"}, false},
		{"ENVIRONMENT=staging", map[string]string{"ENVIRONMENT": "staging"}, false},
	}
	for _, tc := range cases {
		if got := isProductionEnv(env(tc.vars)); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
