package otp

import (
	"testing"
)

func TestEnvelopeEncryption_Roundtrip(t *testing.T) {
	masterKey := []byte("test-master-key-32-bytes-long!!")
	plaintext := "4829"

	ciphertext, err := EncryptOTP(plaintext, masterKey)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	if string(ciphertext) == plaintext {
		t.Fatalf("ciphertext must not match plaintext")
	}

	decrypted, err := DecryptOTP(ciphertext, masterKey)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("expected %s, got %s", plaintext, decrypted)
	}
}

func TestEnvelopeEncryption_WrongKeyFails(t *testing.T) {
	masterKey := []byte("correct-key-32-bytes-long!!!!!!")
	wrongKey := []byte("wrong-key-32-bytes-long!!!!!!!!")
	plaintext := "1234"

	ciphertext, err := EncryptOTP(plaintext, masterKey)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	_, err = DecryptOTP(ciphertext, wrongKey)
	if err == nil {
		t.Fatalf("expected decryption failure with wrong key, got nil error")
	}
}
