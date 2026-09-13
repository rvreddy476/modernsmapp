package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Golden contract fixtures for lane B7 (Feast payment contract): the intent
// response with and without a client_session, GET /orders/:id/payment in each
// status, the foreign/missing 404, the non-online 409 and the launch method
// refusals. Regenerate deliberately with UPDATE_CONTRACTS=1 and review the diff.

var (
	ctPayOrder   = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0030")
	ctPayForeign = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0031")
	ctPayMissing = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0032")
	ctPayIntent  = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0033")
	ctPayRow     = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0034")
)

const (
	ctProviderOrder = "order_ContractRzp01"
	ctRazorpayKeyID = "rzp_test_ContractKey01"
	// ctRazorpaySecret stands in for a key secret payments-service must never
	// send, and food must never relay if it did.
	ctRazorpaySecret = "ContractKeySecretMustNeverLeak"
)

// 12:01:05 IST; the response carries it in UTC (06:31:05Z).
var ctPayUpdatedAt = time.Date(2026, 9, 13, 12, 1, 5, 0, time.FixedZone("IST", 5*3600+1800))

type payContractStore struct {
	service.Store
	state       postgres.CustomerPaymentState
	intentCalls int
}

func (f *payContractStore) WalletPaymentChargeDetails(context.Context, uuid.UUID, uuid.UUID) (*postgres.WalletPaymentChargeDetails, error) {
	return &postgres.WalletPaymentChargeDetails{OrderID: ctPayOrder, OrderNumber: "FG1000000000030", UserID: ctCustomer, RestaurantOwnerID: ctOwner,
		PaymentMethod: "ONLINE", PaymentStatus: "PENDING", Amount: 250, AmountMinor: 25000, PaymentInstrument: "upi"}, nil
}

// CreatePaymentIntent mirrors the real store's paymentIntentTx map.
func (f *payContractStore) CreatePaymentIntent(_ context.Context, _, orderID uuid.UUID, method, instrument, _ string) (map[string]any, error) {
	f.intentCalls++
	providerOrder := "food_order_" + orderID.String() + "_1757745065000000000"
	return map[string]any{
		"id": ctPayRow.String(), "order_id": orderID, "method": method, "status": "PENDING",
		"provider": "payments-service", "provider_payment_id": "", "provider_order_id": providerOrder,
		"amount": 250.0, "currency": "INR",
		"payment_intent": map[string]any{"id": ctPayRow.String(), "instrument": instrument, "provider_ref": providerOrder},
	}, nil
}

func (f *payContractStore) AttachPaymentProviderReference(context.Context, uuid.UUID, uuid.UUID, string, string, map[string]any) error {
	return nil
}

// CustomerPaymentStatus answers only ctCustomer's ctPayOrder, as the real
// query's WHERE clause does (proved on a database by the integration tests).
func (f *payContractStore) CustomerPaymentStatus(_ context.Context, userID, orderID uuid.UUID) (*postgres.CustomerPaymentState, error) {
	if userID != ctCustomer || orderID != ctPayOrder {
		return nil, pgx.ErrNoRows
	}
	s := f.state
	return &s, nil
}

type payContractClient struct {
	session map[string]string
	created int
}

func (c *payContractClient) CreateIntent(_ context.Context, in payments.CreateIntentInput) (*payments.Intent, error) {
	c.created++
	return &payments.Intent{ID: ctPayIntent, Status: "pending", AmountMinor: in.AmountMinor, Currency: "INR", Method: in.Method,
		ProviderRef: ctProviderOrder, ReferenceType: "food_order", ReferenceID: in.OrderID,
		PayerID: in.PayerID, PayeeID: in.PayeeID, ClientSession: c.session}, nil
}

func (c *payContractClient) VerifyCallback(context.Context, uuid.UUID, string, string, string, int64) (*payments.CallbackVerdict, error) {
	return nil, fmt.Errorf("not used")
}

func (c *payContractClient) Refund(context.Context, uuid.UUID, int64, string, string) (*payments.RefundAccepted, error) {
	return nil, fmt.Errorf("not used")
}

