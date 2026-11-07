package main

import (
	"errors"
	"testing"
)

// TestInternalKeyRequiredInProduction is X-L1: production refuses to boot
// without INTERNAL_SERVICE_KEY; other environments only warn.
func TestInternalKeyRequiredInProduction(t *testing.T) {
	envOf := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	refused := []map[string]string{
		{"APP_ENV": "production"},
		{"APP_ENV": "prod"},
		{"ENVIRONMENT": " Production "},
		{"ENV": "PROD"},
	}
	for _, e := range refused {
		if err := requireInternalKeyInProduction("", envOf(e)); !errors.Is(err, errNoInternalKeyInProduction) {
			t.Fatalf("%v with no key: %v", e, err)
		}
		if err := requireInternalKeyInProduction("   ", envOf(e)); !errors.Is(err, errNoInternalKeyInProduction) {
			t.Fatalf("%v with a blank key: %v", e, err)
		}
		if err := requireInternalKeyInProduction("k", envOf(e)); err != nil {
			t.Fatalf("%v with a key: %v", e, err)
		}
	}
	allowed := []map[string]string{
		{},
		{"APP_ENV": "local"},
		{"APP_ENV": "dev", "ENV": "production"}, // the first one set decides
		{"ENVIRONMENT": "staging"},
	}
	for _, e := range allowed {
		if err := requireInternalKeyInProduction("", envOf(e)); err != nil {
			t.Fatalf("%v: %v", e, err)
		}
	}
}
