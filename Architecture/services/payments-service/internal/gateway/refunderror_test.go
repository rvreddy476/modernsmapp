package gateway

// The Razorpay adapter never sends an order id to a payments route, and its
// refusals classify into retry / park / settle. Driven through the REAL adapter
// against recorded response bodies over httptest, like razorpay_recovery_test.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// countingRazorpay is the real adapter against a server that answers every
// request with one reply and counts what it receives.
func countingRazorpay(t *testing.T, status int, body string) (*RazorpayProvider, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewRazorpayProvider("rzp_test", "secret", "whsec").WithEndpoint(srv.URL, srv.Client()), &n
}

var inr60712 = Money{Minor: 60712, Currency: "INR"}

// The refund worker sent `order_…` to POST /payments/{id}/refund, and Razorpay
// answered 400 on every attempt. No payments route may be called with an order
// id or a blank id; the refusal happens before any request exists.
func TestRazorpayNeverSendsAnOrderIDToAPaymentsRoute(t *testing.T) {
	ctx := context.Background()
	p, calls := countingRazorpay(t, http.StatusBadRequest,
		`{"error":{"code":"BAD_REQUEST_ERROR","description":"The id provided does not exist"}}`)

	routes := map[string]func(id string) error{
		"refund": func(id string) error {
			_, err := p.Refund(ctx, id, inr60712, "refund-key")
			return err
		},
		"capture": func(id string) error {
			_, err := p.Capture(ctx, id, inr60712, "capture-key")
			return err
		},
		"fetch payment": func(id string) error {
			_, err := p.FetchPayment(ctx, id)
			return err
		},
		"list refunds": func(id string) error {
			_, err := p.FetchPaymentRefunds(ctx, id)
			return err
		},
	}
	for name, call := range routes {
		for _, id := range []string{"order_RZP9aX1b2C3d4E", ""} {
			err := call(id)
			if !errors.Is(err, ErrNotAPaymentID) {
				t.Errorf("%s(%q): err = %v, want ErrNotAPaymentID", name, id, err)
			}
			if got := ClassifyRefundError(err); got != RefundTerminal {
				t.Errorf("%s(%q): class = %s, want terminal", name, id, got)
			}
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d request(s) reached the provider with an order id or a blank id", n)
	}

	// A genuine payment id is not caught by the guard.
	_, _ = p.FetchPayment(ctx, "pay_RZP9aX1b2C3d4E")
	if n := calls.Load(); n != 1 {
		t.Fatalf("requests after a genuine payment id = %d, want 1", n)
	}
}

func TestClassifyRefundErrorFromRazorpayResponses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   RefundErrorClass
	}{
		{"payment id does not exist", 400,
			`{"error":{"code":"BAD_REQUEST_ERROR","description":"The id provided does not exist","source":"business","step":"payment_initiation","reason":"input_validation_failed","metadata":{}}}`,
			RefundTerminal},
		{"payment not captured", 400,
			`{"error":{"code":"BAD_REQUEST_ERROR","description":"Refund cannot be initiated on a payment that is not captured","reason":"input_validation_failed"}}`,
			RefundTerminal},
		{"amount exceeds refundable", 400,
			`{"error":{"code":"BAD_REQUEST_ERROR","description":"The refund amount provided is greater than amount captured"}}`,
			RefundTerminal},
		{"4xx with no JSON body", 404, `not found`, RefundTerminal},
		{"already fully refunded", 400,
			`{"error":{"code":"BAD_REQUEST_ERROR","description":"The payment has been fully refunded already"}}`,
			RefundAlreadyRefunded},
		{"unauthorised", 401,
			`{"error":{"code":"BAD_REQUEST_ERROR","description":"Authentication failed"}}`, RefundRetryable},
		{"same idempotency key in flight", 409,
			`{"error":{"code":"BAD_REQUEST_ERROR","description":"Request is being processed"}}`, RefundRetryable},
		{"rate limited", 429,
			`{"error":{"code":"BAD_REQUEST_ERROR","description":"Too many requests"}}`, RefundRetryable},
		{"server error", 500,
			`{"error":{"code":"SERVER_ERROR","description":"We are facing some trouble completing your request at the moment. Please try again shortly."}}`,
			RefundRetryable},
		{"bad gateway, HTML body", 502, `<html>502 Bad Gateway</html>`, RefundRetryable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, _ := countingRazorpay(t, c.status, c.body)
			_, err := p.Refund(context.Background(), "pay_RZP9aX1b2C3d4E", inr60712, "refund-key")
			if err == nil {
				t.Fatalf("status %d produced no error", c.status)
			}
			if got := ClassifyRefundError(err); got != c.want {
				t.Fatalf("class = %s, want %s (error: %v)", got, c.want, err)
			}
		})
	}

	t.Run("connection refused", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		base := srv.URL
		srv.Close()
		p := NewRazorpayProvider("rzp_test", "secret", "").WithEndpoint(base, &http.Client{Timeout: 2 * time.Second})
		_, err := p.Refund(context.Background(), "pay_RZP9aX1b2C3d4E", inr60712, "refund-key")
		if err == nil || ClassifyRefundError(err) != RefundRetryable {
			t.Fatalf("err = %v, class = %s; want a retryable transport error", err, ClassifyRefundError(err))
		}
	})

	t.Run("timeout", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(300 * time.Millisecond)
		}))
		t.Cleanup(srv.Close)
		p := NewRazorpayProvider("rzp_test", "secret", "").WithEndpoint(srv.URL, &http.Client{Timeout: 30 * time.Millisecond})
		_, err := p.Refund(context.Background(), "pay_RZP9aX1b2C3d4E", inr60712, "refund-key")
		if err == nil || ClassifyRefundError(err) != RefundRetryable {
			t.Fatalf("err = %v, class = %s; want a retryable timeout", err, ClassifyRefundError(err))
		}
	})
}

