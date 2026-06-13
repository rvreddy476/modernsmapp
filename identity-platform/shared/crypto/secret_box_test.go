package crypto

import (
	"bytes"
	"testing"
)

// 32 random bytes hex-encoded — fine for tests, never re-use in prod.
const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSecretBox_RoundTrip(t *testing.T) {
	box, err := NewSecretBox(testKey)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	plain := []byte("JBSWY3DPEHPK3PXP")
	ct, err := box.Seal(plain)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Equal(plain, ct) {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := box.Open(ct)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round-trip mismatch: %q vs %q", got, plain)
	}
}

func TestSecretBox_DistinctCiphertexts(t *testing.T) {
	// Same plaintext sealed twice must produce different ciphertexts
	// because the nonce is randomised. Catches the bug where a future
	// edit accidentally fixes the nonce.
	box, _ := NewSecretBox(testKey)
	a, _ := box.Seal([]byte("secret"))
	b, _ := box.Seal([]byte("secret"))
	if bytes.Equal(a, b) {
		t.Fatal("identical ciphertexts — nonce is not random")
	}
}

func TestSecretBox_TamperDetected(t *testing.T) {
	box, _ := NewSecretBox(testKey)
	ct, _ := box.Seal([]byte("secret"))
	// Flip a byte in the tag region.
	ct[len(ct)-1] ^= 0x01
	if _, err := box.Open(ct); err == nil {
		t.Fatal("expected tamper rejection")
	}
}

func TestSecretBox_WrongKey(t *testing.T) {
	a, _ := NewSecretBox(testKey)
	otherKey := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	b, _ := NewSecretBox(otherKey)
	ct, _ := a.Seal([]byte("secret"))
	if _, err := b.Open(ct); err == nil {
		t.Fatal("decryption with wrong key should fail")
	}
}

func TestNewSecretBox_RejectsShortKey(t *testing.T) {
	if _, err := NewSecretBox("deadbeef"); err == nil {
		t.Fatal("expected rejection of short key")
	}
}

func TestNewSecretBox_RejectsNonHex(t *testing.T) {
	bad := "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	if _, err := NewSecretBox(bad); err == nil {
		t.Fatal("expected rejection of non-hex key")
	}
}
