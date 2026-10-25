package main

import "testing"

// blobProduction is the verdict handed to blob.ConfigFromEnv. Only the PII
// classifier's development list may use MinIO with static keys; managed and
// unknown environments are production for the object store.
func TestBlobProduction(t *testing.T) {
	for _, env := range []string{"dev", "development", "local", "test", "ci", " Dev "} {
		if blobProduction(env) {
			t.Fatalf("ENV=%q is a development environment and must not count as production", env)
		}
	}
	for _, env := range []string{"prod", "production", "staging", "stage", "", "qa", "prd"} {
		if !blobProduction(env) {
			t.Fatalf("ENV=%q must count as production for the object store", env)
		}
	}
}

// PAYMENTS_ALLOW_STUB opens the stub settlement route, so it is refused in
// every environment that is not on the development list, including a blank
// or unknown ENV. Unset, it is never refused.
func TestStubSettlementPermittedOnlyInDevelopment(t *testing.T) {
	for _, env := range []string{"dev", "development", "local", "test", "ci", " Dev "} {
		if err := stubSettlementPermitted(env, true); err != nil {
			t.Fatalf("ENV=%q with PAYMENTS_ALLOW_STUB: refused (%v), want permitted", env, err)
		}
	}
	for _, env := range []string{"prod", "production", "staging", "stage", "", "qa", "prd"} {
		if err := stubSettlementPermitted(env, true); err == nil {
			t.Fatalf("ENV=%q with PAYMENTS_ALLOW_STUB: permitted, want refused", env)
		}
		if err := stubSettlementPermitted(env, false); err != nil {
			t.Fatalf("ENV=%q without PAYMENTS_ALLOW_STUB: refused (%v), want permitted", env, err)
		}
	}
}
