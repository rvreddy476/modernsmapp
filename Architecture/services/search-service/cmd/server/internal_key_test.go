package main

import (
	"errors"
	"testing"
)

// X-L1: production without the internal key must not boot; with it, or
// outside production, it does.
func TestRequireInternalKey(t *testing.T) {
	if err := requireInternalKey("", true); !errors.Is(err, errInternalKeyRequired) {
		t.Fatalf("production booted without INTERNAL_SERVICE_KEY: %v", err)
	}
	if err := requireInternalKey("k", true); err != nil {
		t.Fatalf("production with a key was refused: %v", err)
	}
	if err := requireInternalKey("", false); err != nil {
		t.Fatalf("a dev rig without a key was refused: %v", err)
	}
}