// ctRazorpaySession is what payments-service's Razorpay adapter sends, plus
// fields that must be dropped.
func ctRazorpaySession() map[string]string {
	return map[string]string{
		"provider": "razorpay", "order_id": ctProviderOrder, "key_id": ctRazorpayKeyID,
		"key_secret": ctRazorpaySecret, "amount": "25000",
	}
}

func payState(orderStatus, paymentStatus, method string, applied bool) postgres.CustomerPaymentState {
	return postgres.CustomerPaymentState{OrderID: ctPayOrder, OrderStatus: orderStatus, PaymentStatus: paymentStatus, PaymentMethod: method,
		AmountMinor: 25000, Currency: "INR", CaptureApplied: applied, UpdatedAt: ctPayUpdatedAt}
}

func payContractRouter(st *payContractStore, pc *payContractClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.New(st).WithPayments(pc).WithPaymentFlags(payments.Flags{})).RegisterRoutes(router)
	return router
}

// b7Fixtures lists the fixtures this file owns, for the leak test.
var b7Fixtures = []string{
	"payment_intent_post_201_client_session", "payment_intent_post_201_no_client_session",
	"payment_intent_post_422_cod", "payment_intent_post_422_wallet", "payment_intent_post_422_unknown_method",
	"order_payment_get_200_confirming", "order_payment_get_200_paid", "order_payment_get_200_paid_refund_pending",
	"order_payment_get_200_failed", "order_payment_get_404", "order_payment_get_409_not_online",
}

func TestPaymentIntentContracts(t *testing.T) {
	intents := "/v1/food/orders/" + ctPayOrder.String() + "/payments/intents"
	cases := []struct {
		fixture string
		body    string
		session map[string]string
		status  int
		// reached: the store and payments were asked for an intent.
		reached bool
	}{
		{"payment_intent_post_201_client_session", `{"method":"upi"}`, ctRazorpaySession(), http.StatusCreated, true},
		// The dev stub gateway attaches no session: the field is omitted.
		{"payment_intent_post_201_no_client_session", `{"method":"card"}`, nil, http.StatusCreated, true},
		{"payment_intent_post_422_cod", `{"method":"cod"}`, ctRazorpaySession(), http.StatusUnprocessableEntity, false},
		{"payment_intent_post_422_wallet", `{"method":"wallet"}`, ctRazorpaySession(), http.StatusUnprocessableEntity, false},
		{"payment_intent_post_422_unknown_method", `{"method":"net_banking"}`, ctRazorpaySession(), http.StatusUnprocessableEntity, false},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			st := &payContractStore{}
			pc := &payContractClient{session: tc.session}
			rec := doJSON(payContractRouter(st, pc), http.MethodPost, intents, tc.body, ctCustomer, false)
			assertContract(t, rec, tc.status, tc.fixture)
			if reached := st.intentCalls == 1 && pc.created == 1; reached != tc.reached {
				t.Fatalf("store intents %d, payments intents %d, want reached=%v", st.intentCalls, pc.created, tc.reached)
			}
		})
	}
}

