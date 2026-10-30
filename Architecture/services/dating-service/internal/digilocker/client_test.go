package digilocker

import (
	"context"
	"testing"
)

// Audit M1: the mock carries a stable subject per person (per code), so the
// one-account-per-Aadhaar rule can be exercised end to end.
func TestMockClientCarriesAStableSubject(t *testing.T) {
	m := NewMockClient()
	a, err := m.ExchangeCode(context.Background(), "alice", "s1")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := m.ExchangeCode(context.Background(), "alice", "s2")
	b, _ := m.ExchangeCode(context.Background(), "bob", "s3")
	if a.Subject == "" || a.Subject != again.Subject || a.Subject == b.Subject {
		t.Fatalf("subjects a=%q again=%q b=%q", a.Subject, again.Subject, b.Subject)
	}
}
