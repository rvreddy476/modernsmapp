package http

// Stub-mode intents name the stub in client_session (WithStubSession), so a
// client detects the stub by `provider` — the same field it reads for every
// real adapter — instead of sniffing the `order_stub_` order handle.

import (
	"context"
	"testing"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

func stubIntent() *postgres.PaymentIntent {
	return &postgres.PaymentIntent{
		ID: uuid.New(), Status: "pending", Currency: "INR", AmountMinorRaw: 134800,
		ProviderRef: "order_stub_abc123", ReferenceType: "order", ReferenceID: uuid.New(),
		PayerID: uuid.New(), PayeeID: uuid.New(), ApplicationID: "mstore",
	}
}

func TestStubModeIntentNamesTheStubInItsClientSession(t *testing.T) {
	h := New(newFake()).WithStubSession(true) // no provider: stub mode
	out := h.withClientSession(context.Background(), stubIntent())

	session, ok := out["client_session"].(map[string]string)
	if !ok {
		t.Fatalf("client_session missing or not a map: %#v", out["client_session"])
	}
	want := map[string]string{
		"provider": "stub", "order_id": "order_stub_abc123", "key_id": "",
		"merchant_display_name": "Momentum Merchant",
	}
	if len(session) != len(want) {
		t.Fatalf("session = %v, want %v", session, want)
	}
	for k, v := range want {
		if session[k] != v {
			t.Errorf("session[%q] = %q, want %q", k, session[k], v)
		}
	}
}

func TestIntentCarriesNoSessionWhenNeitherProviderNorStubIsWired(t *testing.T) {
	// A deployment that resolved no mode (the production refusal path)
	// attaches nothing, exactly as before.
	h := New(newFake())
	out := h.withClientSession(context.Background(), stubIntent())
	if _, present := out["client_session"]; present {
		t.Fatalf("client_session attached without a provider or the stub flag: %#v", out["client_session"])
	}
}

func TestStubSessionNeedsAProviderRef(t *testing.T) {
	h := New(newFake()).WithStubSession(true)
	in := stubIntent()
	in.ProviderRef = ""
	out := h.withClientSession(context.Background(), in)
	if _, present := out["client_session"]; present {
		t.Fatal("a stub session was attached to an intent with no order handle")
	}
}
