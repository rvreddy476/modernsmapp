package digilocker

import (
	"context"
	"strings"
	"testing"
)

func TestMockClient_Deterministic(t *testing.T) {
	m := NewMockClient()
	got, err := m.ExchangeCode(context.Background(), "abc", "state-xyz")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if got.Reference != "mock-ref-abc" {
		t.Errorf("reference: %s", got.Reference)
	}
	if got.DocumentType != "AADHAAR-XML" {
		t.Errorf("doc type: %s", got.DocumentType)
	}
	if got.IssuedAt.IsZero() {
		t.Errorf("issued at zero")
	}
}

func TestMockClient_FailKeyword(t *testing.T) {
	m := NewMockClient()
	_, err := m.ExchangeCode(context.Background(), "fail", "state-xyz")
	if err == nil {
		t.Fatalf("expected simulated failure")
	}
}

func TestMockClient_EmptyCodeRejected(t *testing.T) {
	m := NewMockClient()
	_, err := m.ExchangeCode(context.Background(), "", "state-xyz")
	if err == nil {
		t.Fatalf("expected error for empty code")
	}
	if !strings.Contains(err.Error(), "code required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestHashDocumentType_Deterministic(t *testing.T) {
	a := HashDocumentType("AADHAAR-XML")
	b := HashDocumentType("AADHAAR-XML")
	if a != b {
		t.Fatalf("hash should be deterministic")
	}
	if a == HashDocumentType("PAN") {
		t.Fatalf("different inputs must hash differently")
	}
	if len(a) != 64 {
		t.Errorf("sha-256 hex should be 64 chars; got %d", len(a))
	}
}

// TestHTTPClient_BuildsRequest is a lightweight cover that we ship the same
// surface area as the dating-service client. A full HTTP integration test
// would need an httptest server; that sits in S2 alongside the real partner
// wiring.
func TestHTTPClient_RequiresBaseURL(t *testing.T) {
	c := NewHTTPClient("", "key", true)
	_, err := c.ExchangeCode(context.Background(), "code", "state")
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("expected base_url error; got %v", err)
	}
}
