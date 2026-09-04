package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// fakeService is an in-memory stand-in for *service.Service. It records
// the calls the webhook + ownership paths make so tests can assert on
// side effects without a Postgres pool.
type fakeService struct {
	mu      sync.Mutex
	intents map[uuid.UUID]*postgres.PaymentIntent
	seen    map[string]bool

	statusCalls []statusCall
	refundCalls []string
	verifyCalls int
}

type statusCall struct {
	providerRef, newStatus, paymentID string
}

func newFake() *fakeService {
	return &fakeService{intents: map[uuid.UUID]*postgres.PaymentIntent{}, seen: map[string]bool{}}
}

func (f *fakeService) InitiatePayment(_ context.Context, in service.InitiateInput) (*postgres.PaymentIntent, error) {
	p := &postgres.PaymentIntent{ID: uuid.New(), PayerID: in.PayerID, PayeeID: in.PayeeID, Status: "pending",
		ReferenceType: in.ReferenceType, ReferenceID: in.ReferenceID, AmountMinorRaw: in.AmountMinor, ProviderRef: "order_stub_1"}
	f.intents[p.ID] = p
	return p, nil
}
func (f *fakeService) GetIntent(_ context.Context, id uuid.UUID) (*postgres.PaymentIntent, error) {
	p, ok := f.intents[id]
	if !ok {
		return nil, postgres.ErrPaymentNotFound
	}
	return p, nil
}
func (f *fakeService) GetIntentForActor(ctx context.Context, id, actor uuid.UUID) (*postgres.PaymentIntent, error) {
	p, err := f.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	if !service.IsParty(p, actor) {
		return nil, service.ErrNotIntentParty
	}
	return p, nil
}
func (f *fakeService) UpdateStatus(_ context.Context, id uuid.UUID, _, newStatus, _ string, _ uuid.UUID) (*postgres.PaymentIntent, error) {
	p, ok := f.intents[id]
	if !ok {
		return nil, postgres.ErrPaymentNotFound
	}
	p.Status = newStatus
	return p, nil
}
func (f *fakeService) InitiateRefund(_ context.Context, id, actor uuid.UUID, _ int64, _ string) (*postgres.PaymentIntent, error) {
	p, ok := f.intents[id]
	if !ok {
		return nil, postgres.ErrPaymentNotFound
	}
	if !service.IsParty(p, actor) {
		return nil, service.ErrRefundNotAuthorized
	}
	f.refundCalls = append(f.refundCalls, "user")
	return p, nil
}
func (f *fakeService) InitiateServiceRefund(_ context.Context, id, _ uuid.UUID, _ int64, _ string) (*postgres.PaymentIntent, error) {
	p, ok := f.intents[id]
	if !ok {
		return nil, postgres.ErrPaymentNotFound
	}
	f.refundCalls = append(f.refundCalls, "internal")
	return p, nil
}
func (f *fakeService) VerifyIntent(_ context.Context, id uuid.UUID, _, _, _ string, _ int64) (*service.VerifyResult, error) {
	f.verifyCalls++
	p, ok := f.intents[id]
	if !ok {
		return nil, postgres.ErrPaymentNotFound
	}
	return &service.VerifyResult{Verified: true, IntentID: id, Status: "succeeded", ReferenceID: p.ReferenceID, PayerID: p.PayerID}, nil
}
func (f *fakeService) ListByReference(_ context.Context, _ string, refID uuid.UUID) ([]postgres.PaymentIntent, error) {
	var out []postgres.PaymentIntent
	for _, p := range f.intents {
		if p.ReferenceID == refID {
			out = append(out, *p)
		}
	}
	return out, nil
}
func (f *fakeService) ListByReferenceForActor(ctx context.Context, refType string, refID, actor uuid.UUID) ([]postgres.PaymentIntent, error) {
	all, _ := f.ListByReference(ctx, refType, refID)
	var out []postgres.PaymentIntent
	for i := range all {
		if service.IsParty(&all[i], actor) {
			out = append(out, all[i])
		}
	}
	return out, nil
}
func (f *fakeService) ReleaseHold(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (f *fakeService) MarkWebhookSeen(_ context.Context, eventID, _, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if eventID == "" {
		return true, nil
	}
	if f.seen[eventID] {
		return false, nil
	}
	f.seen[eventID] = true
	return true, nil
}
func (f *fakeService) UpdateStatusByProviderRef(_ context.Context, providerRef, newStatus, paymentID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls = append(f.statusCalls, statusCall{providerRef, newStatus, paymentID})
}
func (f *fakeService) ApplyWebhookRefund(_ context.Context, _, _, _ string, _ int64) {}

var _ Service = (*fakeService)(nil)

func newRouter(t *testing.T, fake *fakeService, webhookSecret, internalKey string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(fake).WithWebhookSecret(webhookSecret)
	if internalKey != "" {
		h.WithInternalKey(internalKey)
	}
	h.RegisterRoutes(r)
	return r
}

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func capturedEvent(eventID, rzpOrderID, rzpPaymentID string) []byte {
	b, _ := json.Marshal(map[string]any{
		"id":    eventID,
		"event": "payment.captured",
		"payload": map[string]any{
			"payment": map[string]any{"entity": map[string]any{"id": rzpPaymentID, "order_id": rzpOrderID}},
		},
	})
	return b
}

func do(r *gin.Engine, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestWebhook_HMAC covers the signature gate: a correctly signed
// payment.captured applies the status; a bad or missing signature is
// refused before any side effect; and with no secret configured (stub
// mode) unsigned calls are accepted.
func TestWebhook_HMAC(t *testing.T) {
	const secret = "whsec_test"
	body := capturedEvent("evt_1", "order_rzp_1", "pay_rzp_1")

	t.Run("valid signature applies payment.captured", func(t *testing.T) {
		fake := newFake()
		r := newRouter(t, fake, secret, "")
		w := do(r, http.MethodPost, "/v1/payments/webhook", body, map[string]string{"X-Razorpay-Signature": sign(body, secret)})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if len(fake.statusCalls) != 1 || fake.statusCalls[0] != (statusCall{"order_rzp_1", "succeeded", "pay_rzp_1"}) {
			t.Fatalf("status calls = %+v", fake.statusCalls)
		}
	})

	t.Run("invalid signature is refused with no side effects", func(t *testing.T) {
		fake := newFake()
		r := newRouter(t, fake, secret, "")
		w := do(r, http.MethodPost, "/v1/payments/webhook", body, map[string]string{"X-Razorpay-Signature": sign(body, "wrong")})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
		if len(fake.statusCalls) != 0 || len(fake.seen) != 0 {
			t.Fatalf("expected no side effects, got status=%+v seen=%v", fake.statusCalls, fake.seen)
		}
	})

	t.Run("missing signature is refused", func(t *testing.T) {
		fake := newFake()
		r := newRouter(t, fake, secret, "")
		w := do(r, http.MethodPost, "/v1/payments/webhook", body, nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})

	t.Run("tampered body fails the original signature", func(t *testing.T) {
		fake := newFake()
		r := newRouter(t, fake, secret, "")
		tampered := capturedEvent("evt_1", "order_rzp_OTHER", "pay_rzp_1")
		w := do(r, http.MethodPost, "/v1/payments/webhook", tampered, map[string]string{"X-Razorpay-Signature": sign(body, secret)})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})

	t.Run("no secret configured (stub mode) accepts unsigned", func(t *testing.T) {
		fake := newFake()
		r := newRouter(t, fake, "", "")
		w := do(r, http.MethodPost, "/v1/payments/webhook", body, nil)
		if w.Code != http.StatusOK || len(fake.statusCalls) != 1 {
			t.Fatalf("status = %d calls=%+v", w.Code, fake.statusCalls)
		}
	})

	t.Run("webhook is not behind the internal key gate", func(t *testing.T) {
		fake := newFake()
		r := newRouter(t, fake, secret, "internal-key")
		w := do(r, http.MethodPost, "/v1/payments/webhook", body, map[string]string{"X-Razorpay-Signature": sign(body, secret)})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (Razorpay cannot send the internal key)", w.Code)
		}
	})
}

// TestWebhook_DuplicateIsNoop: Razorpay retries deliveries; the second
// identical event must ack 200 without re-running the status update.
func TestWebhook_DuplicateIsNoop(t *testing.T) {
	const secret = "whsec_test"
	fake := newFake()
	r := newRouter(t, fake, secret, "")
	body := capturedEvent("evt_dup", "order_rzp_2", "pay_rzp_2")
	hdr := map[string]string{"X-Razorpay-Signature": sign(body, secret)}

	for i := 0; i < 2; i++ {
		if w := do(r, http.MethodPost, "/v1/payments/webhook", body, hdr); w.Code != http.StatusOK {
			t.Fatalf("delivery %d: status = %d", i+1, w.Code)
		}
	}
	if len(fake.statusCalls) != 1 {
		t.Fatalf("status update ran %d times, want exactly 1", len(fake.statusCalls))
	}
}

// TestRouteSplit pins the user-facing vs internal split. The gateway
// injects the internal key on every proxied request, so the only thing
// keeping a logged-in user off PATCH /status and /verify is that those
// routes no longer exist outside /internal (which the gateway gates on
// admin scope).
func TestRouteSplit(t *testing.T) {
	const key = "internal-key"
	fake := newFake()
	r := newRouter(t, fake, "", key)
	payer, payee, stranger := uuid.New(), uuid.New(), uuid.New()
	orderID := uuid.New()
	intent, _ := fake.InitiatePayment(context.Background(), service.InitiateInput{PayerID: payer, PayeeID: payee, ReferenceType: "order", ReferenceID: orderID, AmountMinor: 90000})

	withKey := func(user uuid.UUID) map[string]string {
		h := map[string]string{"X-Internal-Service-Key": key}
		if user != uuid.Nil {
			h["X-User-Id"] = user.String()
		}
		return h
	}
	statusBody := []byte(`{"old_status":"pending","new_status":"succeeded"}`)
	verifyBody := []byte(`{"razorpay_order_id":"order_stub_1","razorpay_payment_id":"pay_1","razorpay_signature":"sig"}`)

	t.Run("status PATCH is gone from the user-facing group", func(t *testing.T) {
		w := do(r, http.MethodPatch, "/v1/payments/intents/"+intent.ID.String()+"/status", statusBody, withKey(stranger))
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
		if intent.Status != "pending" {
			t.Fatalf("intent status mutated to %q", intent.Status)
		}
	})
	t.Run("status PATCH works on the internal path", func(t *testing.T) {
		w := do(r, http.MethodPatch, "/v1/payments/internal/intents/"+intent.ID.String()+"/status", statusBody, withKey(uuid.Nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("verify is gone from the user-facing group", func(t *testing.T) {
		w := do(r, http.MethodPost, "/v1/payments/intents/"+intent.ID.String()+"/verify", verifyBody, withKey(stranger))
		if w.Code != http.StatusNotFound || fake.verifyCalls != 0 {
			t.Fatalf("status = %d verifyCalls=%d", w.Code, fake.verifyCalls)
		}
	})
	t.Run("verify works on the internal path and echoes the reference", func(t *testing.T) {
		w := do(r, http.MethodPost, "/v1/payments/internal/intents/"+intent.ID.String()+"/verify", verifyBody, withKey(uuid.Nil))
		if w.Code != http.StatusOK || fake.verifyCalls != 1 {
			t.Fatalf("status = %d verifyCalls=%d body=%s", w.Code, fake.verifyCalls, w.Body.String())
		}
		var env struct {
			Data service.VerifyResult `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Data.ReferenceID != orderID || env.Data.PayerID != payer {
			t.Fatalf("verify result did not echo parties/reference: %+v", env.Data)
		}
	})
	t.Run("internal key is still required on internal paths", func(t *testing.T) {
		w := do(r, http.MethodPatch, "/v1/payments/internal/intents/"+intent.ID.String()+"/status", statusBody, nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
	t.Run("GET intent: party ok, stranger 403", func(t *testing.T) {
		for _, tc := range []struct {
			user uuid.UUID
			want int
		}{{payer, 200}, {payee, 200}, {stranger, 403}} {
			w := do(r, http.MethodGet, "/v1/payments/intents/"+intent.ID.String(), nil, withKey(tc.user))
			if w.Code != tc.want {
				t.Fatalf("user %s: status = %d, want %d", tc.user, w.Code, tc.want)
			}
		}
	})
	t.Run("list by reference filters to the caller's intents", func(t *testing.T) {
		path := "/v1/payments/intents?ref_type=order&ref_id=" + orderID.String()
		var env struct {
			Data []postgres.PaymentIntent `json:"data"`
		}
		w := do(r, http.MethodGet, path, nil, withKey(stranger))
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if w.Code != 200 || len(env.Data) != 0 {
			t.Fatalf("stranger: status=%d n=%d", w.Code, len(env.Data))
		}
		w = do(r, http.MethodGet, path, nil, withKey(payer))
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if w.Code != 200 || len(env.Data) != 1 {
			t.Fatalf("payer: status=%d n=%d", w.Code, len(env.Data))
		}
		w = do(r, http.MethodGet, "/v1/payments/internal/intents?ref_type=order&ref_id="+orderID.String(), nil, withKey(uuid.Nil))
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if w.Code != 200 || len(env.Data) != 1 {
			t.Fatalf("internal: status=%d n=%d", w.Code, len(env.Data))
		}
	})
	t.Run("refund: stranger 403 on user path, allowed on internal path", func(t *testing.T) {
		w := do(r, http.MethodPost, "/v1/payments/intents/"+intent.ID.String()+"/refund", []byte(`{}`), withKey(stranger))
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", w.Code)
		}
		w = do(r, http.MethodPost, "/v1/payments/internal/intents/"+intent.ID.String()+"/refund", []byte(`{}`), withKey(uuid.Nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		if len(fake.refundCalls) != 1 || fake.refundCalls[0] != "internal" {
			t.Fatalf("refund calls = %v", fake.refundCalls)
		}
	})
}

// TestIsParty pins the ownership predicate the handlers rely on.
func TestIsParty(t *testing.T) {
	payer, payee := uuid.New(), uuid.New()
	p := &postgres.PaymentIntent{PayerID: payer, PayeeID: payee}
	if !service.IsParty(p, payer) || !service.IsParty(p, payee) {
		t.Fatal("payer/payee must be parties")
	}
	if service.IsParty(p, uuid.New()) || service.IsParty(p, uuid.Nil) || service.IsParty(nil, payer) {
		t.Fatal("stranger / nil actor / nil intent must not be parties")
	}
	if !errors.Is(service.ErrNotIntentParty, service.ErrNotIntentParty) {
		t.Fatal("sentinel identity")
	}
}
