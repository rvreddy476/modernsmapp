package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// payFakeStore embeds Store so any method the payment paths are NOT supposed
// to reach panics on the nil interface. In particular there is no
// ConfirmPayment writer at all any more, and MarkWalletPaid is counted.
type payFakeStore struct {
	Store
	details         postgres.WalletPaymentChargeDetails
	detailsAfter    *postgres.WalletPaymentChargeDetails
	detailsCalls    int
	integ           postgres.PaymentIntegrationDetails
	order           postgres.Order
	placed          []postgres.PlaceOrderInput
	intents         [][2]string
	attached        []string
	walletPaidCalls int
	plan            *postgres.RefundPlan
	submitted       []uuid.UUID
	walletFinalised []uuid.UUID
}

func (f *payFakeStore) WalletPaymentChargeDetails(context.Context, uuid.UUID, uuid.UUID) (*postgres.WalletPaymentChargeDetails, error) {
	f.detailsCalls++
	if f.detailsCalls > 1 && f.detailsAfter != nil {
		d := *f.detailsAfter
		return &d, nil
	}
	d := f.details
	return &d, nil
}

func (f *payFakeStore) PaymentIntegrationDetails(context.Context, uuid.UUID) (*postgres.PaymentIntegrationDetails, error) {
	d := f.integ
	return &d, nil
}

func (f *payFakeStore) GetOrder(context.Context, uuid.UUID, uuid.UUID) (*postgres.Order, error) {
	o := f.order
	return &o, nil
}

func (f *payFakeStore) PlaceOrder(_ context.Context, _ uuid.UUID, in postgres.PlaceOrderInput, _ string) (*postgres.Order, error) {
	f.placed = append(f.placed, in)
	o := f.order
	return &o, nil
}

func (f *payFakeStore) CreatePaymentIntent(_ context.Context, _, _ uuid.UUID, method, instrument, _ string) (map[string]any, error) {
	f.intents = append(f.intents, [2]string{method, instrument})
	return map[string]any{"id": uuid.NewString(), "method": method}, nil
}

func (f *payFakeStore) AttachPaymentProviderReference(_ context.Context, _, _ uuid.UUID, providerPaymentID, _ string, _ map[string]any) error {
	f.attached = append(f.attached, providerPaymentID)
	return nil
}

func (f *payFakeStore) MarkWalletPaid(context.Context, uuid.UUID, uuid.UUID) (*postgres.Order, error) {
	f.walletPaidCalls++
	o := f.order
	return &o, nil
}

func (f *payFakeStore) AdminRequestRefund(context.Context, uuid.UUID, uuid.UUID, string, float64, string) (*postgres.RefundPlan, error) {
	p := *f.plan
	return &p, nil
}

func (f *payFakeStore) MarkRefundSubmitted(_ context.Context, refundID uuid.UUID, _ map[string]any) error {
	f.submitted = append(f.submitted, refundID)
	return nil
}

func (f *payFakeStore) FinalizeWalletRefund(_ context.Context, refundID uuid.UUID) error {
	f.walletFinalised = append(f.walletFinalised, refundID)
	return nil
}

type refundCall struct {
	intent uuid.UUID
	amount int64
	reason string
	key    string
}

type fakePayments struct {
	created  []payments.CreateIntentInput
	verified []int64
	verdict  payments.CallbackVerdict
	refunds  []refundCall
}

func (f *fakePayments) CreateIntent(_ context.Context, in payments.CreateIntentInput) (*payments.Intent, error) {
	f.created = append(f.created, in)
	return &payments.Intent{ID: uuid.New(), AmountMinor: in.AmountMinor, ReferenceID: in.OrderID, ReferenceType: "food_order", ProviderRef: "order_rzp"}, nil
}

func (f *fakePayments) VerifyCallback(_ context.Context, _ uuid.UUID, _, _, _ string, expected int64) (*payments.CallbackVerdict, error) {
	f.verified = append(f.verified, expected)
	v := f.verdict
	return &v, nil
}

func (f *fakePayments) Refund(_ context.Context, intentID uuid.UUID, amountMinor int64, reason, key string) (*payments.RefundAccepted, error) {
	f.refunds = append(f.refunds, refundCall{intentID, amountMinor, reason, key})
	return &payments.RefundAccepted{CommandID: uuid.New(), IntentID: intentID, AmountMinor: amountMinor, Status: "pending"}, nil
}

