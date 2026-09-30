package events

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/google/uuid"
)

var (
	ctOrder      = uuid.MustParse("00000000-0000-4000-8000-0000000c0240")
	ctBuyer      = uuid.MustParse("00000000-0000-4000-8000-0000000c0001")
	ctSellerUser = uuid.MustParse("00000000-0000-4000-8000-0000000c0002")
	ctSellerRow  = uuid.MustParse("00000000-0000-4000-8000-0000000c0010")
)

func fullPayload() commerceEventPayload {
	return commerceEventPayload{
		OrderID: ctOrder.String(), OrderNumber: "ORD-2026-010005", UserID: ctBuyer.String(),
		SellerID: ctSellerRow.String(), SellerUserID: ctSellerUser.String(), AmountMinor: 134800,
	}
}

type wantNotice struct {
	typ, recipient, deepLink string
}

func TestPlanCommerceNotices(t *testing.T) {
	buyerLink := "/shop/orders/" + ctOrder.String()
	sellerLink := "/shop/sell/orders/" + ctOrder.String()
	cases := []struct {
		name    string
		event   string
		mutate  func(*commerceEventPayload)
		want    []wantNotice
		missing []string
	}{
		{"paid: buyer confirmed + seller new order", commerceOrderPaid, nil, []wantNotice{
			{service.OrderTypeConfirmed, ctBuyer.String(), buyerLink},
			{service.OrderTypeSellerNewOrder, ctSellerUser.String(), sellerLink},
		}, nil},
		{"shipped", commerceOrderShipped, nil, []wantNotice{{service.OrderTypeShipped, ctBuyer.String(), buyerLink}}, nil},
		{"delivered", commerceOrderDelivered, nil, []wantNotice{{service.OrderTypeDelivered, ctBuyer.String(), buyerLink}}, nil},
		{"cancelled", commerceOrderCancelled, nil, []wantNotice{{service.OrderTypeCancelled, ctBuyer.String(), buyerLink}}, nil},
		{"refunded", commerceOrderRefunded, nil, []wantNotice{{service.OrderTypeRefunded, ctBuyer.String(), buyerLink}}, nil},
		{"payment failed", commerceOrderPaymentFailed, nil, []wantNotice{{service.OrderTypePaymentFailed, ctBuyer.String(), buyerLink}}, nil},
		// "Order placed" before payment is gone.
		{"created notifies nobody", commerceOrderCreated, nil, nil, nil},
		{"invoice is not an order notice", commerceInvoiceIssued, nil, nil, nil},
		// The legacy seller event's seller_id is a sellers.id, never a user.
		{"legacy seller.new_order notifies nobody", commerceSellerNewOrder, nil, nil, nil},
		{"paid without the seller's user: buyer only, counted", commerceOrderPaid,
			func(p *commerceEventPayload) { p.SellerUserID = "" },
			[]wantNotice{{service.OrderTypeConfirmed, ctBuyer.String(), buyerLink}}, []string{"seller_user_id"}},
		{"never the seller row id as a recipient", commerceOrderPaid,
			func(p *commerceEventPayload) { p.SellerUserID = ""; p.UserID = "" },
			nil, []string{"user_id", "seller_user_id"}},
		{"delivered without user_id: counted, nothing sent", commerceOrderDelivered,
			func(p *commerceEventPayload) { p.UserID = "" }, nil, []string{"user_id"}},
		{"no order id", commerceOrderPaid,
			func(p *commerceEventPayload) { p.OrderID = "not-a-uuid" }, nil, []string{"order_id"}},
		{"unknown event", "commerce.order.teleported", nil, nil, nil},
	}
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fullPayload()
			if c.mutate != nil {
				c.mutate(&p)
			}
			plan := planCommerceNotices(c.event, p, now)
			var got []wantNotice
			for _, n := range plan.notices {
				got = append(got, wantNotice{n.Type, n.RecipientID.String(), n.DeepLink})
				if n.OrderID != ctOrder || n.OrderNumber != p.OrderNumber || !n.CreatedAt.Equal(now) {
					t.Errorf("%s: notice carries order %s #%s at %s", n.Type, n.OrderID, n.OrderNumber, n.CreatedAt)
				}
				if n.RecipientID == ctSellerRow {
					t.Errorf("%s addressed to the sellers.id row", n.Type)
				}
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("notices = %v, want %v", got, c.want)
			}
			if !reflect.DeepEqual(plan.missing, c.missing) {
				t.Errorf("missing = %v, want %v", plan.missing, c.missing)
			}
		})
	}
}

// Every type the consumer can emit is registered, push-eligible, and is one
// of the declared order types — and every declared order type is emitted.
func TestEveryCommerceNoticeTypeIsRegistered(t *testing.T) {
	emitted := map[string]bool{}
	for _, ev := range []string{commerceOrderCreated, commerceOrderPaid, commerceOrderShipped, commerceOrderDelivered,
		commerceOrderCancelled, commerceOrderRefunded, commerceOrderPaymentFailed, commerceInvoiceIssued, commerceSellerNewOrder} {
		for _, n := range planCommerceNotices(ev, fullPayload(), time.Now()).notices {
			emitted[n.Type] = true
		}
	}
	var got []string
	for ty := range emitted {
		got = append(got, ty)
		tpl, ok := service.Templates[ty]
		if !ok {
			t.Errorf("%s is emitted but not in service.Templates", ty)
			continue
		}
		if !tpl.PushEligible || tpl.EventType != ty {
			t.Errorf("%s template: %+v", ty, tpl)
		}
	}
	want := append([]string(nil), service.OrderNotificationTypes...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("emitted types %v, registered order types %v", got, want)
	}
}

type fakeOrderNotifier struct {
	got []service.OrderNotification
	err error
}

func (f *fakeOrderNotifier) CreateOrderNotification(_ context.Context, n service.OrderNotification) error {
	f.got = append(f.got, n)
	return f.err
}

// The wire shape commerce-service's outbox writes for a paid order.
func TestHandleCommerceEventDeliversFromTheRealPaidPayload(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"order_id": ctOrder, "user_id": ctBuyer, "amount_minor": 134800, "currency": "INR",
		"intent_id": "pi_1", "order_number": "ORD-2026-010005", "buyer_email": "",
		"seller_id": ctSellerRow, "seller_user_id": ctSellerUser,
	})
	f := &fakeOrderNotifier{}
	c := &Consumer{orderNotify: f}
	if err := c.handleCommerceEvent(context.Background(), commerceOrderPaid, raw); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 2 || f.got[0].Type != service.OrderTypeConfirmed || f.got[1].Type != service.OrderTypeSellerNewOrder {
		t.Fatalf("delivered %+v", f.got)
	}
	if f.got[1].RecipientID != ctSellerUser || f.got[1].ActorID != ctBuyer {
		t.Fatalf("seller notice %+v", f.got[1])
	}

	// A delivery failure is logged, not fatal: the offset moves on.
	f2 := &fakeOrderNotifier{err: errors.New("scylla down")}
	if err := (&Consumer{orderNotify: f2}).handleCommerceEvent(context.Background(), commerceOrderPaid, raw); err != nil {
		t.Fatalf("a failed notice must not fail the event: %v", err)
	}
}

func TestCommerceAmountPrefersPaise(t *testing.T) {
	if got := commerceAmount(commerceEventPayload{AmountMinor: 134805, Amount: 1}); got != "1348.05" {
		t.Fatalf("got %q", got)
	}
	if got := commerceAmount(commerceEventPayload{Amount: 12.5}); got != "12.50" {
		t.Fatalf("got %q", got)
	}
}
