package service

import (
	"context"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestKitchenQueueDBDurableRetryAudienceAndDuplicate(t *testing.T) {
	s, db, p := partnerQueueRig(t)
	ctx := context.Background()
	user := uuid.New()
	deadline := time.Now().Add(time.Minute)
	for _, app := range []string{postgres.AppFeastKitchen, postgres.AppPartner} {
		if _, err := s.pgStore.RegisterDevice(ctx, user, "android", "synthetic-kitchen-"+app+user.String(), app); err != nil {
			t.Fatal(err)
		}
	}
	n := FoodPush{DedupKey: "kitchen-test", RecipientID: user, App: AppFeastKitchen, Type: FoodTypeOrderNew, OrderID: uuid.New(), CreatedAt: time.Now(), AcceptDeadlineAt: &deadline, DeepLink: "/kitchen", Title: "New order"}
	for i := 0; i < 2; i++ {
		if _, err := s.DeliverFoodPush(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM partner_notification_jobs`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("queue dedup: %d %v", count, err)
	}
	j, err := s.pgStore.ClaimPartnerNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.fail = true
	if status, _ := s.deliverPartnerNotification(ctx, j); status != "pending" {
		t.Fatal("failure lost", status)
	}
	if p.calls != 1 {
		t.Fatalf("wrong audience: %d", p.calls)
	}
	p.fail = false
	if status, code := s.deliverPartnerNotification(ctx, j); status != "delivered" {
		t.Fatal(status, code)
	}
	if p.calls != 2 {
		t.Fatalf("wrong audience retry: %d", p.calls)
	}
	if status, _ := s.deliverPartnerNotification(ctx, j); status != "delivered" || p.calls != 2 {
		t.Fatal("duplicate successful delivery")
	}
}

func TestKitchenQueueDBLegacyAndExpiredOrdersNeverRing(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "expired", true: "legacy"}[legacy], func(t *testing.T) {
			s, _, p := partnerQueueRig(t)
			ctx := context.Background()
			expired := time.Now().Add(-time.Second)
			n := FoodPush{DedupKey: "expired-order", RecipientID: uuid.New(), App: AppFeastKitchen, Type: FoodTypeOrderNew, OrderID: uuid.New(), CreatedAt: time.Now(), AcceptDeadlineAt: &expired}
			if legacy {
				n.AcceptDeadlineAt = nil
			}
			if _, err := s.DeliverFoodPush(ctx, n); err != nil {
				t.Fatal(err)
			}
			j, err := s.pgStore.ClaimPartnerNotification(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if status, _ := s.deliverPartnerNotification(ctx, j); status != "expired" || p.calls != 0 {
				t.Fatal("stale order rang", status)
			}
		})
	}
}
