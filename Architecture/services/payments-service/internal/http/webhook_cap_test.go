package http

import (
	"bytes"
	"net/http"
	"testing"
)

// L4 (audit 2026-10-30): the public webhook reads its body before the signature
// can be checked, so the body is capped. Over the cap is refused with 413 and
// nothing reaches the service, even when correctly signed; a signed event under
// the cap is still applied.
func TestWebhook_BodyIsCappedBeforeVerification(t *testing.T) {
	fake := newFake()
	r := newRouter(t, fake, routerOpts{webhookSecret: testWebhookSecret, internalKey: testInternalKey})

	event := capturedEvent("order_cap_1", "pay_cap_1")
	// Valid JSON padded past the cap with trailing whitespace, and signed, so
	// only the size can refuse it.
	big := append(append([]byte{}, event...), bytes.Repeat([]byte(" "), int(MaxWebhookBodyBytes))...)
	w := do(r, http.MethodPost, "/v1/payments/webhook", big, signedWebhook("evt_cap_big", big))
	if w.Code != http.StatusRequestEntityTooLarge || len(fake.webhooks) != 0 {
		t.Fatalf("over the cap: status = %d webhooks=%d, want 413 and none applied", w.Code, len(fake.webhooks))
	}

	// Exactly at the cap is accepted.
	atCap := append(append([]byte{}, event...), bytes.Repeat([]byte(" "), int(MaxWebhookBodyBytes)-len(event))...)
	w = do(r, http.MethodPost, "/v1/payments/webhook", atCap, signedWebhook("evt_cap_at", atCap))
	if w.Code != http.StatusOK || len(fake.webhooks) != 1 {
		t.Fatalf("at the cap: status = %d webhooks=%d body=%s, want 200 and one applied", w.Code, len(fake.webhooks), w.Body.String())
	}
}
