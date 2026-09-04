package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/atpost/commerce-service/internal/payments"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// fakeOrderStore is an in-memory orderPaymentStore whose MarkOrderPaid /
// TransitionPaymentStatus apply the same guard table the SQL does, under
// a mutex, so the concurrency tests below exercise the real convergence
// contract (exactly one Applied=true).
type fakeOrderStore struct {
	mu     sync.Mutex
	orders map[uuid.UUID]*postgres.Order
	items  map[uuid.UUID][]*postgres.OrderItem

	deducted  int
	released  int
	enqueued  []string
	paidCalls int
}

func newFakeStore() *fakeOrderStore {
	return &fakeOrderStore{orders: map[uuid.UUID]*postgres.Order{}, items: map[uuid.UUID][]*postgres.OrderItem{}}
}

func (f *fakeOrderStore) add(o *postgres.Order) *postgres.Order {
	if o.ID == uuid.Nil {
		o.ID = uuid.New()
	}
	f.orders[o.ID] = o
	f.items[o.ID] = []*postgres.OrderItem{{ID: uuid.New(), OrderID: o.ID, VariantID: uuid.New(), Quantity: 2}}
	return o
}

func (f *fakeOrderStore) GetOrderByID(_ context.Context, id uuid.UUID) (*postgres.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok {
		return &postgres.Order{}, pgx.ErrNoRows // mirrors the real store
	}
	cp := *o
	return &cp, nil
}
func (f *fakeOrderStore) GetOrderItems(_ context.Context, id uuid.UUID) ([]*postgres.OrderItem, error) {
	return f.items[id], nil
}
func (f *fakeOrderStore) MarkOrderPaid(_ context.Context, id uuid.UUID, paymentID, gateway string, _ *uuid.UUID, _ string) (postgres.PaidTransition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paidCalls++
	o, ok := f.orders[id]
	if !ok {
		return postgres.PaidTransition{}, postgres.ErrOrderNotFound
	}
	t := postgres.PaidTransition{FromStatus: o.Status, FromPaymentStatus: o.PaymentStatus, Status: o.Status, PaymentStatus: o.PaymentStatus}
	if !postgres.OrderPayable(o.Status, o.PaymentStatus) {
		return t, nil
	}
	o.PaymentStatus = "paid"
	o.Status = postgres.OrderStatusAfterPaid(o.Status)
	o.PaymentID, o.PaymentGateway = &paymentID, &gateway
	t.Applied, t.Status, t.PaymentStatus = true, o.Status, o.PaymentStatus
	return t, nil
}
func (f *fakeOrderStore) TransitionPaymentStatus(_ context.Context, id uuid.UUID, to, _, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok || !postgres.PaymentStatusTransitionAllowed(o.PaymentStatus, to) {
		return false, nil
	}
	o.PaymentStatus = to
	return true, nil
}
func (f *fakeOrderStore) DeductStock(_ context.Context, _ uuid.UUID, _ int, _ uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deducted++
	return nil
}
func (f *fakeOrderStore) ReleaseReservation(_ context.Context, _, _ uuid.UUID, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released++
	return nil
}
func (f *fakeOrderStore) EnqueueJobPool(_ context.Context, kind string, _ []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, kind)
	return nil
}

// fakePayments scripts payments-service's verify answer.
type fakePayments struct {
	result *payments.VerifyResult
	err    error
	calls  int
}

func (p *fakePayments) VerifyIntent(_ context.Context, _ uuid.UUID, _, _, _ string, _ int64) (*payments.VerifyResult, error) {
	p.calls++
	return p.result, p.err
}
func (p *fakePayments) FindOrderIntent(context.Context, uuid.UUID, uuid.UUID) (*payments.PaymentIntent, error) {
	return nil, nil
}
func (p *fakePayments) InitiateRefund(context.Context, uuid.UUID, uuid.UUID, int64, string) (*payments.PaymentIntent, error) {
	return nil, nil
}

func newPaymentSvc(st *fakeOrderStore, pay paymentsClient) *Service {
	s := &Service{}
	s.withOrderPaymentStore(st)
	if pay != nil {
		s.withPaymentsClient(pay)
	}
	return s
}

func prepaidOrder(customer uuid.UUID) *postgres.Order {
	status, paymentStatus := checkoutInitialState(false)
	pm := "upi"
	return &postgres.Order{CustomerUserID: customer, FinalAmount: 900, PaymentMethod: &pm, Status: status, PaymentStatus: paymentStatus, OrderNumber: "ORD-T-1"}
}

