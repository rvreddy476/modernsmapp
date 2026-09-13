package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type statusFakeStore struct {
	Store
	state postgres.CustomerPaymentState
	err   error
}

func (f *statusFakeStore) CustomerPaymentStatus(context.Context, uuid.UUID, uuid.UUID) (*postgres.CustomerPaymentState, error) {
	if f.err != nil {
		return nil, f.err
	}
	s := f.state
	return &s, nil
}

// sessionPayments is fakePayments that attaches a client session.
type sessionPayments struct {
	fakePayments
	session map[string]string
}

func (f *sessionPayments) CreateIntent(ctx context.Context, in payments.CreateIntentInput) (*payments.Intent, error) {
	i, err := f.fakePayments.CreateIntent(ctx, in)
	if err != nil {
		return nil, err
	}
	i.PayerID, i.PayeeID = in.PayerID, in.PayeeID
	i.ClientSession = f.session
	return i, nil
}

func TestCreatePaymentIntent_RelaysOnlyThePublicClientSession(t *testing.T) {
	st := onlinePendingStore()
	pm := &sessionPayments{session: map[string]string{
		"provider": "razorpay", "order_id": "order_rzp", "key_id": "rzp_test_pub", "key_secret": "sk_SECRET",
	}}
	out, err := New(st).WithPayments(pm).WithPaymentFlags(payments.Flags{}).CreatePaymentIntent(context.Background(), sUserID, sOrderID, "upi", "k")
	if err != nil {
		t.Fatalf("CreatePaymentIntent: %v", err)
	}
	session, ok := out["client_session"].(*payments.ClientSession)
	if !ok || *session != (payments.ClientSession{Provider: "razorpay", OrderID: "order_rzp", KeyID: "rzp_test_pub"}) {
		t.Fatalf("client_session = %#v", out["client_session"])
	}
	raw, _ := json.Marshal(out)
	for _, forbidden := range []string{"sk_SECRET", "key_secret", "payer_id", "payee_id", sOwnerID.String()} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("intent response carries %q: %s", forbidden, raw)
		}
	}

	// No session from payments: no field at all.
	pm = &sessionPayments{}
	out, err = New(onlinePendingStore()).WithPayments(pm).WithPaymentFlags(payments.Flags{}).CreatePaymentIntent(context.Background(), sUserID, sOrderID, "upi", "k")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := out["client_session"]; present {
		t.Fatalf("client_session present without a session: %v", out)
	}
}

func TestGetOrderPaymentStatus(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.FixedZone("IST", 19800))
	base := postgres.CustomerPaymentState{OrderID: sOrderID, PaymentMethod: "ONLINE", AmountMinor: 25050, Currency: "INR", UpdatedAt: at}

	paid := base
	paid.OrderStatus, paid.PaymentStatus, paid.CaptureApplied = "CONFIRMED", "CAPTURED", true
	got, err := New(&statusFakeStore{state: paid}).GetOrderPaymentStatus(context.Background(), sUserID, sOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != payments.CustomerStatusPaid || got.RefundStatus != nil || got.AmountMinor != 25050 || got.Currency != "INR" ||
		got.OrderID != sOrderID || !got.UpdatedAt.Equal(at) || got.UpdatedAt.Location() != time.UTC {
		t.Fatalf("paid = %+v", got)
	}

	notApplied := paid
	notApplied.CaptureApplied = false
	got, err = New(&statusFakeStore{state: notApplied}).GetOrderPaymentStatus(context.Background(), sUserID, sOrderID)
	if err != nil || got.Status != payments.CustomerStatusConfirming {
		t.Fatalf("captured without the event = %+v, %v", got, err)
	}

	refunded := paid
	refunded.OrderStatus, refunded.PaymentStatus = "REFUNDED", "REFUNDED"
	got, err = New(&statusFakeStore{state: refunded}).GetOrderPaymentStatus(context.Background(), sUserID, sOrderID)
	if err != nil || got.Status != payments.CustomerStatusPaid || got.RefundStatus == nil || *got.RefundStatus != payments.RefundStatusRefunded {
		t.Fatalf("refunded = %+v, %v", got, err)
	}

	cod := base
	cod.OrderStatus, cod.PaymentStatus, cod.PaymentMethod = "CONFIRMED", "NOT_REQUIRED", "COD"
	if _, err := New(&statusFakeStore{state: cod}).GetOrderPaymentStatus(context.Background(), sUserID, sOrderID); !errors.Is(err, payments.ErrPaymentNotOnline) {
		t.Fatalf("cod err = %v", err)
	}

	if _, err := New(&statusFakeStore{err: pgx.ErrNoRows}).GetOrderPaymentStatus(context.Background(), sUserID, sOrderID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing err = %v", err)
	}
}
