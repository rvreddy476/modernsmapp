package bgv

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestModes(t *testing.T) {
	if p, err := New("mock", true); err == nil || p != nil {
		t.Fatal("mock accepted in production")
	}
	for _, prod := range []bool{true, false} {
		if p, err := New("provider", prod); err == nil || p != nil {
			t.Fatalf("provider accepted (production=%v)", prod)
		}
		if _, err := New("vendor-x", prod); err == nil {
			t.Fatal("unknown mode accepted")
		}
	}
	p, err := New("uploaded_document", true)
	if err != nil || p.Name() != ModeUploadedDocument {
		t.Fatalf("uploaded_document: %v", err)
	}
	issued := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	r, err := p.Initiate(context.Background(), Request{IssuedOn: issued})
	if err != nil || r.Status != StatusPending || r.ValidUntil != nil {
		t.Fatalf("uploaded_document never clears by itself: %+v %v", r, err)
	}
	if _, err := p.VerifyWebhook(nil, nil); !errors.Is(err, ErrNotSupported) {
		t.Fatal("uploaded_document accepted a webhook")
	}
	if _, err := p.Fetch(context.Background(), "x"); !errors.Is(err, ErrNotSupported) {
		t.Fatal("uploaded_document fetch")
	}
	m, err := New("mock", false)
	if err != nil {
		t.Fatal(err)
	}
	r, _ = m.Initiate(context.Background(), Request{IssuedOn: issued})
	if r.Status != StatusClear || !r.ValidUntil.Equal(time.Date(2027, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("mock %+v", r)
	}
	if !ValidUntil(time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)).Equal(time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("leap day")
	}
}
