package main

import (
	"strings"
	"testing"

	"github.com/atpost/shared/identityroles"
)

// TestEveryEcosystemRoleHasASource: each role a service may grant can be
// backfilled and reconciled, and no source names a role identity would 403.
func TestEveryEcosystemRoleHasASource(t *testing.T) {
	covered := map[string]bool{}
	for name, src := range sources {
		if src.name != name {
			t.Errorf("source %q names itself %q", name, src.name)
		}
		if !identityroles.IsEcosystemRole(src.role) {
			t.Errorf("source %q grants %q, which identity refuses from a service", name, src.role)
		}
		if src.query == "" || src.mirrors == "" || src.db == "" {
			t.Errorf("source %q is incomplete", name)
		}
		covered[src.role] = true
	}
	for _, r := range identityroles.Ecosystem() {
		if !covered[r] {
			t.Errorf("ecosystem role %q has no backfill source", r)
		}
	}
}

// TestDoorstepSourceKeepsSuspended: doorstep revokes on rejected and blocked
// only; a suspended professional keeps the role, so the reconciler must not
// skip them.
func TestDoorstepSourceKeepsSuspended(t *testing.T) {
	src, ok := sources["doorstep"]
	if !ok {
		t.Fatal("no doorstep source")
	}
	if src.role != "service_professional" {
		t.Fatalf("doorstep source grants %q", src.role)
	}
	if !strings.Contains(src.query, "doorstep.professionals") ||
		!strings.Contains(src.query, "NOT IN ('rejected','blocked')") ||
		strings.Contains(src.query, "suspended") {
		t.Fatalf("doorstep query must exclude exactly rejected and blocked:\n%s", src.query)
	}
}
