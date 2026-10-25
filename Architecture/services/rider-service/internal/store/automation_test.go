package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func seedPartnerRow(t *testing.T, st *Store) *Partner {
	t.Helper()
	p, err := st.CreatePartner(context.Background(), CreatePartnerInput{UserID: uuid.New(), PartnerType: "individual_driver", FullName: "Store Captain", Phone: "+91" + uuid.NewString()[:10]})
	if err != nil {
		t.Fatalf("create partner: %v", err)
	}
	return p
}

// The trial stamp is written with the trial row: a second activation for
// the same partner is refused and leaves no row.
func TestActivateTrialSubscription_OncePerPartner(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p := seedPartnerRow(t, st)
	plan, err := st.GetPlanByCode(ctx, "trial_7d")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	sub, err := st.ActivateTrialSubscription(ctx, p.ID, plan.ID, now, 7)
	if err != nil || sub.Status != SubscriptionTrial || sub.AmountPaise != 0 || sub.PaymentStatus != nil || !sub.ExpiresAt.Equal(now.AddDate(0, 0, 7)) {
		t.Fatalf("trial = %+v %v", sub, err)
	}
	if used, _ := st.TrialUsed(ctx, p.ID); !used {
		t.Fatal("trial_used_at not stamped")
	}
	if _, err := st.ActivateTrialSubscription(ctx, p.ID, plan.ID, now, 7); !errors.Is(err, ErrTrialAlreadyUsed) {
		t.Fatalf("second trial: %v", err)
	}
	var n int
	if err := st.db.QueryRow(ctx, `SELECT COUNT(*) FROM rider_partner_subscriptions WHERE partner_id = $1`, p.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d %v", n, err)
	}
}

// A checkout row is not an active subscription until the capture; binding
// the intent moves it to confirming; a paid row cannot be re-bound.
func TestCheckoutSubscription_Lifecycle(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p := seedPartnerRow(t, st)
	plan, _ := st.GetPlanByCode(ctx, "basic_199")
	sub, err := st.CreateCheckoutSubscription(ctx, CreateCheckoutInput{PartnerID: p.ID, PlanID: plan.ID, AmountPaise: 19900})
	if err != nil || sub.Status != SubscriptionPendingPayment || sub.PaymentStatus == nil || *sub.PaymentStatus != "pending" {
		t.Fatalf("checkout = %+v %v", sub, err)
	}
	if _, err := st.GetActiveSubscription(ctx, p.ID); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("pending checkout counted as active: %v", err)
	}
	open, err := st.GetOpenCheckout(ctx, p.ID, plan.ID)
	if err != nil || open.ID != sub.ID {
		t.Fatalf("open checkout = %+v %v", open, err)
	}
	intent := uuid.New()
	bound, err := st.BindSubscriptionIntent(ctx, sub.ID, intent, "order_x", "upi")
	if err != nil || bound.IntentID == nil || *bound.IntentID != intent || *bound.PaymentStatus != "confirming" || *bound.IntentMethod != "upi" {
		t.Fatalf("bound = %+v %v", bound, err)
	}
	latest, _ := st.LatestCheckout(ctx, p.ID)
	if latest.ID != sub.ID {
		t.Fatalf("latest checkout = %+v", latest)
	}
	if _, err := st.db.Exec(ctx, `UPDATE rider_partner_subscriptions SET payment_status = 'paid', status = 'active' WHERE id = $1`, sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BindSubscriptionIntent(ctx, sub.ID, uuid.New(), "", "card"); !errors.Is(err, ErrCheckoutNotBindable) {
		t.Fatalf("re-bind of a paid checkout: %v", err)
	}
	if _, err := st.GetOpenCheckout(ctx, p.ID, plan.ID); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("paid checkout still open: %v", err)
	}
	if _, err := st.CreateCheckoutSubscription(ctx, CreateCheckoutInput{PartnerID: p.ID, PlanID: plan.ID, AmountPaise: 0}); err == nil {
		t.Fatal("a free checkout row was created")
	}
}

