package service

import (
	"context"
	"github.com/google/uuid"
	"testing"
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
	state, _ := svc.GetMyPartner(ctx, user)
	if state.Status == "approved" {
		t.Fatal("document review automatically approved account")
	}
	approved, err := svc.ApprovePartner(ctx, p.ID, admin)
	if err != nil || approved.Status != "approved" {
		t.Fatalf("manual approval failed: %v", err)
	}
}
