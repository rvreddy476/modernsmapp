package store

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// Reviewed evidence is a prerequisite, not an approval bypass. This helper is
// used only by isolated database fixtures that exercise the final decision.
func seedReviewedPartnerEvidence(t *testing.T, s *Store, partner uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	actor := uuid.New()
	for _, kind := range []string{"driving_license", "profile_photo"} {
		media := uuid.New()
		d, err := s.CreatePartnerDocumentWithMedia(ctx, CreatePartnerDocumentInput{PartnerID: partner, DocumentType: kind, FileURL: "private-qa-reference"}, &media)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.SetPartnerDocumentStatusBy(ctx, d.ID, "approved", nil, actor); err != nil {
			t.Fatal(err)
		}
	}
	v, err := s.CreateVehicle(ctx, CreateVehicleInput{PartnerID: partner, VehicleType: "auto", RegistrationNumber: "QA" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateVehicleDocument(ctx, CreateVehicleDocumentInput{VehicleID: v.ID, DocumentType: "vehicle_rc", FileURL: "private-qa-reference"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetVehicleStatusBy(ctx, v.ID, "approved", actor); err != nil {
		t.Fatal(err)
	}
}

func TestPartnerReviewDurableRecipientAndDuplicateDecision(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p := seedPartnerRow(t, s)
	actor := uuid.New()
	for i := 0; i < 2; i++ {
		if err := s.UpdatePartnerStatusBy(ctx, p.ID, "rejected", PartnerReview{ActorID: actor, Reason: "Please replace the unclear image"}); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var raw []byte
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM rider.outbox_events WHERE event_type='rider.partner.kyc_rejected' AND payload->>'partner_id'=$1`, p.ID.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate notifications: %d", count)
	}
	if err := s.db.QueryRow(ctx, `SELECT payload FROM rider.outbox_events WHERE event_type='rider.partner.kyc_rejected' AND payload->>'partner_id'=$1`, p.ID.String()).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if event["partner_user_id"] != p.UserID.String() || event["actor_id"] != actor.String() || event["reason"] != "Please replace the unclear image" {
		t.Fatalf("wrong event: %s", raw)
	}
	d, err := s.CreatePartnerDocument(ctx, CreatePartnerDocumentInput{PartnerID: p.ID, DocumentType: "profile_photo", FileURL: "private-test-reference"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.SetPartnerDocumentStatusBy(ctx, d.ID, "approved", nil, actor); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM rider.outbox_events WHERE event_type='rider.partner.document_reviewed' AND payload->>'partner_id'=$1`, p.ID.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate document decisions: %d", count)
	}
}

func TestPartnerReviewOutboxFailureRollsBackDecisions(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p := seedPartnerRow(t, s)
	d, err := s.CreatePartnerDocument(ctx, CreatePartnerDocumentInput{PartnerID: p.ID, DocumentType: "profile_photo", FileURL: "private-test-reference"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(ctx, `CREATE OR REPLACE FUNCTION rider.reject_test_review() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test queue unavailable'; END $$;
	CREATE TRIGGER reject_test_review BEFORE INSERT ON rider.outbox_events FOR EACH ROW EXECUTE FUNCTION rider.reject_test_review()`)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Exec(ctx, `DROP TRIGGER reject_test_review ON rider.outbox_events; DROP FUNCTION rider.reject_test_review()`)
	if err := s.UpdatePartnerStatus(ctx, p.ID, "rejected"); err == nil {
		t.Fatal("decision ignored queue failure")
	}
	if _, err := s.SetPartnerDocumentStatusBy(ctx, d.ID, "approved", nil, uuid.New()); err == nil {
		t.Fatal("document ignored queue failure")
	}
	if err := s.RecordUnderReview(ctx, p.ID, []string{"profile_photo"}); err == nil {
		t.Fatal("under review ignored queue failure")
	}
	p2, err := s.GetPartner(ctx, p.ID)
	if err != nil || p2.Status != p.Status {
		t.Fatalf("partner decision committed: %v %v", p2, err)
	}
	d2, err := s.GetPartnerDocument(ctx, d.ID)
	if err != nil || d2.Status != "pending" {
		t.Fatalf("document decision committed: %v %v", d2, err)
	}
	_, recorded, err := s.UnderReviewPending(ctx, p.ID)
	if err != nil || recorded {
		t.Fatal("audit committed without notification")
	}
}

func TestPartnerReviewConcurrentPendingSetAndApprovedGuard(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p := seedPartnerRow(t, s)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.RecordUnderReview(ctx, p.ID, []string{"profile_photo", "driving_license"})
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordUnderReview(ctx, p.ID, []string{"driving_license", "profile_photo"}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM rider.outbox_events WHERE event_type='rider.partner.under_review' AND payload->>'partner_id'=$1`, p.ID.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("concurrent duplicates: %d", count)
	}
	if _, err := s.db.Exec(ctx, `UPDATE rider_partners SET status='approved' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUnderReview(ctx, p.ID, []string{"vehicle_rc"}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM rider.outbox_events WHERE event_type='rider.partner.under_review' AND payload->>'partner_id'=$1`, p.ID.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("approved partner regressed to under review")
	}
}