// TestCheckoutInitialState_IsPayable is the regression pin for defect #1:
// the state Checkout writes must be accepted by the paid transition, for
// both prepaid and COD orders.
func TestCheckoutInitialState_IsPayable(t *testing.T) {
	for _, cod := range []bool{false, true} {
		status, paymentStatus := checkoutInitialState(cod)
		if !postgres.OrderPayable(status, paymentStatus) {
			t.Fatalf("cod=%v: checkout state (%s,%s) is not payable — ConfirmPayment would always 409", cod, status, paymentStatus)
		}
		if got := postgres.OrderStatusAfterPaid(status); got != "confirmed" {
			t.Fatalf("cod=%v: status after paid = %q, want confirmed", cod, got)
		}
	}
	if s, _ := checkoutInitialState(false); s != "payment_pending" {
		t.Fatalf("prepaid checkout status = %q", s)
	}
	if s, p := checkoutInitialState(true); s != "confirmed" || p != "pending" {
		t.Fatalf("cod checkout state = %s/%s", s, p)
	}
}

func TestConfirmPayment(t *testing.T) {
	customer, stranger := uuid.New(), uuid.New()
	intentID := uuid.New()
	verified := func(orderID uuid.UUID) *payments.VerifyResult {
		return &payments.VerifyResult{Verified: true, IntentID: intentID, Status: "succeeded", AmountMinor: 90000, ReferenceType: "order", ReferenceID: orderID, PayerID: customer}
	}
	input := ConfirmPaymentInput{PaymentIntentID: intentID, RazorpayOrderID: "order_1", RazorpayPaymentID: "pay_1", RazorpaySignature: "sig", AmountMinor: 90000}

	t.Run("happy path: checkout → paid → confirmed with side effects once", func(t *testing.T) {
		st := newFakeStore()
		o := st.add(prepaidOrder(customer))
		pay := &fakePayments{result: verified(o.ID)}
		svc := newPaymentSvc(st, pay)

		if err := svc.ConfirmPayment(context.Background(), o.ID, customer, input); err != nil {
			t.Fatalf("confirm: %v", err)
		}
		got, _ := st.GetOrderByID(context.Background(), o.ID)
		if got.Status != "confirmed" || got.PaymentStatus != "paid" || got.PaymentID == nil || *got.PaymentID != "pay_1" {
			t.Fatalf("order after confirm = %s/%s pid=%v", got.Status, got.PaymentStatus, got.PaymentID)
		}
		if st.deducted != 1 || len(st.enqueued) != 1 || st.enqueued[0] != "fulfill_paid_order" {
			t.Fatalf("side effects: deducted=%d enqueued=%v", st.deducted, st.enqueued)
		}
		// Second confirm is an idempotent no-op: no verify, no side effects.
		if err := svc.ConfirmPayment(context.Background(), o.ID, customer, input); err != nil {
			t.Fatalf("second confirm: %v", err)
		}
		if pay.calls != 1 || st.deducted != 1 || len(st.enqueued) != 1 {
			t.Fatalf("second confirm re-ran work: verify=%d deducted=%d enqueued=%v", pay.calls, st.deducted, st.enqueued)
		}
	})

	t.Run("wrong owner", func(t *testing.T) {
		st := newFakeStore()
		o := st.add(prepaidOrder(customer))
		svc := newPaymentSvc(st, &fakePayments{result: verified(o.ID)})
		if err := svc.ConfirmPayment(context.Background(), o.ID, stranger, input); !errors.Is(err, ErrNotOrderOwner) {
			t.Fatalf("err = %v, want ErrNotOrderOwner", err)
		}
		if got, _ := st.GetOrderByID(context.Background(), o.ID); got.PaymentStatus != "pending" {
			t.Fatal("order mutated by a stranger")
		}
	})

	t.Run("already paid returns nil without verifying", func(t *testing.T) {
		st := newFakeStore()
		o := prepaidOrder(customer)
		o.Status, o.PaymentStatus = "confirmed", "paid"
		st.add(o)
		pay := &fakePayments{err: errors.New("must not be called")}
		svc := newPaymentSvc(st, pay)
		if err := svc.ConfirmPayment(context.Background(), o.ID, customer, input); err != nil {
			t.Fatalf("err = %v", err)
		}
		if pay.calls != 0 || st.paidCalls != 0 {
			t.Fatalf("already-paid order hit verify=%d paid=%d", pay.calls, st.paidCalls)
		}
	})

	t.Run("cancelled order is not payable", func(t *testing.T) {
		st := newFakeStore()
		o := prepaidOrder(customer)
		o.Status = "cancelled"
		st.add(o)
		svc := newPaymentSvc(st, &fakePayments{result: verified(o.ID)})
		if err := svc.ConfirmPayment(context.Background(), o.ID, customer, input); !errors.Is(err, ErrOrderNotPaymentPending) {
			t.Fatalf("err = %v, want ErrOrderNotPaymentPending", err)
		}
	})

	t.Run("order not found", func(t *testing.T) {
		svc := newPaymentSvc(newFakeStore(), &fakePayments{})
		if err := svc.ConfirmPayment(context.Background(), uuid.New(), customer, input); !errors.Is(err, ErrOrderNotFound) {
			t.Fatalf("err = %v, want ErrOrderNotFound", err)
		}
	})

	t.Run("verify failure never marks paid", func(t *testing.T) {
		st := newFakeStore()
		o := st.add(prepaidOrder(customer))
		svc := newPaymentSvc(st, &fakePayments{err: errors.New("signature mismatch")})
		if err := svc.ConfirmPayment(context.Background(), o.ID, customer, input); !errors.Is(err, ErrPaymentVerifyFailed) {
			t.Fatalf("err = %v, want ErrPaymentVerifyFailed", err)
		}
		if st.paidCalls != 0 {
			t.Fatal("MarkOrderPaid called after a failed verify")
		}
	})

	t.Run("verified intent for a different order is refused", func(t *testing.T) {
		st := newFakeStore()
		o := st.add(prepaidOrder(customer))
		svc := newPaymentSvc(st, &fakePayments{result: verified(uuid.New())})
		if err := svc.ConfirmPayment(context.Background(), o.ID, customer, input); !errors.Is(err, ErrPaymentVerifyFailed) {
			t.Fatalf("err = %v, want ErrPaymentVerifyFailed", err)
		}
	})

	t.Run("verified intent paid by another user is refused", func(t *testing.T) {
		st := newFakeStore()
		o := st.add(prepaidOrder(customer))
		res := verified(o.ID)
		res.PayerID = stranger
		svc := newPaymentSvc(st, &fakePayments{result: res})
		if err := svc.ConfirmPayment(context.Background(), o.ID, customer, input); !errors.Is(err, ErrPaymentVerifyFailed) {
			t.Fatalf("err = %v, want ErrPaymentVerifyFailed", err)
		}
	})

	t.Run("amount mismatch (client) and (intent)", func(t *testing.T) {
		st := newFakeStore()
		o := st.add(prepaidOrder(customer))
		svc := newPaymentSvc(st, &fakePayments{result: verified(o.ID)})
		bad := input
		bad.AmountMinor = 1
		if err := svc.ConfirmPayment(context.Background(), o.ID, customer, bad); !errors.Is(err, ErrPaymentAmountMismatch) {
			t.Fatalf("client amount: err = %v", err)
		}
		res := verified(o.ID)
		res.AmountMinor = 1
		svc = newPaymentSvc(st, &fakePayments{result: res})
		if err := svc.ConfirmPayment(context.Background(), o.ID, customer, input); !errors.Is(err, ErrPaymentAmountMismatch) {
			t.Fatalf("intent amount: err = %v", err)
		}
	})

	t.Run("stub gateway refused unless allowed, verified when allowed", func(t *testing.T) {
		st := newFakeStore()
		o := st.add(prepaidOrder(customer))
		pay := &fakePayments{result: verified(o.ID)}
		svc := newPaymentSvc(st, pay)
		stub := input
		stub.Gateway = "stub"
		t.Setenv("PAYMENTS_ALLOW_STUB", "")
		if err := svc.ConfirmPayment(context.Background(), o.ID, customer, stub); !errors.Is(err, ErrStubGatewayInProd) {
			t.Fatalf("err = %v, want ErrStubGatewayInProd", err)
		}
		svc.WithAllowStubGateway(true)
		if err := svc.ConfirmPayment(context.Background(), o.ID, customer, stub); err != nil {
			t.Fatalf("stub allowed: %v", err)
		}
		if pay.calls != 1 {
			t.Fatalf("stub path must still verify the intent with payments-service, calls=%d", pay.calls)
		}
		if got, _ := st.GetOrderByID(context.Background(), o.ID); got.PaymentGateway == nil || *got.PaymentGateway != "stub" {
			t.Fatalf("gateway not recorded: %v", got.PaymentGateway)
		}
	})

	t.Run("payments client missing", func(t *testing.T) {
		st := newFakeStore()
		o := st.add(prepaidOrder(customer))
		svc := newPaymentSvc(st, nil)
		if err := svc.ConfirmPayment(context.Background(), o.ID, customer, input); !errors.Is(err, ErrPaymentsClientMissing) {
			t.Fatalf("err = %v, want ErrPaymentsClientMissing", err)
		}
	})
}