// The evaluator reads the latest document per type with an approved row
// winning: a later manual re-upload never hides the DigiLocker document,
// and an uploaded document is recorded with source upload.
func TestLatestPartnerDocuments_NewUploadNeedsReview(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p := seedPartnerRow(t, st)
	num := "KA0120260000001"
	photo := uuid.New()
	dl, err := st.UpsertDigiLockerDocument(ctx, UpsertDigiLockerDocumentInput{PartnerID: p.ID, DocumentType: "driving_license", DocumentNumber: &num, FileURL: "digilocker://dl", PhotoMediaID: &photo, DigiLockerRef: "ref/dl"})
	if err != nil || dl.Status != "approved" || dl.Source != DocSourceDigiLocker || *dl.VerifiedByActor != VerifiedByAuto {
		t.Fatalf("digilocker dl = %+v %v", dl, err)
	}
	// Re-fetch refreshes the same row instead of adding one.
	again, _ := st.UpsertDigiLockerDocument(ctx, UpsertDigiLockerDocumentInput{PartnerID: p.ID, DocumentType: "driving_license", DocumentNumber: &num, FileURL: "digilocker://dl2", DigiLockerRef: "ref/dl"})
	if again.ID != dl.ID || again.FileURL != "digilocker://dl2" {
		t.Fatalf("refresh = %+v", again)
	}
	up, err := st.CreatePartnerDocumentWithMedia(ctx, CreatePartnerDocumentInput{PartnerID: p.ID, DocumentType: "driving_license", FileURL: "https://x/dl.jpg"}, nil)
	if err != nil || up.Status != "pending" || up.Source != DocSourceUpload || up.VerifiedByActor != nil {
		t.Fatalf("uploaded dl = %+v %v", up, err)
	}
	latest, err := st.LatestPartnerDocuments(ctx, p.ID)
	if err != nil || latest["driving_license"].ID != up.ID || latest["driving_license"].Status != "pending" {
		t.Fatalf("latest = %+v %v", latest, err)
	}
	// AutoVerify only moves a pending row; the audit row names the system.
	if _, err := st.AutoVerifyPartnerDocument(ctx, dl.ID, "face_compare", "x"); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("auto verify of an approved row: %v", err)
	}
	v, err := st.AutoVerifyPartnerDocument(ctx, up.ID, "face_compare", "similarity 95")
	if err != nil || v.Status != "approved" || *v.VerifiedByActor != VerifiedByAuto || *v.AutoCheckDetail != "similarity 95" {
		t.Fatalf("auto verified = %+v %v", v, err)
	}
	var n int
	if err := st.db.QueryRow(ctx, `SELECT COUNT(*) FROM rider_admin_audit_logs WHERE admin_user_id = $1 AND action = 'document.auto_verify' AND entity_id = $2`, SystemActorID, up.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows = %d %v", n, err)
	}
}

