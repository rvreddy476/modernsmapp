package setu

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMockClient_FetchBill_FailSentinel(t *testing.T) {
	m := NewMockClient()
	if _, err := m.FetchBill(context.Background(), "BSESRJ", "fail-fetch", nil); err == nil {
		t.Fatalf("expected error for fail-fetch sentinel")
	}
}

func TestMockClient_FetchBill_NoBillSentinel(t *testing.T) {
	m := NewMockClient()
	b, err := m.FetchBill(context.Background(), "BSESRJ", "no-bill", nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if b != nil {
		t.Fatalf("expected nil bill")
	}
}

func TestMockClient_SubmitPayment_FailSentinel(t *testing.T) {
	m := NewMockClient()
	if _, err := m.SubmitPayment(context.Background(), PaymentRequest{
		AtPostPaymentID: "fail-submit",
	}); err == nil {
		t.Fatalf("expected error for fail-submit sentinel")
	}
}

func TestMockClient_SubmitPayment_BBPSDeclined(t *testing.T) {
	m := NewMockClient()
	r, err := m.SubmitPayment(context.Background(), PaymentRequest{
		SetuBillerID:    "submit-fails-bbps",
		AmountPaise:     1000,
		AtPostPaymentID: "x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Status != "failed" {
		t.Fatalf("expected failed status; got %q", r.Status)
	}
}

func TestMockClient_DetectOperatorCircle(t *testing.T) {
	m := NewMockClient()
	op, cir, err := m.DetectOperatorCircle(context.Background(), "8123456789")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if op != "airtel" || cir != "KA" {
		t.Fatalf("expected airtel/KA; got %s/%s", op, cir)
	}
	if _, _, err := m.DetectOperatorCircle(context.Background(), ""); err == nil {
		t.Fatalf("expected error on empty phone")
	}
}

func TestMockClient_VerifyWebhookSignature_OK(t *testing.T) {
	m := NewMockClient()
	body := []byte(`{"setu_payment_ref":"ref-1","status":"succeeded"}`)
	mac := hmac.New(sha256.New, []byte("mock-webhook-secret"))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	req.Header.Set("X-Setu-Signature", sig)
	if err := m.VerifyWebhookSignature(req, body); err != nil {
		t.Fatalf("expected valid signature; got %v", err)
	}
}

func TestMockClient_VerifyWebhookSignature_Invalid(t *testing.T) {
	m := NewMockClient()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	req.Header.Set("X-Setu-Signature", "deadbeef")
	if err := m.VerifyWebhookSignature(req, []byte("body")); err == nil {
		t.Fatalf("expected invalid-signature error")
	}
}

func TestMockClient_VerifyWebhookSignature_MissingHeader(t *testing.T) {
	m := NewMockClient()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	if err := m.VerifyWebhookSignature(req, []byte("body")); err == nil {
		t.Fatalf("expected missing-header error")
	}
}
