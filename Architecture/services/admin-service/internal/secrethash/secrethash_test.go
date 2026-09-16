package secrethash

import (
	"errors"
	"strings"
	"testing"
)

func TestHashIsNotTheSecretAndVerifies(t *testing.T) {
	h, err := Hash("client-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h, "client-secret-value") || !IsHash(h) {
		t.Fatalf("hash %q", h)
	}
	if err := Verify(h, "client-secret-value"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := Verify(h, "client-secret-valuE"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("wrong secret: %v", err)
	}
	h2, _ := Hash("client-secret-value")
	if h2 == h {
		t.Fatal("two hashes of the same secret are identical; the salt is not random")
	}
}

func TestPlaintextIsNotAHash(t *testing.T) {
	if IsHash("client-secret-value") {
		t.Fatal("plaintext classified as a hash")
	}
	if err := Verify("client-secret-value", "client-secret-value"); !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("a plaintext stored value must never verify: %v", err)
	}
	if _, err := Hash(""); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("empty secret: %v", err)
	}
}
