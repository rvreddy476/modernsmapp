package service

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestCommerceDeliveryOfferAbsoluteExpiryAndRouting(t *testing.T) {
	user, job, offer := uuid.New(), uuid.New(), uuid.New()
	expiry := time.Now().Add(-time.Minute).UTC()
	n, err := commerceWorkNotice("commerce.delivery.offer", "event", user, job, offer, time.Now(), expiry)
	if err != nil {
		t.Fatal(err)
	}
	if n.Inbox || !n.ExpiresAt.Equal(expiry) || n.EntityID != offer || n.Data["job_id"] != job.String() || n.UserID != user {
		t.Fatalf("incorrect offer: %+v", n)
	}
	again, _ := commerceWorkNotice("commerce.delivery.offer", "event", user, job, offer, time.Now(), expiry)
	if again.ID != n.ID {
		t.Fatal("retries must deduplicate")
	}
	if _, err = commerceWorkNotice("commerce.delivery.offer", "event", user, job, offer, time.Now(), time.Time{}); err == nil {
		t.Fatal("expiry required")
	}
	if _, err = commerceWorkNotice("commerce.delivery.offer", "event", uuid.Nil, job, offer, time.Now(), expiry); err == nil {
		t.Fatal("recipient must be authoritative")
	}
}

func TestCommerceDeliveryUpdatesHaveDurableInbox(t *testing.T) {
	for _, typ := range []string{"assigned", "picked_up", "delivered", "failed", "cancelled", "earnings", "support"} {
		job := uuid.New()
		n, err := commerceWorkNotice("commerce.delivery."+typ, "event", uuid.New(), job, uuid.Nil, time.Now(), time.Time{})
		if err != nil || !n.Inbox || n.ExpiresAt != nil || n.EntityID != job {
			t.Fatalf("%s: %+v %v", typ, n, err)
		}
	}
}