var (
	sOrderID  = uuid.MustParse("aaaaaaaa-1111-0000-0000-000000000001")
	sUserID   = uuid.MustParse("bbbbbbbb-1111-0000-0000-000000000002")
	sOwnerID  = uuid.MustParse("dddddddd-1111-0000-0000-000000000004")
	sIntentID = uuid.MustParse("cccccccc-1111-0000-0000-000000000003")
)

func onlinePendingStore() *payFakeStore {
	return &payFakeStore{
		details: postgres.WalletPaymentChargeDetails{
			OrderID: sOrderID, UserID: sUserID, RestaurantOwnerID: sOwnerID,
			PaymentMethod: "ONLINE", PaymentStatus: "PENDING", Amount: 250.5, AmountMinor: 25050,
			PaymentInstrument: "upi",
		},
		integ: postgres.PaymentIntegrationDetails{
			OrderID: sOrderID, UserID: sUserID, RestaurantOwnerID: sOwnerID,
			PaymentMethod: "ONLINE", PaymentStatus: "PENDING", ProviderPaymentID: sIntentID.String(),
			Amount: 250.5, AmountMinor: 25050,
		},
		order: postgres.Order{ID: sOrderID, UserID: sUserID, Status: "PAYMENT_PENDING", PaymentStatus: "PENDING", PaymentMethod: "ONLINE"},
	}
}

func matchingVerdict() payments.CallbackVerdict {
	return payments.CallbackVerdict{
		Verified: true, Advisory: true, Status: "pending", AmountMinor: 25050,
		PayerID: sUserID, PayeeID: sOwnerID, ReferenceType: "food_order", ReferenceID: sOrderID,
	}
}

func confirmInput() ConfirmPaymentInput {
	return ConfirmPaymentInput{
		UserID: sUserID, OrderID: sOrderID,
		RazorpayOrderID: "order_rzp", RazorpayPaymentID: "pay_rzp", RazorpaySignature: "sig",
		// The client's amount is ignored; the server's order total is sent.
		AmountMinor: 1,
	}
}

func TestConfirmPayment_ReturnsConfirmingAndNeverMarksPaid(t *testing.T) {
	st := onlinePendingStore()
	pm := &fakePayments{verdict: matchingVerdict()}
	svc := New(st).WithPayments(pm).WithPaymentFlags(payments.Flags{})

	res, err := svc.ConfirmPayment(context.Background(), confirmInput())
	if err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}
	if res.State != PaymentStateConfirming || !res.Verified {
		t.Fatalf("result = %+v, want confirming + verified", res)
	}
	if len(pm.verified) != 1 || pm.verified[0] != 25050 {
		t.Fatalf("verify must be called once with the server total, got %v", pm.verified)
	}
	if st.walletPaidCalls != 0 {
		t.Fatalf("confirm marked the order paid")
	}
	if res.Order == nil || res.Order.PaymentStatus == "CAPTURED" {
		t.Fatalf("order in result = %+v", res.Order)
	}
}

func TestConfirmPayment_PaidOnlyWhenAlreadyCaptured(t *testing.T) {
	st := onlinePendingStore()
	st.details.PaymentStatus = "CAPTURED"
	pm := &fakePayments{verdict: matchingVerdict()}
	svc := New(st).WithPayments(pm).WithPaymentFlags(payments.Flags{})
	res, err := svc.ConfirmPayment(context.Background(), confirmInput())
	if err != nil {
		t.Fatalf("ConfirmPayment: %v", err)
	}
	if res.State != PaymentStatePaid {
		t.Fatalf("state = %s, want paid", res.State)
	}
	if len(pm.verified) != 0 || st.walletPaidCalls != 0 {
		t.Fatalf("an already captured order re-verified or re-marked")
	}

	// Captured by the event consumer while verify was in flight.
	st = onlinePendingStore()
	after := st.details
	after.PaymentStatus = "CAPTURED"
	st.detailsAfter = &after
	svc = New(st).WithPayments(&fakePayments{verdict: matchingVerdict()}).WithPaymentFlags(payments.Flags{})
	res, err = svc.ConfirmPayment(context.Background(), confirmInput())
	if err != nil || res.State != PaymentStatePaid {
		t.Fatalf("captured during verify: res=%+v err=%v", res, err)
	}
	if st.walletPaidCalls != 0 {
		t.Fatal("confirm marked the order paid")
	}
}