// TestConcurrentWebhookAndConfirm: the payments consumer (webhook) and the
// customer's confirm race on one order. Both must return nil and the
// side effects must run exactly once.
func TestConcurrentWebhookAndConfirm(t *testing.T) {
	customer := uuid.New()
	for round := 0; round < 25; round++ {
		st := newFakeStore()
		o := st.add(prepaidOrder(customer))
		svc := newPaymentSvc(st, &fakePayments{result: &payments.VerifyResult{Verified: true, ReferenceID: o.ID, PayerID: customer, AmountMinor: 90000}})
		in := ConfirmPaymentInput{PaymentIntentID: uuid.New(), RazorpayOrderID: "o", RazorpayPaymentID: "pay_confirm", RazorpaySignature: "s"}

		var wg sync.WaitGroup
		errs := make([]error, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if i%2 == 0 {
					errs[i] = svc.ApplyVerifiedPaymentEvent(context.Background(), o.ID, "pay_webhook")
				} else {
					errs[i] = svc.ConfirmPayment(context.Background(), o.ID, customer, in)
				}
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d caller %d: %v", round, i, err)
			}
		}
		got, _ := st.GetOrderByID(context.Background(), o.ID)
		if got.Status != "confirmed" || got.PaymentStatus != "paid" {
			t.Fatalf("round %d: order = %s/%s", round, got.Status, got.PaymentStatus)
		}
		if st.deducted != 1 || len(st.enqueued) != 1 {
			t.Fatalf("round %d: side effects ran deducted=%d enqueued=%d, want 1/1", round, st.deducted, len(st.enqueued))
		}
	}
}