// AutoApprovePartner approves a draft / pending partner once, only behind
// the same evidence gate as the admin's approval (d61bd603c), and never an
// approved, suspended or rejected one.
func TestAutoApprovePartner(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p := seedPartnerRow(t, st)
	if ok, err := st.AutoApprovePartner(ctx, p.ID, map[string]any{"reason": "test"}); err != nil || ok {
		t.Fatalf("approved without reviewed evidence: %v %v", ok, err)
	}
	if got, _ := st.GetPartner(ctx, p.ID); got.Status == "approved" {
		t.Fatalf("partner = %+v", got)
	}
	seedReviewedPartnerEvidence(t, st, p.ID)
	ok, err := st.AutoApprovePartner(ctx, p.ID, map[string]any{"reason": "test"})
	if err != nil || !ok {
		t.Fatalf("approve: %v %v", ok, err)
	}
	got, _ := st.GetPartner(ctx, p.ID)
	if got.Status != "approved" || got.KYCStatus != "approved" || got.ApprovedAt == nil {
		t.Fatalf("partner = %+v", got)
	}
	if ok, _ := st.AutoApprovePartner(ctx, p.ID, nil); ok {
		t.Fatal("approved twice")
	}
	var events int
	if err := st.db.QueryRow(ctx, `SELECT COUNT(*) FROM rider.outbox_events WHERE event_type = 'rider.partner.approved' AND payload->>'partner_id' = $1`, p.ID.String()).Scan(&events); err != nil || events != 1 {
		t.Fatalf("partner.approved events = %d %v, want 1", events, err)
	}
	if err := st.UpdatePartnerStatus(ctx, p.ID, "suspended"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.AutoApprovePartner(ctx, p.ID, nil); ok {
		t.Fatal("a suspended partner was approved automatically")
	}
}

// The evidence gate takes a licence DigiLocker returned (issuer reference
// and licence photo, no uploaded image) and refuses one without its photo,
// a pending selfie, or a vehicle approved on a pending RC.
func TestAutoApprovePartner_DigiLockerEvidenceGate(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p := seedPartnerRow(t, st)
	num := "KA0120260000002"
	// A DigiLocker licence without the photo is not evidence.
	if _, err := st.UpsertDigiLockerDocument(ctx, UpsertDigiLockerDocumentInput{PartnerID: p.ID, DocumentType: "driving_license", DocumentNumber: &num, FileURL: "digilocker://dl", DigiLockerRef: "ref/dl"}); err != nil {
		t.Fatal(err)
	}
	selfieMedia := uuid.New()
	selfie, err := st.CreatePartnerDocumentWithMedia(ctx, CreatePartnerDocumentInput{PartnerID: p.ID, DocumentType: "profile_photo", FileURL: "media://selfie"}, &selfieMedia)
	if err != nil {
		t.Fatal(err)
	}
	v, err := st.CreateVehicle(ctx, CreateVehicleInput{PartnerID: p.ID, VehicleType: "auto", RegistrationNumber: "QA" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertDigiLockerVehicleRC(ctx, v.ID, "digilocker://rc", nil, "ref/rc"); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.AutoApproveVehicle(ctx, v.ID); err != nil || !ok {
		t.Fatalf("vehicle: %v %v", ok, err)
	}
	if ok, _ := st.AutoApprovePartner(ctx, p.ID, nil); ok {
		t.Fatal("approved with a pending selfie and a licence without its photo")
	}
	if _, err := st.AutoVerifyPartnerDocument(ctx, selfie.ID, "face_compare", "similarity 96"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.AutoApprovePartner(ctx, p.ID, nil); ok {
		t.Fatal("approved on a DigiLocker licence without its photo")
	}
	photo := uuid.New()
	if _, err := st.UpsertDigiLockerDocument(ctx, UpsertDigiLockerDocumentInput{PartnerID: p.ID, DocumentType: "driving_license", DocumentNumber: &num, FileURL: "digilocker://dl", PhotoMediaID: &photo, DigiLockerRef: "ref/dl"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.AutoApprovePartner(ctx, p.ID, nil); err != nil || !ok {
		t.Fatalf("DigiLocker licence, matched selfie, DigiLocker RC: %v %v", ok, err)
	}
}

// AutoApproveVehicle approves only a pending vehicle whose latest RC came
// from DigiLocker; a hand-uploaded RC waits for the admin, also once a
// newer hand upload replaced the DigiLocker one.
func TestAutoApproveVehicle_OnlyDigiLockerRC(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p := seedPartnerRow(t, st)
	newVehicle := func() *Vehicle {
		v, err := st.CreateVehicle(ctx, CreateVehicleInput{PartnerID: p.ID, VehicleType: "auto", RegistrationNumber: "QA" + uuid.NewString()[:8]})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	statusOf := func(id uuid.UUID) string {
		var s string
		if err := st.db.QueryRow(ctx, `SELECT status::text FROM rider_vehicles WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	// Hand-uploaded RC: never approved automatically.
	uploaded := newVehicle()
	if _, err := st.CreateVehicleDocument(ctx, CreateVehicleDocumentInput{VehicleID: uploaded.ID, DocumentType: "vehicle_rc", FileURL: "media://rc"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.AutoApproveVehicle(ctx, uploaded.ID); err != nil || ok || statusOf(uploaded.ID) != "pending" {
		t.Fatalf("hand-uploaded RC approved its vehicle: %v %v", ok, err)
	}
	// No RC at all.
	bare := newVehicle()
	if ok, _ := st.AutoApproveVehicle(ctx, bare.ID); ok {
		t.Fatal("a vehicle without an RC was approved")
	}
	// DigiLocker RC: approved once, under the system actor.
	pulled := newVehicle()
	if _, err := st.UpsertDigiLockerVehicleRC(ctx, pulled.ID, "digilocker://rc", nil, "ref/rc"); err != nil {
		t.Fatal(err)
	}
	if statusOf(pulled.ID) != "pending" {
		t.Fatal("recording the RC approved the vehicle by itself")
	}
	if ok, err := st.AutoApproveVehicle(ctx, pulled.ID); err != nil || !ok || statusOf(pulled.ID) != "approved" {
		t.Fatalf("DigiLocker RC: %v %v", ok, err)
	}
	if ok, _ := st.AutoApproveVehicle(ctx, pulled.ID); ok {
		t.Fatal("approved twice")
	}
	var n int
	if err := st.db.QueryRow(ctx, `SELECT COUNT(*) FROM rider_admin_audit_logs WHERE admin_user_id = $1 AND action = 'vehicle.auto_verify' AND entity_id = $2`, SystemActorID, pulled.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("vehicle.auto_verify audit rows = %d %v", n, err)
	}
	// A DigiLocker RC replaced by a newer hand upload: the upload decides.
	replaced := newVehicle()
	if _, err := st.UpsertDigiLockerVehicleRC(ctx, replaced.ID, "digilocker://rc", nil, "ref/rc2"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateVehicleDocument(ctx, CreateVehicleDocumentInput{VehicleID: replaced.ID, DocumentType: "vehicle_rc", FileURL: "media://rc-new"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.AutoApproveVehicle(ctx, replaced.ID); ok {
		t.Fatal("a newer hand-uploaded RC was approved through the older DigiLocker one")
	}
	// An expired DigiLocker RC is not current.
	expired := newVehicle()
	past := time.Now().Add(-24 * time.Hour)
	if _, err := st.UpsertDigiLockerVehicleRC(ctx, expired.ID, "digilocker://rc", &past, "ref/rc3"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.AutoApproveVehicle(ctx, expired.ID); ok {
		t.Fatal("an expired RC approved its vehicle")
	}
}
