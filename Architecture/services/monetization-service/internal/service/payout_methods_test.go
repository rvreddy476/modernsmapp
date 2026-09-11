package service

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// The full account number is stored only as AES-256-GCM ciphertext (plan
// Phase 4D). A round trip must give the number back, the ciphertext must
// not contain it, and a different key must not open it.
func TestBankAccountNumberEncryptionRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	enc, err := encryptSecret(key, "765432123456789")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "v1:") || strings.Contains(enc, "765432123456789") {
		t.Fatalf("ciphertext %q leaks or is untagged", enc)
	}
	got, err := decryptSecret(key, enc)
	if err != nil || got != "765432123456789" {
		t.Fatalf("decrypt = %q, %v", got, err)
	}
	other := bytes.Repeat([]byte{8}, 32)
	if _, err := decryptSecret(other, enc); err == nil {
		t.Fatal("a different key opened the ciphertext")
	}
	// Two encryptions of the same number differ (random nonce).
	enc2, _ := encryptSecret(key, "765432123456789")
	if enc2 == enc {
		t.Fatal("nonce reuse: identical ciphertexts")
	}
	if _, err := decryptSecret(key, "enc:test"); err == nil {
		t.Fatal("a legacy opaque blob decrypted")
	}
}

func TestParseBankDetailsKey(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	for _, in := range []string{hex.EncodeToString(key), base64.StdEncoding.EncodeToString(key), base64.RawURLEncoding.EncodeToString(key), " " + hex.EncodeToString(key) + "\n"} {
		got, err := ParseBankDetailsKey(in)
		if err != nil || !bytes.Equal(got, key) {
			t.Errorf("ParseBankDetailsKey(%q) = %x, %v", in, got, err)
		}
	}
	if got, err := ParseBankDetailsKey(""); err != nil || got != nil {
		t.Errorf("empty key = %v, %v; want nil, nil", got, err)
	}
	for _, in := range []string{"abc", hex.EncodeToString(key[:16]), base64.StdEncoding.EncodeToString(key[:31])} {
		if _, err := ParseBankDetailsKey(in); err == nil {
			t.Errorf("ParseBankDetailsKey(%q) accepted a bad key", in)
		}
	}
	s := New(nil, nil).WithBankDetailsKey(key[:16])
	if s.BankCaptureEnabled() {
		t.Fatal("a 16-byte key enabled bank capture")
	}
	if !New(nil, nil).WithBankDetailsKey(key).BankCaptureEnabled() {
		t.Fatal("a 32-byte key did not enable bank capture")
	}
}