func TestApplyVerifiedPaymentEvent_Outcomes(t *testing.T) {
	customer := uuid.New()
	t.Run("not found", func(t *testing.T) {
		svc := newPaymentSvc(newFakeStore(), nil)
		if err := svc.ApplyVerifiedPaymentEvent(context.Background(), uuid.New(), "p"); !errors.Is(err, ErrOrderNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("cancelled order → ErrOrderNotPayable", func(t *testing.T) {
		st := newFakeStore()
		o := prepaidOrder(customer)
		o.Status = "cancelled"
		st.add(o)
		svc := newPaymentSvc(st, nil)
		if err := svc.ApplyVerifiedPaymentEvent(context.Background(), o.ID, "p"); !errors.Is(err, ErrOrderNotPayable) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("COD confirmed order: paid, status stays confirmed", func(t *testing.T) {
		st := newFakeStore()
		o := prepaidOrder(customer)
		o.Status, o.PaymentStatus = checkoutInitialState(true)
		st.add(o)
		svc := newPaymentSvc(st, nil)
		if err := svc.ApplyVerifiedPaymentEvent(context.Background(), o.ID, "p"); err != nil {
			t.Fatal(err)
		}
		got, _ := st.GetOrderByID(context.Background(), o.ID)
		if got.Status != "confirmed" || got.PaymentStatus != "paid" {
			t.Fatalf("order = %s/%s", got.Status, got.PaymentStatus)
		}
	})
}

func TestMarkPaymentFailed(t *testing.T) {
	customer := uuid.New()
	st := newFakeStore()
	o := st.add(prepaidOrder(customer))
	svc := newPaymentSvc(st, nil)

	if err := svc.MarkPaymentFailed(context.Background(), o.ID, "pay_f"); err != nil {
		t.Fatal(err)
	}
	if st.released != 1 {
		t.Fatalf("released = %d, want 1", st.released)
	}
	// Replay is a no-op.
	if err := svc.MarkPaymentFailed(context.Background(), o.ID, "pay_f"); err != nil || st.released != 1 {
		t.Fatalf("replay: err=%v released=%d", err, st.released)
	}
	// A retry can still pay the order (failed → paid)...
	if err := svc.ApplyVerifiedPaymentEvent(context.Background(), o.ID, "pay_retry"); err != nil {
		t.Fatal(err)
	}
	// ...and a late failed after capture is refused without releasing stock.
	if err := svc.MarkPaymentFailed(context.Background(), o.ID, "pay_late"); err != nil || st.released != 1 {
		t.Fatalf("late failed: err=%v released=%d", err, st.released)
	}
	if got, _ := st.GetOrderByID(context.Background(), o.ID); got.PaymentStatus != "paid" {
		t.Fatalf("late failed clobbered paid: %s", got.PaymentStatus)
	}
}