func TestConfirmPayment_RefusesMismatchedEcho(t *testing.T) {
	cases := map[string]func(*payments.CallbackVerdict){
		"payer":          func(v *payments.CallbackVerdict) { v.PayerID = uuid.New() },
		"amount":         func(v *payments.CallbackVerdict) { v.AmountMinor = 25049 },
		"reference id":   func(v *payments.CallbackVerdict) { v.ReferenceID = uuid.New() },
		"reference type": func(v *payments.CallbackVerdict) { v.ReferenceType = "order" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			v := matchingVerdict()
			mutate(&v)
			st := onlinePendingStore()
			svc := New(st).WithPayments(&fakePayments{verdict: v}).WithPaymentFlags(payments.Flags{})
			_, err := svc.ConfirmPayment(context.Background(), confirmInput())
			if !errors.Is(err, ErrPaymentCallbackMismatch) {
				t.Fatalf("err = %v, want ErrPaymentCallbackMismatch", err)
			}
		})
	}
}

func TestConfirmPayment_RefusesIncompleteOrUnverifiedCallback(t *testing.T) {
	st := onlinePendingStore()
	svc := New(st).WithPayments(&fakePayments{verdict: matchingVerdict()}).WithPaymentFlags(payments.Flags{})
	in := confirmInput()
	in.RazorpaySignature = ""
	if _, err := svc.ConfirmPayment(context.Background(), in); !errors.Is(err, ErrPaymentCallbackIncomplete) {
		t.Fatalf("missing signature: err = %v", err)
	}

	v := matchingVerdict()
	v.Verified = false
	svc = New(onlinePendingStore()).WithPayments(&fakePayments{verdict: v}).WithPaymentFlags(payments.Flags{})
	if _, err := svc.ConfirmPayment(context.Background(), confirmInput()); !errors.Is(err, ErrPaymentNotVerified) {
		t.Fatalf("unverified: err = %v", err)
	}
}

func TestConfirmPayment_WalletUnavailableWhenFlagOff(t *testing.T) {
	st := onlinePendingStore()
	st.details.PaymentMethod = "WALLET"
	svc := New(st).WithPayments(&fakePayments{}).WithPaymentFlags(payments.Flags{})
	if _, err := svc.ConfirmPayment(context.Background(), confirmInput()); !errors.Is(err, payments.ErrPaymentMethodUnavailable) {
		t.Fatalf("err = %v, want ErrPaymentMethodUnavailable", err)
	}
	if st.walletPaidCalls != 0 {
		t.Fatal("wallet marked paid with the flag off")
	}
}

func TestPlaceOrder_PaymentMethodGate(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		flags      payments.Flags
		err        error
		store      string
		instrument string
	}{
		{"cod refused with the flag off", "COD", payments.Flags{}, payments.ErrPaymentMethodUnavailable, "", ""},
		{"wallet refused with the flag off", "WALLET", payments.Flags{}, payments.ErrPaymentMethodUnavailable, "", ""},
		{"missing method never defaults to cod", "", payments.Flags{}, payments.ErrPaymentMethodInvalid, "", ""},
		{"missing method refused even with cod on", "", payments.Flags{CODEnabled: true}, payments.ErrPaymentMethodInvalid, "", ""},
		{"legacy ONLINE refused", "ONLINE", payments.Flags{}, payments.ErrPaymentMethodInvalid, "", ""},
		{"upi places an online order", "upi", payments.Flags{}, nil, "ONLINE", "upi"},
		{"card places an online order", "card", payments.Flags{}, nil, "ONLINE", "card"},
		{"cod with the flag on", "cod", payments.Flags{CODEnabled: true}, nil, "COD", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := onlinePendingStore()
			svc := New(st).WithPaymentFlags(tc.flags)
			_, err := svc.PlaceOrder(context.Background(), sUserID, postgres.PlaceOrderInput{AddressID: uuid.New(), PaymentMethod: tc.method}, "k")
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err = %v, want %v", err, tc.err)
				}
				if len(st.placed) != 0 {
					t.Fatalf("refused order reached the store: %+v", st.placed)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if len(st.placed) != 1 || st.placed[0].PaymentMethod != tc.store || st.placed[0].PaymentInstrument != tc.instrument {
				t.Fatalf("store got %+v, want method=%s instrument=%s", st.placed, tc.store, tc.instrument)
			}
		})
	}
}

