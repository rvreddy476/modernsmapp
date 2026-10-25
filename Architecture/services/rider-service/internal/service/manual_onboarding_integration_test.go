package service

import (
	"context"
	"testing"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

func TestManualRiderOnboardingOnDatabase(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	user, p := newCaptainUser(t, svc, "Offline Captain")
	image := uuid.New()
	dl, err := svc.SubmitKYCDocument(ctx, user, p.ID, SubmitKYCDocumentRequest{DocumentType: DocDrivingLicence, MediaID: &image, FileURL: "media://" + image.String()})
	if err != nil {
		t.Fatal(err)
	}
	selfie, err := svc.SubmitKYCDocument(ctx, user, p.ID, SubmitKYCDocumentRequest{DocumentType: DocSelfie, MediaID: &image, FileURL: "media://" + image.String()})
	if err != nil {
		t.Fatal(err)
	}
	vehicle, err := svc.AddVehicle(ctx, user, p.ID, AddVehicleRequest{VehicleType: "bike", RegistrationNumber: "TS09AB1234"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyVehicle(ctx, vehicle.ID, uuid.New()); err == nil {
		t.Fatal("vehicle approved without RC")
	}
	rc, err := svc.SubmitVehicleDocument(ctx, user, vehicle.ID, SubmitVehicleDocumentRequest{DocumentType: DocVehicleRC, MediaID: &image, FileURL: "media://" + image.String()})
	if err != nil {
		t.Fatal(err)
	}
	if rc.Status != "pending" {
		t.Fatal("RC auto approved")
	}
	admin := uuid.New()
	if _, err := svc.ApprovePartner(ctx, p.ID, admin); err == nil {
		t.Fatal("pending uploads approved account")
	}
	if _, err := svc.VerifyDocument(ctx, dl.ID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyDocument(ctx, selfie.ID, admin); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyVehicle(ctx, vehicle.ID, admin); err != nil {
		t.Fatal(err)
	}
	// Founder, 25 Oct ("nothing manual, instant approval"): once a person
	// has reviewed the hand-uploaded documents, the account is approved
	// right away by the evaluator, behind the same evidence gate as the
	// admin's approval; there is no second manual step.
	state, _ := svc.GetMyPartner(ctx, user)
	if state.Status != "approved" || state.KYCStatus != "approved" || state.ApprovedAt == nil {
		t.Fatalf("reviewed documents did not approve the account: %+v", state)
	}
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider_admin_audit_logs WHERE admin_user_id = $1 AND action = 'partner.auto_approve' AND entity_id = $2`, store.SystemActorID, p.ID); n != 1 {
		t.Fatalf("partner.auto_approve audit rows = %d, want 1", n)
	}
	// The hand-uploaded documents were each reviewed by the admin, never
	// verified automatically.
	for _, id := range []uuid.UUID{dl.ID, selfie.ID} {
		d, err := svc.Store().GetPartnerDocument(ctx, id)
		if err != nil || d.Status != "approved" || d.VerifiedByActor == nil || *d.VerifiedByActor != admin.String() {
			t.Fatalf("document %s after review = %+v %v", id, d, err)
		}
	}
	if v, err := svc.Store().GetVehicle(ctx, vehicle.ID); err != nil || v.VerifiedByActor == nil || *v.VerifiedByActor != admin.String() {
		t.Fatalf("vehicle after review = %+v %v", v, err)
	}
	// Approved once: the admin's approve call has nothing left to do and
	// no second approved event is queued.
	if _, err := svc.ApprovePartner(ctx, p.ID, admin); err == nil {
		t.Fatal("an approved account was approved again")
	}
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider.outbox_events WHERE event_type = 'rider.partner.approved' AND payload->>'partner_id' = $1`, p.ID.String()); n != 1 {
		t.Fatalf("partner.approved events = %d, want 1", n)
	}
}
