package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDocumentReminderAndSubscriptionRejectionAtomicOutbox(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p := seedPartnerRow(t, s)
	d := ExpiringDocument{PartnerID: p.ID, DocumentID: uuid.New(), OwnerKind: "partner", DocumentKind: "driving_license", ExpiresAt: time.Now().Add(time.Hour)}
	plan, err := s.GetPlanByCode(ctx, "basic_199")
	if err != nil {
		t.Fatal(err)
	}
	pay, err := s.CreateSubscriptionPayment(ctx, CreateSubscriptionPaymentInput{PartnerID: p.ID, PlanID: plan.ID, Amount: 199, PaymentMethod: "manual_upi"})
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	for i := 0; i < 2; i++ {
		newly, err := s.QueueDocumentReminder(ctx, d, "1d")
		if err != nil || newly != (i == 0) {
			t.Fatalf("reminder %d: inserted=%v err=%v", i, newly, err)
		}
		if err = s.RejectSubscriptionPaymentBy(ctx, pay.ID, PartnerReview{ActorID: actor, Reason: "Replace unclear proof"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, typ := range []string{"rider.document.expiring", "rider.subscription.payment_rejected"} {
		var count int
		if err := s.db.QueryRow(ctx, `SELECT count(*) FROM rider.outbox_events WHERE event_type=$1 AND payload->>'partner_user_id'=$2`, typ, p.UserID.String()).Scan(&count); err != nil || count != 1 {
			t.Fatalf("recipient/deduplication %s: %d %v", typ, count, err)
		}
	}
	_, err = s.db.Exec(ctx, `CREATE OR REPLACE FUNCTION rider.reject_test_reminder() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test queue unavailable'; END $$;
	 CREATE TRIGGER reject_test_reminder BEFORE INSERT ON rider.outbox_events FOR EACH ROW EXECUTE FUNCTION rider.reject_test_reminder()`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := s.db.Exec(ctx, `DROP TRIGGER reject_test_reminder ON rider.outbox_events; DROP FUNCTION rider.reject_test_reminder()`); err != nil {
			t.Errorf("drop test trigger: %v", err)
		}
	}()
	d.DocumentID = uuid.New()
	if _, err = s.QueueDocumentReminder(ctx, d, "3d"); err == nil {
		t.Fatal("queue outage ignored")
	}
	if exists, err := s.HasReminderSent(ctx, d.DocumentID, "3d"); err != nil || exists {
		t.Fatalf("dedupe committed on failure: %v %v", exists, err)
	}
	pay2, err := s.CreateSubscriptionPayment(ctx, CreateSubscriptionPaymentInput{PartnerID: p.ID, PlanID: plan.ID, Amount: 199, PaymentMethod: "manual_upi"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RejectSubscriptionPaymentBy(ctx, pay2.ID, PartnerReview{ActorID: actor, Reason: "Unclear proof"}); err == nil {
		t.Fatal("rejection ignored queue outage")
	}
	pay2, err = s.GetSubscriptionPayment(ctx, pay2.ID)
	if err != nil || pay2.Status != "pending" {
		t.Fatalf("rejection committed without event: %+v %v", pay2, err)
	}
}
