package service

import (
	"context"
	"errors"
	"testing"
)

// Stub verifier used in unit tests so we don't depend on Postgres or HTTP.
type stubVerifier struct {
	assertion AadhaarAssertion
	err       error
}

func (s *stubVerifier) ExchangeCode(_ context.Context, _ string, _ string) (AadhaarAssertion, error) {
	return s.assertion, s.err
}

func TestPANRegex_HappyAndUnhappy(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"ABCDE1234F", true},
		{"abcde1234f", false}, // case-sensitive at the regex level; service uppercases first
		{"ABCDE12345", false}, // last char must be letter
		{"1234567890", false},
		{"AAAAA1234A", true},
		{"ABCD1234EF", false}, // 4 letters + 4 digits + 2 letters = 10 chars but bad shape
		{"", false},
	}
	for _, tc := range cases {
		got := panRE.MatchString(tc.in)
		if got != tc.want {
			t.Errorf("panRE(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestRandomHex_LengthAndDifferent(t *testing.T) {
	a, err := randomHex(16)
	if err != nil {
		t.Fatalf("hex: %v", err)
	}
	if len(a) != 32 {
		t.Fatalf("hex(16) should be 32 chars; got %d", len(a))
	}
	b, _ := randomHex(16)
	if a == b {
		t.Fatalf("hex calls should not collide")
	}
}

func TestStubVerifier_ErrorPath(t *testing.T) {
	v := &stubVerifier{err: errors.New("boom")}
	if _, err := v.ExchangeCode(context.Background(), "code", "state"); err == nil {
		t.Fatalf("expected error from stub")
	}
}