func TestCreatePaymentIntent_PaymentMethodGate(t *testing.T) {
	for _, method := range []string{"COD", "cod", "WALLET"} {
		st := onlinePendingStore()
		pm := &fakePayments{}
		svc := New(st).WithPayments(pm).WithPaymentFlags(payments.Flags{})
		if _, err := svc.CreatePaymentIntent(context.Background(), sUserID, sOrderID, method, "k"); !errors.Is(err, payments.ErrPaymentMethodUnavailable) {
			t.Fatalf("%s: err = %v, want ErrPaymentMethodUnavailable", method, err)
		}
		if len(st.intents) != 0 || len(pm.created) != 0 {
			t.Fatalf("%s: refused intent reached the store or payments", method)
		}
	}

	// COD with the flag on goes to the store's state-guarded COD path and
	// never opens a payments intent.
	st := onlinePendingStore()
	pm := &fakePayments{}
	svc := New(st).WithPayments(pm).WithPaymentFlags(payments.Flags{CODEnabled: true})
	if _, err := svc.CreatePaymentIntent(context.Background(), sUserID, sOrderID, "cod", "k"); err != nil {
		t.Fatalf("cod with flag on: %v", err)
	}
	if len(st.intents) != 1 || st.intents[0][0] != "COD" || len(pm.created) != 0 {
		t.Fatalf("cod with flag on: store=%v payments=%v", st.intents, pm.created)
	}
}

func TestCreatePaymentIntent_OnlineUsesServerAmountAndClientInstrument(t *testing.T) {
	st := onlinePendingStore()
	pm := &fakePayments{}
	svc := New(st).WithPayments(pm).WithPaymentFlags(payments.Flags{})
	if _, err := svc.CreatePaymentIntent(context.Background(), sUserID, sOrderID, "card", "k"); err != nil {
		t.Fatalf("CreatePaymentIntent: %v", err)
	}
	if len(pm.created) != 1 {
		t.Fatalf("payments intents = %d", len(pm.created))
	}
	in := pm.created[0]
	if in.AmountMinor != 25050 || in.PayerID != sUserID || in.PayeeID != sOwnerID || in.OrderID != sOrderID || in.Method != "card" {
		t.Fatalf("intent input = %+v", in)
	}
	if len(st.intents) != 1 || st.intents[0] != [2]string{"ONLINE", "card"} {
		t.Fatalf("store intent = %v", st.intents)
	}
	if len(st.attached) != 1 {
		t.Fatalf("provider reference not attached")
	}

	// No method in the body falls back to the instrument chosen at checkout.
	st = onlinePendingStore()
	pm = &fakePayments{}
	svc = New(st).WithPayments(pm).WithPaymentFlags(payments.Flags{})
	if _, err := svc.CreatePaymentIntent(context.Background(), sUserID, sOrderID, "", "k"); err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if len(pm.created) != 1 || pm.created[0].Method != "upi" {
		t.Fatalf("fallback instrument = %+v", pm.created)
	}
}

func TestAdminRefundOrder_RequestsRefundWithIntegerAmountAndDeterministicKey(t *testing.T) {
	refundID := uuid.New()
	st := onlinePendingStore()
	st.plan = &postgres.RefundPlan{
		RefundID: refundID, OrderID: sOrderID, PaymentMethod: "ONLINE", IntentID: sIntentID.String(),
		AmountMinor: 12345, Status: "PENDING", OrderStatus: "REFUND_PENDING",
	}
	pm := &fakePayments{}
	svc := New(st).WithPayments(pm).WithPaymentFlags(payments.Flags{})
	body, err := svc.AdminRefundOrder(context.Background(), uuid.New(), sOrderID, "cold food", 123.45, "admin-key")
	if err != nil {
		t.Fatalf("AdminRefundOrder: %v", err)
	}
	if len(pm.refunds) != 1 {
		t.Fatalf("refund calls = %d", len(pm.refunds))
	}
	got := pm.refunds[0]
	wantKey := "food_refund:" + sOrderID.String() + ":" + refundID.String()
	if got.intent != sIntentID || got.amount != 12345 || got.key != wantKey || got.reason != "cold food" {
		t.Fatalf("refund call = %+v, want key %s", got, wantKey)
	}
	if len(st.submitted) != 1 || st.submitted[0] != refundID {
		t.Fatalf("refund not marked submitted: %v", st.submitted)
	}
	if body["order_status"] != "REFUND_PENDING" {
		t.Fatalf("body = %v", body)
	}

	// A refund the provider already settled is not requested again.
	st.plan.Status = "PROCESSED"
	pm.refunds = nil
	if _, err := svc.AdminRefundOrder(context.Background(), uuid.New(), sOrderID, "cold food", 0, "admin-key"); err != nil {
		t.Fatal(err)
	}
	if len(pm.refunds) != 0 {
		t.Fatal("a processed refund was re-requested")
	}
}
