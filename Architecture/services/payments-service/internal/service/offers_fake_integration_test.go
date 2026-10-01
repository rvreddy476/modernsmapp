//go:build integration

package service

// A scripted Razorpay for the bank-offer proofs (and the no-offer golden).
//
// Same rig as reconcile_n2 and refundworker_rw: the REAL Razorpay adapter,
// pointed at an httptest server that answers in Razorpay's documented shapes.
// Nothing here reaches Razorpay's live or test API.
//
//	POST /v1/orders                          create order (body recorded)
//	GET  /v1/orders/{order_id}/payments      the order's payment attempts
//	GET  /v1/payments/{id}?expand[]=offers   one payment, with its applied offers
//	POST /v1/payments/{id}/refund            refund (body recorded)
//
// Any other request is recorded as a violation and fails the test.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/google/uuid"
)

const offerFakeWebhookSecret = "whsec_offers_fake"

type offerRazorpay struct {
	srv *httptest.Server

	mu            sync.Mutex
	orderIDs      map[string]string   // receipt → order id
	orderPayments map[string][]string // order id → raw payment entities
	payments      map[string]string   // payment id → raw expanded payment entity
	orderBodies   []string            // raw POST /v1/orders bodies, in order
	refundBodies  []string            // raw POST /v1/payments/{id}/refund bodies
	refundSeq     int
	expandQueries []string
	violations    []string
}

func newOfferRazorpay(t *testing.T) *offerRazorpay {
	t.Helper()
	s := &offerRazorpay{
		orderIDs:      map[string]string{},
		orderPayments: map[string][]string{},
		payments:      map[string]string{},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		path := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(r.Body)

		switch {
		case r.Method == http.MethodPost && path == "/v1/orders":
			s.orderBodies = append(s.orderBodies, string(raw))
			var req struct {
				Amount   int64  `json:"amount"`
				Currency string `json:"currency"`
				Receipt  string `json:"receipt"`
			}
			_ = json.Unmarshal(raw, &req)
			id, ok := s.orderIDs[req.Receipt]
			if !ok {
				id = "order_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:14]
				s.orderIDs[req.Receipt] = id
			}
			_, _ = fmt.Fprintf(w, `{"id":%q,"entity":"order","amount":%d,"amount_paid":0,"amount_due":%d,"currency":%q,"receipt":%q,"status":"created","attempts":0,"offer_id":null}`,
				id, req.Amount, req.Amount, req.Currency, req.Receipt)
			return

		case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/orders/") && strings.HasSuffix(path, "/payments"):
			orderID := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/orders/"), "/payments")
			items := s.orderPayments[orderID]
			_, _ = fmt.Fprintf(w, `{"entity":"collection","count":%d,"items":[%s]}`, len(items), strings.Join(items, ","))
			return

		case r.Method == http.MethodPost && strings.HasPrefix(path, "/v1/payments/") && strings.HasSuffix(path, "/refund"):
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/payments/"), "/refund")
			s.refundBodies = append(s.refundBodies, string(raw))
			var req struct {
				Amount int64 `json:"amount"`
			}
			_ = json.Unmarshal(raw, &req)
			s.refundSeq++
			_, _ = fmt.Fprintf(w, `{"id":"rfnd_%s_%d","entity":"refund","payment_id":%q,"amount":%d,"currency":"INR","status":"processed"}`,
				strings.TrimPrefix(id, "pay_"), s.refundSeq, id, req.Amount)
			return

		case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/payments/") && !strings.Contains(strings.TrimPrefix(path, "/v1/payments/"), "/"):
			id := strings.TrimPrefix(path, "/v1/payments/")
			s.expandQueries = append(s.expandQueries, r.URL.RawQuery)
			if body, ok := s.payments[id]; ok && strings.HasPrefix(id, "pay_") {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		s.violations = append(s.violations, r.Method+" "+r.URL.RequestURI())
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BAD_REQUEST_ERROR","description":"The id provided does not exist"}}`))
	}))
	t.Cleanup(s.srv.Close)
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.violations) > 0 {
			t.Errorf("requests the scripted Razorpay must never receive: %v", s.violations)
		}
	})
	return s
}

func (s *offerRazorpay) provider() *gateway.RazorpayProvider {
	return gateway.NewRazorpayProvider("rzp_test_offers", "secret", offerFakeWebhookSecret).
		WithEndpoint(s.srv.URL+"/v1", s.srv.Client())
}

// putPayment records a payment at "Razorpay": listed under its order, and
// fetchable by id with expand[]=offers.
func (s *offerRazorpay) putPayment(p fakePayment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	listed := p.entity(false)
	s.orderPayments[p.OrderID] = append(s.orderPayments[p.OrderID], listed)
	s.payments[p.ID] = p.entity(true)
}

func (s *offerRazorpay) snapshot() (orders, refunds, expands []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.orderBodies...), append([]string(nil), s.refundBodies...),
		append([]string(nil), s.expandQueries...)
}

// fakePayment is a Razorpay payment entity in its documented shape
// (razorpay.com/docs/api/payments/fetch-payment-expanded-offers/).
type fakePayment struct {
	ID, OrderID, Currency, Status string
	Amount                        int64
	CreatedAt                     int64
	// Offers is the expanded `offers` collection; it appears only on the
	// expand[]=offers fetch, never in the order's payment list.
	Offers []string
}

func (p fakePayment) entity(expanded bool) string {
	m := map[string]any{
		"id": p.ID, "entity": "payment", "amount": p.Amount, "currency": p.Currency,
		"status": p.Status, "order_id": p.OrderID, "method": "card", "captured": p.Status == "captured",
		"amount_refunded": 0, "created_at": p.CreatedAt,
	}
	if expanded {
		items := make([]map[string]string, 0, len(p.Offers))
		for _, o := range p.Offers {
			items = append(items, map[string]string{"id": o})
		}
		m["offers"] = map[string]any{"entity": "collection", "count": len(items), "items": items}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// signedWebhook builds a Razorpay payment webhook body and its headers, signed
// the way Razorpay signs (HMAC-SHA256 hex over the raw body).
func signedPaymentWebhook(eventID, event string, p fakePayment, createdAt int64) (http.Header, []byte) {
	body, _ := json.Marshal(map[string]any{
		"entity": "event", "event": event, "contains": []string{"payment"},
		"payload":    map[string]any{"payment": map[string]any{"entity": json.RawMessage(p.entity(false))}},
		"created_at": createdAt,
	})
	return signedHeaders(eventID, body), body
}

func signedRefundWebhook(eventID, refundID, paymentID string, amount int64, createdAt int64) (http.Header, []byte) {
	body, _ := json.Marshal(map[string]any{
		"entity": "event", "event": "refund.processed", "contains": []string{"refund"},
		"payload": map[string]any{"refund": map[string]any{"entity": map[string]any{
			"id": refundID, "entity": "refund", "amount": amount, "currency": "INR",
			"payment_id": paymentID, "status": "processed",
		}}},
		"created_at": createdAt,
	})
	return signedHeaders(eventID, body), body
}

func signedHeaders(eventID string, body []byte) http.Header {
	mac := hmac.New(sha256.New, []byte(offerFakeWebhookSecret))
	mac.Write(body)
	h := http.Header{}
	h.Set("X-Razorpay-Signature", hex.EncodeToString(mac.Sum(nil)))
	h.Set("X-Razorpay-Event-Id", eventID)
	return h
}
