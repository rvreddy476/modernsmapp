package service

import (
	"errors"
	"testing"
)

// TestValidatePasswordTooShort verifies that a password shorter than 8 characters is rejected.
func TestValidatePasswordTooShort(t *testing.T) {
	err := validatePassword("abc")
	if err == nil {
		t.Fatal("expected error for too-short password, got nil")
	}
	if !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("expected ErrPasswordTooShort, got %v", err)
	}
}

// TestValidatePasswordNoUppercase verifies that a password without an uppercase letter is rejected.
func TestValidatePasswordNoUppercase(t *testing.T) {
	err := validatePassword("password1!")
	if err == nil {
		t.Fatal("expected error for password with no uppercase, got nil")
	}
	if !errors.Is(err, ErrPasswordTooWeak) {
		t.Errorf("expected ErrPasswordTooWeak, got %v", err)
	}
}

// TestValidatePasswordNoDigit verifies that a password without a digit is rejected.
func TestValidatePasswordNoDigit(t *testing.T) {
	err := validatePassword("Password!")
	if err == nil {
		t.Fatal("expected error for password with no digit, got nil")
	}
	if !errors.Is(err, ErrPasswordTooWeak) {
		t.Errorf("expected ErrPasswordTooWeak, got %v", err)
	}
}

// TestValidatePasswordNoSpecial verifies that a password without a special character is rejected.
func TestValidatePasswordNoSpecial(t *testing.T) {
	err := validatePassword("Password1")
	if err == nil {
		t.Fatal("expected error for password with no special character, got nil")
	}
	if !errors.Is(err, ErrPasswordTooWeak) {
		t.Errorf("expected ErrPasswordTooWeak, got %v", err)
	}
}

// TestValidatePasswordValid verifies that a fully compliant password passes.
func TestValidatePasswordValid(t *testing.T) {
	err := validatePassword("Password1!")
	if err != nil {
		t.Errorf("expected nil for valid password, got %v", err)
	}
}

// TestValidatePasswordMinLength verifies that a password of exactly 8 characters with all
// required character classes passes.
func TestValidatePasswordMinLength(t *testing.T) {
	// 8 chars: uppercase P, lowercase ass, digit 1, special !, lowercase ab
	err := validatePassword("Pass1!ab")
	if err != nil {
		t.Errorf("expected nil for 8-char password meeting all requirements, got %v", err)
	}
}