// last_error is stored: it carries the status, code, reason and a bounded
// description — never the raw body.
func TestRedactedRefundErrorCarriesNoRawBody(t *testing.T) {
	p, _ := countingRazorpay(t, http.StatusBadRequest,
		`{"error":{"code":"BAD_REQUEST_ERROR","description":"The id provided does not exist","reason":"input_validation_failed","metadata":{"email":"buyer@example.com"}}}`)
	_, err := p.Refund(context.Background(), "pay_RZP9aX1b2C3d4E", inr60712, "refund-key")
	got := RedactError(err)
	for _, want := range []string{"400", "BAD_REQUEST_ERROR", "input_validation_failed", "does not exist", "/payments/pay_RZP9aX1b2C3d4E/refund"} {
		if !strings.Contains(got, want) {
			t.Errorf("redacted error %q lacks %q", got, want)
		}
	}
	for _, leak := range []string{"buyer@example.com", "metadata"} {
		if strings.Contains(got, leak) {
			t.Errorf("redacted error %q carries %q from the raw body", got, leak)
		}
	}

	long, _ := countingRazorpay(t, http.StatusBadRequest,
		`{"error":{"code":"BAD_REQUEST_ERROR","description":"`+strings.Repeat("x", 5000)+`"}}`)
	_, err = long.Refund(context.Background(), "pay_RZP9aX1b2C3d4E", inr60712, "refund-key")
	if n := len(RedactError(err)); n > 400 {
		t.Errorf("redacted error is %d bytes; the description must be bounded", n)
	}
}

func TestFetchPaymentRefundsDecodesTheRefunds(t *testing.T) {
	p, _ := countingRazorpay(t, http.StatusOK, `{"entity":"collection","count":2,"items":[
		{"id":"rfnd_A","entity":"refund","amount":60712,"currency":"INR","payment_id":"pay_X","status":"processed"},
		{"id":"rfnd_B","entity":"refund","amount":100,"payment_id":"pay_X","status":"pending"}]}`)
	refunds, err := p.FetchPaymentRefunds(context.Background(), "pay_X")
	if err != nil {
		t.Fatalf("list refunds: %v", err)
	}
	if len(refunds) != 2 {
		t.Fatalf("refunds = %+v, want 2", refunds)
	}
	if r := refunds[0]; r.ProviderRefundID != "rfnd_A" || r.State != StateRefunded ||
		r.Amount != (Money{Minor: 60712, Currency: "INR"}) || r.ProviderPaymentID != "pay_X" {
		t.Errorf("processed refund decoded as %+v", r)
	}
	if r := refunds[1]; r.State != StatePending || r.Amount.Currency != "" {
		t.Errorf("pending refund decoded as %+v; its missing currency must stay blank", r)
	}
}
