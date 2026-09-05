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
	"time"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// fakeService is an in-memory stand-in for *service.Service. It records
// the calls the webhook + ownership paths make so tests can assert on
// side effects without a Postgres pool.
type fakeService struct {
	mu      sync.Mutex
	intents map[uuid.UUID]*postgres.PaymentIntent
	applied map[string]bool

	webhooks    []service.WebhookInput
	refunds     []service.RefundRequest
	verifyCalls int
}

func newFake() *fakeService {
	return &fakeService{intents: map[uuid.UUID]*postgres.PaymentIntent{}, applied: map[string]bool{}}
}

func (f *fakeService) InitiatePayment(_ context.Context, in service.InitiateInput) (*postgres.PaymentIntent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := &postgres.PaymentIntent{ID: uuid.New(), PayerID: in.PayerID, PayeeID: in.PayeeID, Status: "pending",
		ReferenceType: in.ReferenceType, ReferenceID: in.ReferenceID, AmountMinorRaw: in.AmountMinor,
		ProviderRef: "order_stub_1", OwnerDomain: in.OwnerDomain}
	f.intents[p.ID] = p
	return p, nil
}
func (f *fakeService) GetIntent(_ context.Context, id uuid.UUID) (*postgres.PaymentIntent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
func (f *fakeService) ListByReference(_ context.Context, _ string, refID uuid.UUID) ([]postgres.PaymentIntent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
func (f *fakeService) VerifyIntent(_ context.Context, id uuid.UUID, _, _, _ string, _ int64) (*service.VerifyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifyCalls++
	p, ok := f.intents[id]
	if !ok {
		return nil, postgres.ErrPaymentNotFound
	}
	// Advisory: the stored status is echoed unchanged.
	return &service.VerifyResult{Verified: true, Advisory: true, IntentID: id, Status: p.Status,
		ReferenceType: p.ReferenceType, ReferenceID: p.ReferenceID, PayerID: p.PayerID, PayeeID: p.PayeeID}, nil
}
func (f *fakeService) RequestRefund(_ context.Context, req service.RefundRequest) (*postgres.RefundCommand, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.ProviderIdempotencyKey == "" || req.AmountMinor <= 0 {
		return nil, errors.New("key and positive amount required")
	}
	f.refunds = append(f.refunds, req)
	return &postgres.RefundCommand{ID: uuid.New(), IntentID: req.IntentID, AmountMinor: req.AmountMinor, Status: "pending"}, nil
}
func (f *fakeService) IntentOwnerDomain(_ context.Context, id uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.intents[id]
	if !ok {
		return "", postgres.ErrIntentNotFound
	}
	return p.OwnerDomain, nil
}
func (f *fakeService) ReleaseHold(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (f *fakeService) ApplyWebhook(_ context.Context, in service.WebhookInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.applied[in.EventID] {
		return service.ErrWebhookDuplicate
	}
	f.applied[in.EventID] = true
	f.webhooks = append(f.webhooks, in)
	return nil
}

var _ Service = (*fakeService)(nil)

const (
	testInternalKey   = "internal-key"
	testWebhookSecret = "whsec_test"
)

// commerceCaller is a registered service-token caller for the /internal
// family: commerce-service, allowed the three launch operations on "order".
type commerceCaller struct {
	signer   *servicetoken.Signer
	verifier *servicetoken.Verifier
}

func newCommerceCaller(t *testing.T) commerceCaller {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64("commerce-service", "c1", priv)
	if err != nil {
		t.Fatal(err)
	}
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	if err := v.RegisterBase64("commerce-service", "c1", pub,
		[]string{servicetoken.OpIntentCreate, servicetoken.OpIntentRead, servicetoken.OpRefundCreate},
		[]string{servicetoken.RefOrder}); err != nil {
		t.Fatal(err)
	}
	return commerceCaller{signer: signer, verifier: v}
}

func (c commerceCaller) header(t *testing.T, ops ...string) map[string]string {
	t.Helper()
	tok, err := c.signer.Mint(servicetoken.AudiencePayments, "commerce-service", ops, []string{servicetoken.RefOrder}, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

type routerOpts struct {
	webhookSecret string
	internalKey   string
	caller        *commerceCaller
}

func newRouter(t *testing.T, fake *fakeService, o routerOpts) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(fake)
	if o.webhookSecret != "" {
		h.WithProvider(gateway.NewRazorpayProvider("rzp_test_k", "s", o.webhookSecret))
	}
	if o.internalKey != "" {
		h.WithInternalKey(o.internalKey)
	}
	if o.caller != nil {
		h.WithServiceAuth(o.caller.verifier)
	}
	if err := h.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	return r
}

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func capturedEvent(rzpOrderID, rzpPaymentID string) []byte {
	b, _ := json.Marshal(map[string]any{
		"event":      "payment.captured",
		"created_at": time.Now().Unix(),
		"payload": map[string]any{
			"payment": map[string]any{"entity": map[string]any{
				"id": rzpPaymentID, "order_id": rzpOrderID, "amount": 90000, "currency": "INR", "status": "captured",
			}},
		},
	})
	return b
}

func signedWebhook(eventID string, body []byte) map[string]string {
	return map[string]string{"X-Razorpay-Signature": sign(body, testWebhookSecret), "X-Razorpay-Event-Id": eventID}
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

func withKey(user uuid.UUID) map[string]string {
	h := map[string]string{"X-Internal-Service-Key": testInternalKey}
	if user != uuid.Nil {
		h["X-User-Id"] = user.String()
	}
	return h
}

// TestWebhook_FailsClosed covers the provider signature gate (A3 / LB-6):
// a correctly signed payment.captured is applied atomically; a bad or
// missing signature is refused before any side effect; an event with no
// provider id is refused (R-5); and without a provider adapter at all the
// webhook answers 503 rather than accepting on faith.
func TestWebhook_FailsClosed(t *testing.T) {
	body := capturedEvent("order_rzp_1", "pay_rzp_1")

	t.Run("valid signature applies payment.captured with the money tuple", func(t *testing.T) {
		fake := newFake()
		r := newRouter(t, fake, routerOpts{webhookSecret: testWebhookSecret, internalKey: testInternalKey})
		w := do(r, http.MethodPost, "/v1/payments/webhook", body, signedWebhook("evt_1", body))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if len(fake.webhooks) != 1 {
			t.Fatalf("webhooks applied = %d, want 1", len(fake.webhooks))
		}
		got := fake.webhooks[0]
		if got.EventID != "evt_1" || got.ProviderOrderID != "order_rzp_1" || got.ProviderPaymentID != "pay_rzp_1" ||
			got.AmountMinor != 90000 || got.Currency != "INR" || got.EventType != "payment.captured" {
			t.Fatalf("webhook input = %+v", got)
		}
	})

	t.Run("invalid signature is refused with no side effects", func(t *testing.T) {
		fake := newFake()
		r := newRouter(t, fake, routerOpts{webhookSecret: testWebhookSecret, internalKey: testInternalKey})
		hdr := signedWebhook("evt_1", body)
		hdr["X-Razorpay-Signature"] = sign(body, "wrong")
		w := do(r, http.MethodPost, "/v1/payments/webhook", body, hdr)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
		if len(fake.webhooks) != 0 || len(fake.applied) != 0 {
			t.Fatalf("expected no side effects, got %+v", fake.webhooks)
		}
	})

	t.Run("missing signature is refused", func(t *testing.T) {
		fake := newFake()
		r := newRouter(t, fake, routerOpts{webhookSecret: testWebhookSecret, internalKey: testInternalKey})
		w := do(r, http.MethodPost, "/v1/payments/webhook", body, map[string]string{"X-Razorpay-Event-Id": "evt_1"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})

	t.Run("tampered body fails the original signature", func(t *testing.T) {
		fake := newFake()
		r := newRouter(t, fake, routerOpts{webhookSecret: testWebhookSecret, internalKey: testInternalKey})
		tampered := capturedEvent("order_rzp_OTHER", "pay_rzp_1")
		w := do(r, http.MethodPost, "/v1/payments/webhook", tampered, signedWebhook("evt_1", body))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})

	t.Run("missing provider event id is refused", func(t *testing.T) {
		fake := newFake()
		r := newRouter(t, fake, routerOpts{webhookSecret: testWebhookSecret, internalKey: testInternalKey})
		w := do(r, http.MethodPost, "/v1/payments/webhook", body, map[string]string{"X-Razorpay-Signature": sign(body, testWebhookSecret)})
		if w.Code != http.StatusBadRequest || len(fake.webhooks) != 0 {
			t.Fatalf("status = %d applied=%d, want 400 and none", w.Code, len(fake.webhooks))
		}
	})

	t.Run("no provider adapter (stub mode) answers 503, never accepts", func(t *testing.T) {
		fake := newFake()
		r := newRouter(t, fake, routerOpts{internalKey: testInternalKey})
		w := do(r, http.MethodPost, "/v1/payments/webhook", body, signedWebhook("evt_1", body))
		if w.Code != http.StatusServiceUnavailable || len(fake.webhooks) != 0 {
			t.Fatalf("status = %d applied=%d", w.Code, len(fake.webhooks))
		}
	})

	t.Run("webhook is not behind either credential gate", func(t *testing.T) {
		fake := newFake()
		caller := newCommerceCaller(t)
		r := newRouter(t, fake, routerOpts{webhookSecret: testWebhookSecret, internalKey: testInternalKey, caller: &caller})
		w := do(r, http.MethodPost, "/v1/payments/webhook", body, signedWebhook("evt_1", body))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (Razorpay cannot send our credentials)", w.Code)
		}
	})
}

// TestWebhook_DuplicateIsNoop: Razorpay retries deliveries; the second
// identical event must ack 200 without re-running the effect. The dedupe
// authority is the service's inbox row, surfaced as ErrWebhookDuplicate.
func TestWebhook_DuplicateIsNoop(t *testing.T) {
	fake := newFake()
	r := newRouter(t, fake, routerOpts{webhookSecret: testWebhookSecret, internalKey: testInternalKey})
	body := capturedEvent("order_rzp_2", "pay_rzp_2")
	hdr := signedWebhook("evt_dup", body)

	for i := 0; i < 2; i++ {
		if w := do(r, http.MethodPost, "/v1/payments/webhook", body, hdr); w.Code != http.StatusOK {
			t.Fatalf("delivery %d: status = %d", i+1, w.Code)
		}
	}
	if len(fake.webhooks) != 1 {
		t.Fatalf("effect ran %d times, want exactly 1", len(fake.webhooks))
	}
}

// TestRegisterRoutes_RefusesWithoutCredentials pins A2: the money surface
// is never exposed with no caller credential at all.
func TestRegisterRoutes_RefusesWithoutCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := New(newFake()).RegisterRoutes(gin.New()); err == nil {
		t.Fatal("RegisterRoutes accepted a handler with neither a verifier nor an internal key")
	}
}

// TestRouteSplit_UserFacing pins the on-behalf-of-a-user family: the
// internal key admits the request, X-User-Id decides what it may see, and
// the mutations that used to sit here are gone (410) rather than hidden.
func TestRouteSplit_UserFacing(t *testing.T) {
	fake := newFake()
	r := newRouter(t, fake, routerOpts{internalKey: testInternalKey})
	payer, payee, stranger := uuid.New(), uuid.New(), uuid.New()
	orderID := uuid.New()

	t.Run("intent is created for X-User-Id with a derived owner domain", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"payee_id": payee, "reference_type": "food_order", "reference_id": orderID,
			"amount_minor": 90000, "method": "upi", "idempotency_key": "food:" + orderID.String(),
		})
		w := do(r, http.MethodPost, "/v1/payments/intents", body, withKey(payer))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		for _, p := range fake.intents {
			if p.PayerID != payer || p.OwnerDomain != "food-service" {
				t.Fatalf("intent = payer %s owner %q", p.PayerID, p.OwnerDomain)
			}
		}
	})
	t.Run("intent creation refuses COD, a missing idempotency key and a missing user", func(t *testing.T) {
		mk := func(method, idem string) []byte {
			b, _ := json.Marshal(map[string]any{"payee_id": payee, "reference_type": "food_order", "reference_id": orderID,
				"amount_minor": 90000, "method": method, "idempotency_key": idem})
			return b
		}
		if w := do(r, http.MethodPost, "/v1/payments/intents", mk("cod", "k"), withKey(payer)); w.Code != http.StatusBadRequest {
			t.Fatalf("cod: status = %d", w.Code)
		}
		if w := do(r, http.MethodPost, "/v1/payments/intents", mk("upi", ""), withKey(payer)); w.Code != http.StatusBadRequest {
			t.Fatalf("no key: status = %d", w.Code)
		}
		if w := do(r, http.MethodPost, "/v1/payments/intents", mk("upi", "k"), withKey(uuid.Nil)); w.Code != http.StatusUnauthorized {
			t.Fatalf("no user: status = %d", w.Code)
		}
	})

	var intent *postgres.PaymentIntent
	for _, p := range fake.intents {
		intent = p
	}
	if intent == nil {
		t.Fatal("no intent created")
	}

	t.Run("internal key is required", func(t *testing.T) {
		w := do(r, http.MethodGet, "/v1/payments/intents/"+intent.ID.String(), nil, map[string]string{"X-User-Id": payer.String()})
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
		path := "/v1/payments/intents?ref_type=food_order&ref_id=" + orderID.String()
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
	})
	t.Run("status PATCH and user refund are gone (410), not merely hidden", func(t *testing.T) {
		statusBody := []byte(`{"old_status":"pending","new_status":"succeeded"}`)
		w := do(r, http.MethodPatch, "/v1/payments/intents/"+intent.ID.String()+"/status", statusBody, withKey(payer))
		if w.Code != http.StatusGone || intent.Status != "pending" {
			t.Fatalf("status = %d intent=%q, want 410/pending", w.Code, intent.Status)
		}
		w = do(r, http.MethodPost, "/v1/payments/intents/"+intent.ID.String()+"/refund", []byte(`{}`), withKey(payer))
		if w.Code != http.StatusGone || len(fake.refunds) != 0 {
			t.Fatalf("status = %d refunds=%d, want 410/0", w.Code, len(fake.refunds))
		}
	})
	t.Run("verify is not on the user-facing family", func(t *testing.T) {
		body := []byte(`{"razorpay_order_id":"order_stub_1","razorpay_payment_id":"pay_1","razorpay_signature":"sig"}`)
		w := do(r, http.MethodPost, "/v1/payments/intents/"+intent.ID.String()+"/verify", body, withKey(payer))
		if w.Code != http.StatusNotFound || fake.verifyCalls != 0 {
			t.Fatalf("status = %d verifyCalls=%d", w.Code, fake.verifyCalls)
		}
	})
}

// TestRouteSplit_ServiceToken pins the A2 family: a scoped Ed25519 token
// admits commerce-service to intents it owns, and only those.
func TestRouteSplit_ServiceToken(t *testing.T) {
	fake := newFake()
	caller := newCommerceCaller(t)
	r := newRouter(t, fake, routerOpts{caller: &caller})
	payer, payee := uuid.New(), uuid.New()
	orderID := uuid.New()
	createBody, _ := json.Marshal(map[string]any{
		"payer_id": payer, "payee_id": payee, "reference_type": "order", "reference_id": orderID,
		"amount_minor": 90000, "method": "upi", "idempotency_key": "order:" + orderID.String(),
	})

	t.Run("no credential at all is refused", func(t *testing.T) {
		w := do(r, http.MethodPost, "/v1/payments/internal/intents", createBody, nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
		// With no internal key configured, the key is not a credential here.
		w = do(r, http.MethodPost, "/v1/payments/internal/intents", createBody, withKey(uuid.Nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("key without fallback: status = %d, want 401", w.Code)
		}
	})
	t.Run("a token lacking the operation is refused", func(t *testing.T) {
		w := do(r, http.MethodPost, "/v1/payments/internal/intents", createBody, caller.header(t, servicetoken.OpIntentRead))
		if w.Code != http.StatusForbidden || len(fake.intents) != 0 {
			t.Fatalf("status = %d intents=%d", w.Code, len(fake.intents))
		}
	})
	t.Run("a token for another reference type cannot create an order intent", func(t *testing.T) {
		foodBody, _ := json.Marshal(map[string]any{
			"payer_id": payer, "payee_id": payee, "reference_type": "food_order", "reference_id": orderID,
			"amount_minor": 90000, "method": "upi", "idempotency_key": "x",
		})
		w := do(r, http.MethodPost, "/v1/payments/internal/intents", foodBody, caller.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", w.Code)
		}
	})

	var intent *postgres.PaymentIntent
	t.Run("intent created under the caller's domain", func(t *testing.T) {
		w := do(r, http.MethodPost, "/v1/payments/internal/intents", createBody, caller.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		for _, p := range fake.intents {
			intent = p
		}
		if intent == nil || intent.OwnerDomain != "commerce-service" || intent.PayerID != payer {
			t.Fatalf("intent = %+v", intent)
		}
	})
	t.Run("verify is advisory and echoes the parties + reference", func(t *testing.T) {
		body := []byte(`{"razorpay_order_id":"order_stub_1","razorpay_payment_id":"pay_1","razorpay_signature":"sig"}`)
		w := do(r, http.MethodPost, "/v1/payments/internal/intents/"+intent.ID.String()+"/verify", body, caller.header(t, servicetoken.OpIntentRead))
		if w.Code != http.StatusOK || fake.verifyCalls != 1 {
			t.Fatalf("status = %d verifyCalls=%d body=%s", w.Code, fake.verifyCalls, w.Body.String())
		}
		var env struct {
			Data service.VerifyResult `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if !env.Data.Advisory || env.Data.Status != "pending" || env.Data.ReferenceID != orderID || env.Data.PayerID != payer {
			t.Fatalf("verify result = %+v", env.Data)
		}
	})
	t.Run("refund needs an explicit amount + key and is ACCEPTED, not settled", func(t *testing.T) {
		w := do(r, http.MethodPost, "/v1/payments/internal/intents/"+intent.ID.String()+"/refund", []byte(`{"reason":"cancel"}`), caller.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("no amount: status = %d", w.Code)
		}
		body := []byte(`{"reason":"cancel","amount_minor":90000,"idempotency_key":"refund:order:1"}`)
		w = do(r, http.MethodPost, "/v1/payments/internal/intents/"+intent.ID.String()+"/refund", body, caller.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusAccepted || len(fake.refunds) != 1 {
			t.Fatalf("status = %d refunds=%d body=%s", w.Code, len(fake.refunds), w.Body.String())
		}
		if got := fake.refunds[0]; got.CallerDomain != "commerce-service" || got.ProviderIdempotencyKey != "refund:order:1" {
			t.Fatalf("refund request = %+v", got)
		}
	})
	t.Run("an intent owned by another domain reads as not found", func(t *testing.T) {
		other, _ := fake.InitiatePayment(context.Background(), service.InitiateInput{PayerID: payer, PayeeID: payee,
			ReferenceType: "food_order", ReferenceID: uuid.New(), AmountMinor: 100, OwnerDomain: "food-service"})
		w := do(r, http.MethodGet, "/v1/payments/internal/intents/"+other.ID.String(), nil, caller.header(t, servicetoken.OpIntentRead))
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})
	t.Run("status PATCH is gone on the internal path too", func(t *testing.T) {
		statusBody := []byte(`{"old_status":"pending","new_status":"succeeded"}`)
		w := do(r, http.MethodPatch, "/v1/payments/internal/intents/"+intent.ID.String()+"/status", statusBody, caller.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusGone || intent.Status != "pending" {
			t.Fatalf("status = %d intent=%q, want 410/pending", w.Code, intent.Status)
		}
	})
}

// TestRouteSplit_LegacyInternalKey pins the fallback for callers not yet
// issuing service tokens (food-service): the shared key admits them to
// /internal, the refund amount defaults to the remaining balance with a
// derived idempotency key, and a wrong key is still refused.
func TestRouteSplit_LegacyInternalKey(t *testing.T) {
	fake := newFake()
	caller := newCommerceCaller(t)
	r := newRouter(t, fake, routerOpts{internalKey: testInternalKey, caller: &caller})
	payer, payee := uuid.New(), uuid.New()
	orderID := uuid.New()
	intent, _ := fake.InitiatePayment(context.Background(), service.InitiateInput{PayerID: payer, PayeeID: payee,
		ReferenceType: "food_order", ReferenceID: orderID, AmountMinor: 50000, OwnerDomain: "food-service"})
	intent.Status = "succeeded"

	t.Run("wrong key is refused", func(t *testing.T) {
		w := do(r, http.MethodGet, "/v1/payments/internal/intents?ref_type=food_order&ref_id="+orderID.String(), nil,
			map[string]string{"X-Internal-Service-Key": "nope"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
	t.Run("unfiltered list on the internal path", func(t *testing.T) {
		var env struct {
			Data []postgres.PaymentIntent `json:"data"`
		}
		w := do(r, http.MethodGet, "/v1/payments/internal/intents?ref_type=food_order&ref_id="+orderID.String(), nil, withKey(uuid.Nil))
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if w.Code != 200 || len(env.Data) != 1 {
			t.Fatalf("status=%d n=%d", w.Code, len(env.Data))
		}
	})
	t.Run("verify with the key, attributing the acting user", func(t *testing.T) {
		body := []byte(`{"razorpay_order_id":"order_stub_1","razorpay_payment_id":"pay_1","razorpay_signature":"sig"}`)
		w := do(r, http.MethodPost, "/v1/payments/internal/intents/"+intent.ID.String()+"/verify", body, withKey(payer))
		if w.Code != http.StatusOK || fake.verifyCalls != 1 {
			t.Fatalf("status = %d verifyCalls=%d body=%s", w.Code, fake.verifyCalls, w.Body.String())
		}
	})
	t.Run("refund with only a reason: full remaining balance, derived key, owner domain", func(t *testing.T) {
		w := do(r, http.MethodPost, "/v1/payments/internal/intents/"+intent.ID.String()+"/refund", []byte(`{"reason":"support:42"}`), withKey(payer))
		if w.Code != http.StatusAccepted || len(fake.refunds) != 1 {
			t.Fatalf("status = %d refunds=%d body=%s", w.Code, len(fake.refunds), w.Body.String())
		}
		got := fake.refunds[0]
		if got.AmountMinor != 50000 || got.CallerDomain != "food-service" || got.ProviderIdempotencyKey != "legacy:"+intent.ID.String()+":support:42" {
			t.Fatalf("refund request = %+v", got)
		}
	})
	t.Run("a service token still works alongside the fallback", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"payer_id": payer, "payee_id": payee, "reference_type": "order", "reference_id": uuid.New(),
			"amount_minor": 100, "method": "upi", "idempotency_key": "order:x",
		})
		w := do(r, http.MethodPost, "/v1/payments/internal/intents", body, caller.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
	})
}

// TestIsParty pins the ownership predicate the user-facing handlers rely on.
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