// The client_session is exactly provider/order_id/key_id, whatever payments
// sends, and nothing secret appears anywhere in the response.
func TestPaymentIntentClientSessionIsExactlyPublic(t *testing.T) {
	st := &payContractStore{}
	rec := doJSON(payContractRouter(st, &payContractClient{session: ctRazorpaySession()}), http.MethodPost,
		"/v1/food/orders/"+ctPayOrder.String()+"/payments/intents", `{"method":"upi"}`, ctCustomer, false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var session map[string]string
	if err := json.Unmarshal(env.Data["client_session"], &session); err != nil {
		t.Fatalf("client_session: %v (%s)", err, env.Data["client_session"])
	}
	want := map[string]string{"provider": "razorpay", "order_id": ctProviderOrder, "key_id": ctRazorpayKeyID}
	if len(session) != len(want) {
		t.Fatalf("client_session = %v, want exactly %v", session, want)
	}
	for k, v := range want {
		if session[k] != v {
			t.Fatalf("client_session = %v, want exactly %v", session, want)
		}
	}
	body := rec.Body.String()
	for _, forbidden := range []string{ctRazorpaySecret, "key_secret", "payer_id", "payee_id", ctOwner.String()} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("intent response carries %q: %s", forbidden, body)
		}
	}

	// A session for another provider order is not relayed.
	other := ctRazorpaySession()
	other["order_id"] = "order_SomeoneElse"
	rec = doJSON(payContractRouter(&payContractStore{}, &payContractClient{session: other}), http.MethodPost,
		"/v1/food/orders/"+ctPayOrder.String()+"/payments/intents", `{"method":"upi"}`, ctCustomer, false)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "client_session") {
		t.Fatalf("mismatched session relayed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestOrderPaymentContracts(t *testing.T) {
	path := func(order uuid.UUID) string { return "/v1/food/orders/" + order.String() + "/payment" }
	cases := []struct {
		name    string
		fixture string
		order   uuid.UUID
		user    uuid.UUID
		state   postgres.CustomerPaymentState
		status  int
	}{
		{"confirming", "order_payment_get_200_confirming", ctPayOrder, ctCustomer, payState("PAYMENT_PENDING", "PENDING", "ONLINE", false), http.StatusOK},
		// payment_status CAPTURED without the applied event is still confirming.
		{"captured without event", "order_payment_get_200_confirming", ctPayOrder, ctCustomer, payState("PAYMENT_PENDING", "CAPTURED", "ONLINE", false), http.StatusOK},
		{"paid", "order_payment_get_200_paid", ctPayOrder, ctCustomer, payState("CONFIRMED", "CAPTURED", "ONLINE", true), http.StatusOK},
		{"paid refund pending", "order_payment_get_200_paid_refund_pending", ctPayOrder, ctCustomer, payState("REFUND_PENDING", "REFUND_PENDING", "ONLINE", true), http.StatusOK},
		{"failed", "order_payment_get_200_failed", ctPayOrder, ctCustomer, payState("PAYMENT_FAILED", "FAILED", "ONLINE", false), http.StatusOK},
		{"foreign", "order_payment_get_404", ctPayOrder, ctPayForeign, payState("CONFIRMED", "CAPTURED", "ONLINE", true), http.StatusNotFound},
		{"missing", "order_payment_get_404", ctPayMissing, ctCustomer, payState("CONFIRMED", "CAPTURED", "ONLINE", true), http.StatusNotFound},
		{"cash on delivery", "order_payment_get_409_not_online", ctPayOrder, ctCustomer, payState("CONFIRMED", "NOT_REQUIRED", "COD", false), http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(payContractRouter(&payContractStore{state: tc.state}, &payContractClient{}), http.MethodGet, path(tc.order), ``, tc.user, false)
			assertContract(t, rec, tc.status, tc.fixture)
		})
	}

	// Foreign and missing are byte-identical.
	st := &payContractStore{state: payState("CONFIRMED", "CAPTURED", "ONLINE", true)}
	foreign := doJSON(payContractRouter(st, &payContractClient{}), http.MethodGet, path(ctPayOrder), ``, ctPayForeign, false)
	missing := doJSON(payContractRouter(st, &payContractClient{}), http.MethodGet, path(ctPayMissing), ``, ctCustomer, false)
	if foreign.Code != missing.Code || !bytes.Equal(foreign.Body.Bytes(), missing.Body.Bytes()) {
		t.Fatalf("foreign %d %s != missing %d %s", foreign.Code, foreign.Body.String(), missing.Code, missing.Body.String())
	}
}

func TestB7FixturesCarryNothingPrivate(t *testing.T) {
	for _, name := range b7Fixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, forbidden := range []string{ctRazorpaySecret, "key_secret", "secret", "payer_id", "payee_id", ctOwner.String()} {
			if bytes.Contains(raw, []byte(forbidden)) {
				t.Fatalf("%s carries %q", name, forbidden)
			}
		}
	}
}

// The status fixtures carry exactly the documented fields.
func TestOrderPaymentFixtureFields(t *testing.T) {
	want := []string{"amount_minor", "currency", "order_id", "refund_status", "status", "updated_at"}
	for _, name := range []string{"order_payment_get_200_confirming", "order_payment_get_200_paid", "order_payment_get_200_paid_refund_pending", "order_payment_get_200_failed"} {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var env struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		var got []string
		for k := range env.Data {
			got = append(got, k)
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s fields = %v, want %v", name, got, want)
		}
	}
}
