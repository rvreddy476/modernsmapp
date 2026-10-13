package postgres

import (
	"context"
	"github.com/atpost/food-service/internal/riderkyc"
	"github.com/google/uuid"
	"testing"
)

func TestManualDeliveryIdentityOnDatabase(t *testing.T) {
	st, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, admin := uuid.New(), uuid.New()
	partner, err := st.UpsertDeliveryPartner(ctx, user, DeliveryPartnerInput{FullName: "Offline Delivery Partner", Phone: "+919111111102", VehicleType: "BICYCLE"})
	if err != nil {
		t.Fatal(err)
	}
	image := uuid.New()
	passport, err := st.AddDeliveryPartnerDocument(ctx, user, DeliveryDocumentRecord{DocumentType: "PASSPORT", MediaID: &image, Status: DocumentStatusPending})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdminDecideDeliveryPartnerDocument(ctx, admin, partner.ID, passport.ID, "APPROVED", ""); err != nil {
		t.Fatal(err)
	}
	kyc, err := st.AdminDeliveryPartnerKYC(ctx, partner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hasStep(kyc.Missing, riderkyc.StepIdentity) || !hasStep(kyc.Missing, riderkyc.StepDrivingLicence) {
		t.Fatal("passport replaced mandatory licence")
	}
	licence, err := st.AddDeliveryPartnerDocument(ctx, user, DeliveryDocumentRecord{DocumentType: "DRIVING_LICENCE", MediaID: &image, Status: DocumentStatusPending, NumberSealed: []byte("test-sealed"), NumberKeyVersion: 1, NumberLookup: "test-" + user.String(), NumberMasked: "****0001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdminDecideDeliveryPartnerDocument(ctx, admin, partner.ID, licence.ID, "APPROVED", ""); err != nil {
		t.Fatal(err)
	}
	kyc, err = st.AdminDeliveryPartnerKYC(ctx, partner.ID)
	if err != nil || hasStep(kyc.Missing, riderkyc.StepDrivingLicence) {
		t.Fatal("reviewed uploaded licence did not satisfy licence step")
	}
	// Replacing the reviewed file must not preserve its previous approval.
	newImage := uuid.New()
	changed, err := st.AddDeliveryPartnerDocument(ctx, user, DeliveryDocumentRecord{DocumentType: "DRIVING_LICENCE", MediaID: &newImage, Status: DocumentStatusPending, NumberSealed: []byte("new-test-sealed"), NumberKeyVersion: 1, NumberLookup: "test-" + user.String(), NumberMasked: "****0001"})
	if err != nil || changed.Status != DocumentStatusPending {
		t.Fatalf("replacement retained approval: %v", err)
	}
	kyc, err = st.AdminDeliveryPartnerKYC(ctx, partner.ID)
	if err != nil || !hasStep(kyc.Missing, riderkyc.StepDrivingLicence) {
		t.Fatal("replacement licence skipped review")
	}
}
func hasStep(steps []string, step string) bool {
	for _, s := range steps {
		if s == step {
			return true
		}
	}
	return false
}
