package service

import (
	"context"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestCustomerNoticeAudience(t *testing.T) {
	for _, typ := range []string{"food_order_status", "order_shipped", "ride.arrived", "doorstep.booking.confirmed"} {
		if !customerNoticeAllowed(PartnerNotice{App: postgres.AppMomentum, Type: typ}) {
			t.Fatal("customer type rejected", typ)
		}
		if customerNoticeAllowed(PartnerNotice{App: postgres.AppPartner, Type: typ}) {
			t.Fatal("partner audience admitted", typ)
		}
	}
	for _, typ := range []string{"seller_new_order", "food_order_new", "food_delivery_offer", "captain.offer", "doorstep.pro.offer.new", "ride.unknown"} {
		if customerNoticeAllowed(PartnerNotice{App: postgres.AppMomentum, Type: typ}) {
			t.Fatal("noncustomer type admitted", typ)
		}
	}
}

func TestCustomerQueueDBRetriesAndAudience(t *testing.T) {
	s, _, sender := partnerQueueRig(t)
	ctx := context.Background()
	user := uuid.New()
	if _, err := s.pgStore.RegisterDevice(ctx, user, "android", "synthetic-customer-token", postgres.AppMomentum); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pgStore.RegisterDevice(ctx, user, "android", "synthetic-partner-token", postgres.AppPartner); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"food_order_status", "order_shipped", "ride.arrived", "doorstep.booking.confirmed"} {
		n := PartnerNotice{ID: uuid.New(), UserID: user, App: postgres.AppMomentum, Type: typ, EntityID: uuid.New(), CreatedAt: time.Now(), Data: map[string]string{"type": typ}}
		if err := s.queueCustomerNotice(ctx, n); err != nil {
			t.Fatal(err)
		}
		if err := s.queueCustomerNotice(ctx, n); err != nil {
			t.Fatal(err)
		}
		job, err := s.pgStore.ClaimPartnerNotification(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sender.fail = true
		if status, _ := s.deliverPartnerNotification(ctx, job); status != "pending" {
			t.Fatal("failed send lost", status)
		}
		sender.fail = false
		before := sender.calls
		if status, code := s.deliverPartnerNotification(ctx, job); status != "delivered" {
			t.Fatal("recovery", status, code)
		}
		if sender.calls != before+1 {
			t.Fatal("wrong app received customer push", sender.calls-before)
		}
		before = sender.calls
		if status, _ := s.deliverPartnerNotification(ctx, job); status != "delivered" || sender.calls != before {
			t.Fatal("duplicate device send")
		}
		if err = s.pgStore.FinishPartnerNotification(ctx, job, "delivered", "", 0); err != nil {
			t.Fatal(err)
		}
	}
}
