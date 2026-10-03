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
