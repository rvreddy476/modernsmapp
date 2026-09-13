package http

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type paymentsHTTPStore struct {
	service.Store
	orderID uuid.UUID
	userID  uuid.UUID
	err     error
}

func (f *paymentsHTTPStore) WalletPaymentChargeDetails(context.Context, uuid.UUID, uuid.UUID) (*postgres.WalletPaymentChargeDetails, error) {
	return &postgres.WalletPaymentChargeDetails{OrderID: f.orderID, UserID: f.userID, PaymentMethod: "ONLINE", PaymentStatus: "PENDING", AmountMinor: 25000, PaymentInstrument: "upi"}, nil
}

func (f *paymentsHTTPStore) PaymentIntegrationDetails(context.Context, uuid.UUID) (*postgres.PaymentIntegrationDetails, error) {
	return &postgres.PaymentIntegrationDetails{OrderID: f.orderID, UserID: f.userID, PaymentMethod: "ONLINE", PaymentStatus: "PENDING", ProviderPaymentID: uuid.NewString(), AmountMinor: 25000}, nil
}

func (f *paymentsHTTPStore) GetOrder(context.Context, uuid.UUID, uuid.UUID) (*postgres.Order, error) {
	return &postgres.Order{ID: f.orderID, Status: "PAYMENT_PENDING", PaymentStatus: "PENDING"}, nil
}

func (f *paymentsHTTPStore) CreatePaymentIntent(context.Context, uuid.UUID, uuid.UUID, string, string, string) (map[string]any, error) {
	return nil, f.err
}

func (f *paymentsHTTPStore) AdminRequestRefund(context.Context, uuid.UUID, uuid.UUID, string, float64, string) (*postgres.RefundPlan, error) {
	return nil, f.err
}

type paymentsHTTPClient struct{ orderID, userID uuid.UUID }

func (c *paymentsHTTPClient) CreateIntent(context.Context, payments.CreateIntentInput) (*payments.Intent, error) {
	return nil, fmt.Errorf("not used")
}

func (c *paymentsHTTPClient) VerifyCallback(_ context.Context, _ uuid.UUID, _, _, _ string, expected int64) (*payments.CallbackVerdict, error) {
	return &payments.CallbackVerdict{Verified: true, Advisory: true, AmountMinor: expected, PayerID: c.userID, ReferenceType: "food_order", ReferenceID: c.orderID}, nil
}

func (c *paymentsHTTPClient) Refund(context.Context, uuid.UUID, int64, string, string) (*payments.RefundAccepted, error) {
	return nil, fmt.Errorf("not used")
}

func paymentsRouter(st *paymentsHTTPStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	svc := service.New(st).
		WithPayments(&paymentsHTTPClient{orderID: st.orderID, userID: st.userID}).
		WithPaymentFlags(payments.Flags{})
	New(svc).RegisterRoutes(router)
	return router
}

func doJSON(router *gin.Engine, method, path, body string, userID uuid.UUID, admin bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", userID.String())
	req.Header.Set("Idempotency-Key", uuid.NewString())
	if admin {
		req.Header.Set("X-Scopes", "admin")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestConfirmPaymentHandler_Returns202Confirming(t *testing.T) {
	st := &paymentsHTTPStore{orderID: uuid.New(), userID: uuid.New()}
	rec := doJSON(paymentsRouter(st), http.MethodPost, "/v1/food/orders/"+st.orderID.String()+"/payments/confirm",
		`{"razorpay_order_id":"o","razorpay_payment_id":"p","razorpay_signature":"s","amount_minor":1}`, st.userID, false)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"state":"confirming"`) {
		t.Fatalf("body %s does not say confirming", rec.Body.String())
	}
}

func TestPaymentErrorMapping(t *testing.T) {
	orderID := uuid.New()
	cases := []struct {
		name, method, path, body string
		admin                    bool
		err                      error
		status                   int
		code                     string
	}{
		{"cod intent unavailable", http.MethodPost, "/v1/food/orders/" + orderID.String() + "/payments/intents", `{"method":"cod"}`, false,
			nil, http.StatusUnprocessableEntity, "PAYMENT_METHOD_UNAVAILABLE"},
		{"wallet intent unavailable", http.MethodPost, "/v1/food/orders/" + orderID.String() + "/payments/intents", `{"method":"WALLET"}`, false,
			nil, http.StatusUnprocessableEntity, "PAYMENT_METHOD_UNAVAILABLE"},
		{"invalid intent method", http.MethodPost, "/v1/food/orders/" + orderID.String() + "/payments/intents", `{"method":"net_banking"}`, false,
			nil, http.StatusUnprocessableEntity, "PAYMENT_METHOD_INVALID"},
		{"cod place order unavailable", http.MethodPost, "/v1/food/orders", `{"address_id":"` + uuid.NewString() + `","payment_method":"COD"}`, false,
			nil, http.StatusUnprocessableEntity, "PAYMENT_METHOD_UNAVAILABLE"},
		{"place order without a method", http.MethodPost, "/v1/food/orders", `{"address_id":"` + uuid.NewString() + `"}`, false,
			nil, http.StatusUnprocessableEntity, "PAYMENT_METHOD_INVALID"},
		{"intent from a bad state", http.MethodPost, "/v1/food/orders/" + orderID.String() + "/payments/intents", `{"method":"upi"}`, false,
			fmt.Errorf("%w: CONFIRMED", postgres.ErrPaymentNotAllowedFromState), http.StatusConflict, "FOOD_PAYMENT_NOT_ALLOWED_FROM_STATE"},
		{"refund not eligible", http.MethodPost, "/v1/food/admin/orders/" + orderID.String() + "/refund", `{"reason":"x"}`, true,
			postgres.ErrRefundNotEligible, http.StatusConflict, "FOOD_REFUND_NOT_ELIGIBLE"},
		{"refund transition refused", http.MethodPost, "/v1/food/admin/orders/" + orderID.String() + "/refund", `{"reason":"x"}`, true,
			fmt.Errorf("%w: PREPARING -> REFUND_PENDING", postgres.ErrOrderTransitionNotAllowed), http.StatusConflict, "FOOD_ORDER_TRANSITION_NOT_ALLOWED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &paymentsHTTPStore{orderID: orderID, userID: uuid.New(), err: tc.err}
			rec := doJSON(paymentsRouter(st), tc.method, tc.path, tc.body, st.userID, tc.admin)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("status = %d body = %s, want %d %s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}
		})
	}
}

func TestCODStateErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/x", func(c *gin.Context) {
		if !writeKnownError(c, fmt.Errorf("%w: from CONFIRMED", postgres.ErrCODNotAllowed)) {
			c.Status(http.StatusTeapot)
		}
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "FOOD_COD_NOT_ALLOWED_FROM_STATE") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}
