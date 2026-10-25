package service

// Launch-safety automation (TEST_PG_DSN on rider_it_test): subscription
// checkout through payments-service, government-verified captain approval
// and the refund rules. Each test proves the mutation it guards against:
// trial activated twice, a subscription activated without a signed event,
// a wrong-amount capture, an uploaded document auto-verified, a selfie below
// threshold auto-verified, a partner approved with a pending document, a
// captain-cancel refund on a cash ride, a duplicate capture not refunded.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/digilocker"
	"github.com/atpost/rider-service/internal/payments"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/identityroles"
	"github.com/google/uuid"
)

func newCaptainUser(t *testing.T, svc *Service, name string) (uuid.UUID, *store.Partner) {
	t.Helper()
	blr := pickBangaloreCity(t, svc)
	uid := uuid.New()
	p, err := svc.CreatePartnerProfile(context.Background(), uid, CreatePartnerRequest{
		PartnerType: "individual_driver", FullName: name, Phone: "+9197" + uid.String()[:8], CityID: &blr.ID,
	})
	if err != nil {
		t.Fatalf("create partner: %v", err)
	}
	return uid, p
}

func subCaptureEvent(subID, intentID, payer uuid.UUID, amount int64) payments.Event {
	return payments.Event{EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentSucceeded, IntentID: intentID.String(),
		ReferenceType: payments.RefTypeMopeduSubscription, ReferenceID: subID, PayerID: payer, AmountMinor: amount, Currency: "INR", Status: "succeeded"}
}

func countRows(t *testing.T, svc *Service, q string, args ...any) int {
	t.Helper()
	var n int
	if err := svc.Store().DB().QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

// --- 1. Subscription checkout -----------------------------------------------

func TestSubscriptionCheckout_PaidPlanActivatesOnlyFromSignedCapture(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	fake := newFakePayments()
	svc.SetPayments(fake, "dev")
	uid, partner := newCaptainUser(t, svc, "Checkout Captain")

	// A bad method and an unknown plan are refused before payments.
	_, err := svc.CheckoutSubscription(ctx, uid, "basic_199", "cash")
	payCode(t, err, http.StatusBadRequest, CodePaymentMethodInvalid)
	_, err = svc.CheckoutSubscription(ctx, uid, "no_such_plan", "upi")
	payCode(t, err, http.StatusNotFound, CodePlanNotFound)
	if len(fake.subCreated) != 0 {
		t.Fatal("payments called for a refused checkout")
	}

	out, err := svc.CheckoutSubscription(ctx, uid, "basic_199", "upi")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if out.Status != store.SubscriptionPendingPayment || out.IntentID == nil || out.AmountPaise != 19900 || out.Currency != "INR" || out.ClientSession == nil {
		t.Fatalf("checkout = %+v", out)
	}
	in := fake.subCreated[0]
	if in.ReferenceID != out.SubscriptionID || in.PayerID != uid || in.AmountMinor != 19900 || in.IdempotencyKey != "subscription:"+out.SubscriptionID.String()+":upi" {
		t.Fatalf("payments request = %+v", in)
	}
	st, err := svc.GetMySubscriptionPayment(ctx, uid)
	if err != nil || st.Status != payments.SubscriptionPaymentConfirming || st.IntentID == nil || *st.IntentID != *out.IntentID || st.ExpiresAt != nil {
		t.Fatalf("payment status = %+v %v", st, err)
	}
	// Mutation guard: an open checkout is NOT a subscription.
	if _, err := svc.GetMySubscription(ctx, uid); err == nil || !contains(err.Error(), "not_found") {
		t.Fatalf("subscription active before any signed event: %v", err)
	}
	// Re-opening reuses the row and the intent (deterministic key).
	again, err := svc.CheckoutSubscription(ctx, uid, "basic_199", "upi")
	if err != nil || again.SubscriptionID != out.SubscriptionID || *again.IntentID != *out.IntentID {
		t.Fatalf("re-open: %+v %v", again, err)
	}

	// Mutation guard: a wrong-amount capture activates nothing and queues
	// reconciliation on the subscription.
	applied, err := svc.Store().ApplyRidePaymentEvent(ctx, subCaptureEvent(out.SubscriptionID, *out.IntentID, uid, 100))
	if err != nil || applied.Decision.Outcome != payments.OutcomeMismatch {
		t.Fatalf("wrong amount: %+v %v", applied, err)
	}
	if _, err := svc.GetMySubscription(ctx, uid); err == nil {
		t.Fatal("wrong-amount capture activated the subscription")
	}
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider_payment_reconciliation WHERE subscription_id = $1 AND canonical_status = 'reconciliation_required'`, out.SubscriptionID); n != 1 {
		t.Fatalf("reconciliation rows = %d", n)
	}
	// Another captain's capture for this subscription is a mismatch too.
	applied, _ = svc.Store().ApplyRidePaymentEvent(ctx, subCaptureEvent(out.SubscriptionID, *out.IntentID, uuid.New(), 19900))
	if applied.Decision.Outcome != payments.OutcomeMismatch {
		t.Fatalf("other payer: %+v", applied.Decision)
	}

	// Only the signed capture activates.
	before := time.Now().UTC()
	applied, err = svc.Store().ApplyRidePaymentEvent(ctx, subCaptureEvent(out.SubscriptionID, *out.IntentID, uid, 19900))
	if err != nil || applied.Decision.Outcome != payments.OutcomeSubscriptionActivated || applied.Target != payments.TargetSubscription || applied.PartnerID != partner.ID {
		t.Fatalf("capture: %+v %v", applied, err)
	}
	svc.OnRidePaymentApplied(ctx, applied)
	sub, err := svc.GetMySubscription(ctx, uid)
	if err != nil || sub.ID != out.SubscriptionID || sub.Status != store.SubscriptionActive {
		t.Fatalf("after capture: %+v %v", sub, err)
	}
	if sub.StartsAt.Before(before.Add(-time.Second)) || sub.ExpiresAt.Sub(sub.StartsAt) != 30*24*time.Hour {
		t.Fatalf("period = %s .. %s", sub.StartsAt, sub.ExpiresAt)
	}
	st, _ = svc.GetMySubscriptionPayment(ctx, uid)
	if st.Status != payments.SubscriptionPaymentPaid || st.ExpiresAt == nil {
		t.Fatalf("payment status after capture = %+v", st)
	}
	// A re-delivered event is a duplicate; a later failure never un-pays.
	dup := subCaptureEvent(out.SubscriptionID, *out.IntentID, uid, 19900)
	dup.EventID = "evt-dup-" + uuid.NewString()
	if a, _ := svc.Store().ApplyRidePaymentEvent(ctx, dup); a.Decision.Outcome != payments.OutcomeAlreadyPaid {
		t.Fatalf("second capture: %+v", a.Decision)
	}
	failed := subCaptureEvent(out.SubscriptionID, *out.IntentID, uid, 19900)
	failed.EventType, failed.Status = events.EventPaymentFailed, "failed"
	if a, _ := svc.Store().ApplyRidePaymentEvent(ctx, failed); a.Decision.Effect != payments.EffectNone {
		t.Fatalf("failure after paid: %+v", a.Decision)
	}
	if sub, _ = svc.GetMySubscription(ctx, uid); sub.Status != store.SubscriptionActive {
		t.Fatalf("status after late failure = %s", sub.Status)
	}
	// The retired proof route answers 410; the legacy admin verify refuses a
	// subscription that has an intent.
	_, err = svc.SubmitPaymentProof(ctx, uid, uuid.New(), "https://x/proof.jpg")
	payCode(t, err, http.StatusGone, CodeSubscriptionProofGone)
	legacy, err := svc.Store().CreateSubscriptionPayment(ctx, store.CreateSubscriptionPaymentInput{PartnerID: partner.ID, PlanID: sub.PlanID, Amount: 199, PaymentMethod: "wallet"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_subscription_payments SET subscription_id = $2 WHERE id = $1`, legacy.ID, sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifySubscriptionPayment(ctx, legacy.ID, uuid.New()); err == nil || !contains(err.Error(), "invalid:") {
		t.Fatalf("admin verify of a checkout subscription: %v", err)
	}
	if err := svc.RejectSubscriptionPayment(ctx, legacy.ID, uuid.New(), "nope"); err == nil || !contains(err.Error(), "invalid:") {
		t.Fatalf("admin reject of a checkout subscription: %v", err)
	}
}

func TestSubscriptionCheckout_FailedThenRetrySameIntent(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	fake := newFakePayments()
	svc.SetPayments(fake, "dev")
	uid, _ := newCaptainUser(t, svc, "Retry Captain")
	out, err := svc.CheckoutSubscription(ctx, uid, "basic_199", "card")
	if err != nil {
		t.Fatal(err)
	}
	failed := subCaptureEvent(out.SubscriptionID, *out.IntentID, uid, 19900)
	failed.EventType, failed.Status, failed.Reason = events.EventPaymentFailed, "failed", "card declined"
	a, err := svc.Store().ApplyRidePaymentEvent(ctx, failed)
	if err != nil || a.Decision.Outcome != payments.OutcomeSubscriptionFailed {
		t.Fatalf("failed: %+v %v", a, err)
	}
	st, _ := svc.GetMySubscriptionPayment(ctx, uid)
	if st.Status != payments.SubscriptionPaymentFailed {
		t.Fatalf("status after failure = %+v", st)
	}
	// The captain retries: same row, same intent, then the capture pays.
	retry, err := svc.CheckoutSubscription(ctx, uid, "basic_199", "card")
	if err != nil || retry.SubscriptionID != out.SubscriptionID || *retry.IntentID != *out.IntentID {
		t.Fatalf("retry = %+v %v", retry, err)
	}
	if a, _ := svc.Store().ApplyRidePaymentEvent(ctx, subCaptureEvent(out.SubscriptionID, *out.IntentID, uid, 19900)); a.Decision.Outcome != payments.OutcomeSubscriptionActivated {
		t.Fatalf("capture after retry: %+v", a.Decision)
	}
	if sub, err := svc.GetMySubscription(ctx, uid); err != nil || sub.Status != store.SubscriptionActive {
		t.Fatalf("after retry: %+v %v", sub, err)
	}
}

// Mutation guard: the free trial activates once per partner, ever — on the
// checkout route and on the legacy wallet route alike, and never through
// payments.
func TestSubscriptionCheckout_TrialOncePerPartner(t *testing.T) {
	svc, walletMock, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	fake := newFakePayments()
	svc.SetPayments(fake, "dev")
	uid, _ := newCaptainUser(t, svc, "Trial Captain")
	out, err := svc.CheckoutSubscription(ctx, uid, "trial_7d", "")
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	if out.Status != store.SubscriptionTrial || out.IntentID != nil || out.AmountPaise != 0 || out.ExpiresAt == nil || out.ExpiresAt.Sub(svc.now()) > 7*24*time.Hour+time.Minute {
		t.Fatalf("trial = %+v", out)
	}
	if len(fake.subCreated) != 0 || len(walletMock.Debits()) != 0 {
		t.Fatal("trial touched payments or the wallet")
	}
	sub, err := svc.GetMySubscription(ctx, uid)
	if err != nil || sub.Status != store.SubscriptionTrial {
		t.Fatalf("active trial = %+v %v", sub, err)
	}
	_, err = svc.CheckoutSubscription(ctx, uid, "trial_7d", "")
	payCode(t, err, http.StatusConflict, CodeTrialAlreadyUsed)
	// After the trial lapsed the partner still cannot take a second one.
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_partner_subscriptions SET status = 'expired' WHERE id = $1`, sub.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.CheckoutSubscription(ctx, uid, "trial_7d", "upi")
	payCode(t, err, http.StatusConflict, CodeTrialAlreadyUsed)
	plan, _ := svc.Store().GetPlanByCode(ctx, "trial_7d")
	_, err = svc.Subscribe(ctx, uid, plan.ID, "wallet", "idem-trial-twice")
	payCode(t, err, http.StatusConflict, CodeTrialAlreadyUsed)
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider_partner_subscriptions WHERE partner_id = (SELECT id FROM rider_partners WHERE user_id = $1) AND status = 'trial'`, uid); n != 0 {
		t.Fatalf("trial rows = %d", n)
	}
}

// A renewal while a period runs starts where the current period ends —
// never reset to now — and supersedes the old row so the expiry reminders
// follow the new period.
func TestSubscriptionCheckout_RenewalExtendsFromCurrentExpiry(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	fake := newFakePayments()
	svc.SetPayments(fake, "dev")
	uid, _ := newCaptainUser(t, svc, "Renew Captain")
	first, err := svc.CheckoutSubscription(ctx, uid, "basic_199", "upi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store().ApplyRidePaymentEvent(ctx, subCaptureEvent(first.SubscriptionID, *first.IntentID, uid, 19900)); err != nil {
		t.Fatal(err)
	}
	current, _ := svc.GetMySubscription(ctx, uid)
	// Pretend 20 days passed: the current period ends in 10 days.
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_partner_subscriptions SET starts_at = NOW() - INTERVAL '20 days', expires_at = NOW() + INTERVAL '10 days' WHERE id = $1`, current.ID); err != nil {
		t.Fatal(err)
	}
	current, _ = svc.GetMySubscription(ctx, uid)

	renewal, err := svc.CheckoutSubscription(ctx, uid, "basic_199", "upi")
	if err != nil || renewal.SubscriptionID == current.ID || renewal.Status != store.SubscriptionPendingPayment {
		t.Fatalf("renewal checkout = %+v %v", renewal, err)
	}
	// Mutation guard: nothing changes before the capture.
	if cur, _ := svc.GetMySubscription(ctx, uid); cur.ID != current.ID || !cur.ExpiresAt.Equal(current.ExpiresAt) {
		t.Fatalf("current changed before capture: %+v", cur)
	}
	a, err := svc.Store().ApplyRidePaymentEvent(ctx, subCaptureEvent(renewal.SubscriptionID, *renewal.IntentID, uid, 19900))
	if err != nil || a.Decision.Outcome != payments.OutcomeSubscriptionActivated {
		t.Fatalf("renewal capture: %+v %v", a, err)
	}
	renewed, err := svc.GetMySubscription(ctx, uid)
	if err != nil || renewed.ID != renewal.SubscriptionID || renewed.Status != store.SubscriptionActive {
		t.Fatalf("after renewal: %+v %v", renewed, err)
	}
	if !renewed.StartsAt.Equal(current.ExpiresAt) || !renewed.ExpiresAt.Equal(current.ExpiresAt.AddDate(0, 0, 30)) {
		t.Fatalf("renewal period %s .. %s, want from %s", renewed.StartsAt, renewed.ExpiresAt, current.ExpiresAt)
	}
	old, _ := svc.Store().GetSubscription(ctx, current.ID)
	if old.Status != "cancelled" {
		t.Fatalf("old row = %s, want superseded (cancelled)", old.Status)
	}
	// The expiry worker sees one active row: the new period.
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider_partner_subscriptions WHERE partner_id = $1 AND status = 'active'`, renewed.PartnerID); n != 1 {
		t.Fatalf("active rows = %d", n)
	}
}

// --- 2. Government-verified approval ------------------------------------------

// Founder, 25 Oct ("nothing manual, instant approval"), on top of the
// d61bd603c review gate: the licence from DigiLocker, the vehicle RC
// pulled from DigiLocker and a selfie that matches the licence photo
// approve the captain with no person. Mutation guards: a selfie verified
// without the face check, a partner approved while the selfie is missing,
// the approval queued twice, an admin approve call approving again.
func TestPartnerApproval_DigiLockerAndSelfieAutomatic(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	svc.Store().WithRoleIntents(identityroles.NewOutbox("rider", "rider-service"))
	svc.SetDigiLockerClient(digilocker.NewMockClient())
	svc.SetFaceComparer(&MockFaceComparer{Similarity: 96}, 80)
	uid, p := newCaptainUser(t, svc, "Auto Captain")

	res, err := svc.CompleteAadhaarFlow(ctx, uid, p.ID, "code-auto", "state")
	if err != nil || !res.Verified {
		t.Fatalf("aadhaar: %+v %v", res, err)
	}
	docs, _ := svc.ListPartnerDocuments(ctx, uid)
	byType := map[string]store.PartnerDocument{}
	for _, d := range docs {
		byType[d.DocumentType] = d
	}
	dl, ok := byType[DocDrivingLicence]
	if !ok || dl.Status != "approved" || dl.Source != store.DocSourceDigiLocker || dl.VerifiedByActor == nil || *dl.VerifiedByActor != store.VerifiedByAuto || dl.PhotoMediaID == nil {
		t.Fatalf("licence after digilocker = %+v", dl)
	}
	if a, ok := byType[DocAadhaar]; ok && a.DocumentNumber != nil {
		t.Fatal("an Aadhaar number was stored")
	}
	ob, _ := svc.OnboardingStatusFor(ctx, uid)
	if ob.Status != OnboardingIncomplete || strings.Join(ob.Missing, ",") != DocSelfie+","+DocVehicleRC || len(ob.Pending) != 0 {
		t.Fatalf("onboarding after digilocker = %+v", ob)
	}

	// The vehicle's RC comes from DigiLocker: verified, vehicle approved
	// by the system.
	v, err := svc.AddVehicle(ctx, uid, p.ID, AddVehicleRequest{VehicleType: "auto", RegistrationNumber: "ka01zz" + uid.String()[:4]})
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "approved" || v.VerifiedByActor == nil || *v.VerifiedByActor != store.VerifiedByAuto {
		t.Fatalf("vehicle after digilocker rc = %+v", v)
	}
	vdocs, _ := svc.ListVehicleDocuments(ctx, uid, v.ID)
	if len(vdocs) != 1 || vdocs[0].DocumentType != DocVehicleRC || vdocs[0].Status != "approved" || vdocs[0].Source != store.DocSourceDigiLocker {
		t.Fatalf("vehicle docs = %+v", vdocs)
	}
	if pp, _ := svc.GetMyPartner(ctx, uid); pp.Status == "approved" {
		t.Fatal("partner approved without a selfie")
	}

	// The selfie matches the licence photo (96 >= 80): verified, and that
	// completes the set: the partner is approved with no person.
	media := uuid.New()
	selfie, err := svc.SubmitKYCDocument(ctx, uid, p.ID, SubmitKYCDocumentRequest{DocumentType: DocSelfie, FileURL: "media://" + media.String(), MediaID: &media})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Store().GetPartnerDocument(ctx, selfie.ID)
	if got.Status != "approved" || got.Source != store.DocSourceUpload || got.VerifiedByActor == nil || *got.VerifiedByActor != store.VerifiedByAuto || got.AutoCheckDetail == nil || !contains(*got.AutoCheckDetail, "face_compare") || !contains(*got.AutoCheckDetail, "reference "+SelfieRefDigiLockerPhoto) {
		t.Fatalf("selfie = %+v", got)
	}
	pp, _ := svc.GetMyPartner(ctx, uid)
	if pp.Status != "approved" || pp.KYCStatus != "approved" || pp.ApprovedAt == nil {
		t.Fatalf("partner = %+v", pp)
	}
	if ob, _ = svc.OnboardingStatusFor(ctx, uid); ob.Status != OnboardingApproved {
		t.Fatalf("onboarding = %+v", ob)
	}
	for _, action := range []string{"document.auto_verify", "vehicle.auto_verify", "partner.auto_approve"} {
		if n := countRows(t, svc, `SELECT COUNT(*) FROM rider_admin_audit_logs WHERE admin_user_id = $1 AND action = $2 AND new_value->>'actor' = 'system'`, store.SystemActorID, action); n == 0 {
			t.Fatalf("no %s audit row", action)
		}
	}
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider.identity_role_intents WHERE user_id = $1 AND op = 'grant' AND reason LIKE '%approved automatically%'`, uid.String()); n != 1 {
		t.Fatalf("role intents from the automatic approval = %d", n)
	}
	// One approved event through the outbox; a later evaluation or admin
	// approve call adds none.
	if _, err := svc.evaluatePartnerApproval(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApprovePartner(ctx, p.ID, uuid.New()); err == nil {
		t.Fatal("an approved partner was approved again")
	}
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider.outbox_events WHERE event_type = 'rider.partner.approved' AND payload->>'partner_id' = $1`, p.ID.String()); n != 1 {
		t.Fatalf("partner.approved events = %d, want 1", n)
	}
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider.outbox_events WHERE event_type = 'rider.partner.vehicle_approved' AND payload->>'partner_id' = $1`, p.ID.String()); n != 1 {
		t.Fatalf("vehicle_approved events = %d, want 1", n)
	}
}

// A borderline or failed face match, or none at all, sends the selfie to a
// person; the partner waits, named under review once; the person's
// verification then approves the partner at once. Mutation guards: the
// threshold comparison flipped, a missing verdict treated as a pass.
func TestPartnerApproval_BorderlineSelfieGoesToPerson(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	svc.SetDigiLockerClient(digilocker.NewMockClient())
	svc.SetFaceComparer(&MockFaceComparer{Similarity: 79}, 80)
	uid, p := newCaptainUser(t, svc, "Borderline Captain")
	if _, err := svc.CompleteAadhaarFlow(ctx, uid, p.ID, "code-borderline", "state"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddVehicle(ctx, uid, p.ID, AddVehicleRequest{VehicleType: "bike", RegistrationNumber: "ka02zz" + uid.String()[:4]}); err != nil {
		t.Fatal(err)
	}
	media := uuid.New()
	selfie, err := svc.SubmitKYCDocument(ctx, uid, p.ID, SubmitKYCDocumentRequest{DocumentType: DocSelfie, FileURL: "media://" + media.String(), MediaID: &media})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Store().GetPartnerDocument(ctx, selfie.ID)
	if got.Status != "pending" || got.VerifiedByActor != nil || got.AutoCheckDetail == nil || !contains(*got.AutoCheckDetail, "below threshold") {
		t.Fatalf("borderline selfie = %+v", got)
	}
	if pp, _ := svc.GetMyPartner(ctx, uid); pp.Status == "approved" {
		t.Fatal("partner approved with a borderline selfie")
	}
	ob, _ := svc.OnboardingStatusFor(ctx, uid)
	if ob.Status != OnboardingUnderReview || strings.Join(ob.Pending, ",") != DocSelfie || len(ob.Missing) != 0 {
		t.Fatalf("onboarding under review = %+v", ob)
	}
	// Queued for a person once per pending set.
	if _, err := svc.evaluatePartnerApproval(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider.outbox_events WHERE event_type = 'rider.partner.under_review' AND payload->>'partner_id' = $1`, p.ID.String()); n != 1 {
		t.Fatalf("under_review events = %d, want 1", n)
	}
	// A failed match and no verdict at all stay pending too.
	svc.SetFaceComparer(&MockFaceComparer{Similarity: 10}, 80)
	if _, err := svc.evaluatePartnerApproval(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	svc.SetFaceComparer(&MockFaceComparer{Err: ErrFaceCompareUnavailable}, 80)
	if _, err := svc.evaluatePartnerApproval(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = svc.Store().GetPartnerDocument(ctx, selfie.ID); got.Status != "pending" || !contains(*got.AutoCheckDetail, "unavailable") {
		t.Fatalf("selfie with media-service down = %+v", got)
	}
	svc.SetFaceComparer(nil, 80)
	if _, err := svc.evaluatePartnerApproval(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = svc.Store().GetPartnerDocument(ctx, selfie.ID); got.Status != "pending" {
		t.Fatalf("selfie without comparer = %s", got.Status)
	}
	// The person verifies the selfie: the partner is approved at once.
	admin := uuid.New()
	if _, err := svc.VerifyDocument(ctx, selfie.ID, admin); err != nil {
		t.Fatal(err)
	}
	if got, _ = svc.Store().GetPartnerDocument(ctx, selfie.ID); got.Status != "approved" || got.VerifiedByActor == nil || *got.VerifiedByActor != admin.String() {
		t.Fatalf("selfie after review = %+v", got)
	}
	if pp, _ := svc.GetMyPartner(ctx, uid); pp.Status != "approved" || pp.KYCStatus != "approved" {
		t.Fatalf("partner after review = %+v", pp)
	}
}

// noRCDigiLocker is a DigiLocker that holds the captain's licence but not
// the vehicle's RC, so the captain uploads the RC by hand.
type noRCDigiLocker struct{ *digilocker.MockClient }

func (c noRCDigiLocker) FetchDocuments(ctx context.Context, ref string, req digilocker.DocumentRequest) ([]digilocker.FetchedDocument, error) {
	req.VehicleRegistrations = nil
	return c.MockClient.FetchDocuments(ctx, ref, req)
}

// A hand-uploaded RC goes to a person even when everything else was
// verified automatically; the person's vehicle review then approves the
// partner at once. Mutation guard: a hand-uploaded RC approving its vehicle.
func TestPartnerApproval_HandUploadedRCGoesToPerson(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	svc.SetDigiLockerClient(noRCDigiLocker{digilocker.NewMockClient()})
	svc.SetFaceComparer(&MockFaceComparer{Similarity: 96}, 80)
	uid, p := newCaptainUser(t, svc, "Upload RC Captain")
	if _, err := svc.CompleteAadhaarFlow(ctx, uid, p.ID, "code-upload-rc", "state"); err != nil {
		t.Fatal(err)
	}
	media := uuid.New()
	if _, err := svc.SubmitKYCDocument(ctx, uid, p.ID, SubmitKYCDocumentRequest{DocumentType: DocSelfie, FileURL: "media://" + media.String(), MediaID: &media}); err != nil {
		t.Fatal(err)
	}
	v, err := svc.AddVehicle(ctx, uid, p.ID, AddVehicleRequest{VehicleType: "bike", RegistrationNumber: "ka03zz" + uid.String()[:4]})
	if err != nil {
		t.Fatal(err)
	}
	rcImage := uuid.New()
	rc, err := svc.SubmitVehicleDocument(ctx, uid, v.ID, SubmitVehicleDocumentRequest{DocumentType: DocVehicleRC, FileURL: "media://" + rcImage.String(), MediaID: &rcImage})
	if err != nil {
		t.Fatal(err)
	}
	if rc.Status != "pending" || rc.Source != store.DocSourceUpload || rc.VerifiedByActor != nil {
		t.Fatalf("hand-uploaded RC = %+v", rc)
	}
	if got, _ := svc.Store().GetVehicle(ctx, v.ID); got.Status != "pending" || got.VerifiedByActor != nil {
		t.Fatalf("vehicle with a hand-uploaded RC = %+v", got)
	}
	if pp, _ := svc.GetMyPartner(ctx, uid); pp.Status == "approved" {
		t.Fatal("partner approved on a hand-uploaded RC")
	}
	ob, _ := svc.OnboardingStatusFor(ctx, uid)
	if ob.Status != OnboardingUnderReview || strings.Join(ob.Pending, ",") != DocVehicleRC || len(ob.Missing) != 0 {
		t.Fatalf("onboarding = %+v", ob)
	}
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider_admin_audit_logs WHERE action = 'vehicle.auto_verify' AND entity_id = $1`, v.ID); n != 0 {
		t.Fatalf("vehicle auto verified on a hand-uploaded RC: %d audit rows", n)
	}
	// The person reviews the vehicle: the partner is approved at once.
	admin := uuid.New()
	if err := svc.VerifyVehicle(ctx, v.ID, admin); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Store().GetVehicle(ctx, v.ID); got.Status != "approved" || got.VerifiedByActor == nil || *got.VerifiedByActor != admin.String() {
		t.Fatalf("vehicle after review = %+v", got)
	}
	if pp, _ := svc.GetMyPartner(ctx, uid); pp.Status != "approved" {
		t.Fatalf("partner after vehicle review = %+v", pp)
	}
}

// recordingFaceComparer answers a fixed similarity and records what it was
// asked to compare.
type recordingFaceComparer struct {
	similarity float64
	calls      []FaceCompareRequest
}

func (c *recordingFaceComparer) CompareFaces(_ context.Context, req FaceCompareRequest) (*FaceCompareResult, error) {
	c.calls = append(c.calls, req)
	return &FaceCompareResult{Similarity: c.similarity, FaceCountSource: 1, FaceCountTarget: 1, Match: c.similarity >= DefaultSelfieMinSimilarity, Provider: "recording"}, nil
}

// A captain with no DigiLocker uploads the licence by hand. Once a person
// approves that licence, its image is trusted evidence: the selfie is
// compared with it and a match verifies the selfie with no person, the
// reference recorded (founder, 25 Oct: "nothing manual, instant
// approval"). Mutation guards: a pending or rejected licence used as the
// reference, an older approved licence used while the latest is not
// approved, the reference not recorded.
func TestPartnerApproval_ApprovedUploadedLicenceVerifiesSelfie(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	cmp := &recordingFaceComparer{similarity: 96}
	svc.SetFaceComparer(cmp, 80)
	uid, p := newCaptainUser(t, svc, "Upload Licence Captain")
	admin := uuid.New()

	// An older licence a person approved, then a newer one still pending:
	// the newer one wins, so there is nothing trusted to compare with.
	oldImage := uuid.New()
	oldDL, err := svc.SubmitKYCDocument(ctx, uid, p.ID, SubmitKYCDocumentRequest{DocumentType: DocDrivingLicence, FileURL: "media://" + oldImage.String(), MediaID: &oldImage})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyDocument(ctx, oldDL.ID, admin); err != nil {
		t.Fatal(err)
	}
	newImage := uuid.New()
	newDL, err := svc.SubmitKYCDocument(ctx, uid, p.ID, SubmitKYCDocumentRequest{DocumentType: DocDrivingLicence, FileURL: "media://" + newImage.String(), MediaID: &newImage})
	if err != nil {
		t.Fatal(err)
	}
	selfieImage := uuid.New()
	selfie, err := svc.SubmitKYCDocument(ctx, uid, p.ID, SubmitKYCDocumentRequest{DocumentType: DocSelfie, FileURL: "media://" + selfieImage.String(), MediaID: &selfieImage})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Store().GetPartnerDocument(ctx, selfie.ID)
	if len(cmp.calls) != 0 || got.Status != "pending" || got.VerifiedByActor != nil || got.AutoCheckDetail == nil || !contains(*got.AutoCheckDetail, "not approved") {
		t.Fatalf("selfie while the latest licence is pending = %+v, compare calls %+v", got, cmp.calls)
	}

	// The person rejects the newer licence: still nothing trusted.
	if _, err := svc.RejectDocument(ctx, newDL.ID, admin, "blurred"); err != nil {
		t.Fatal(err)
	}
	if got, _ = svc.Store().GetPartnerDocument(ctx, selfie.ID); len(cmp.calls) != 0 || got.Status != "pending" {
		t.Fatalf("selfie against a rejected licence = %+v, compare calls %+v", got, cmp.calls)
	}

	// A fresh licence, approved by a person: the selfie is compared with
	// that image and verified automatically, the reference recorded.
	freshImage := uuid.New()
	freshDL, err := svc.SubmitKYCDocument(ctx, uid, p.ID, SubmitKYCDocumentRequest{DocumentType: DocDrivingLicence, FileURL: "media://" + freshImage.String(), MediaID: &freshImage})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmp.calls) != 0 {
		t.Fatalf("compared against a pending licence: %+v", cmp.calls)
	}
	if _, err := svc.VerifyDocument(ctx, freshDL.ID, admin); err != nil {
		t.Fatal(err)
	}
	if len(cmp.calls) != 1 || cmp.calls[0].SourceMediaID != selfieImage || cmp.calls[0].TargetMediaID != freshImage || cmp.calls[0].RequesterUserID != uid {
		t.Fatalf("compare calls = %+v, want the selfie against the approved licence image", cmp.calls)
	}
	got, _ = svc.Store().GetPartnerDocument(ctx, selfie.ID)
	if got.Status != "approved" || got.VerifiedByActor == nil || *got.VerifiedByActor != store.VerifiedByAuto || got.AutoCheckDetail == nil ||
		!contains(*got.AutoCheckDetail, "face_compare") || !contains(*got.AutoCheckDetail, "reference "+SelfieRefUploadedLicence) {
		t.Fatalf("selfie after the licence review = %+v", got)
	}
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider_admin_audit_logs WHERE admin_user_id = $1 AND action = 'document.auto_verify' AND entity_id = $2 AND new_value->>'actor' = 'system' AND new_value->>'detail' LIKE '%reference `+SelfieRefUploadedLicence+`%'`, store.SystemActorID, selfie.ID); n != 1 {
		t.Fatalf("selfie auto_verify audit rows naming the uploaded licence = %d, want 1", n)
	}
	// The licence itself stays the person's decision.
	if d, _ := svc.Store().GetPartnerDocument(ctx, freshDL.ID); d.VerifiedByActor == nil || *d.VerifiedByActor != admin.String() {
		t.Fatalf("licence = %+v", d)
	}
}

// A borderline match against the approved uploaded licence still goes to
// a person, with the reference named for the reviewer.
func TestPartnerApproval_BorderlineSelfieAgainstUploadedLicenceGoesToPerson(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	svc.SetFaceComparer(&MockFaceComparer{Similarity: 79}, 80)
	uid, p := newCaptainUser(t, svc, "Borderline Upload Captain")
	licence := uuid.New()
	dl, err := svc.SubmitKYCDocument(ctx, uid, p.ID, SubmitKYCDocumentRequest{DocumentType: DocDrivingLicence, FileURL: "media://" + licence.String(), MediaID: &licence})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyDocument(ctx, dl.ID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	media := uuid.New()
	selfie, err := svc.SubmitKYCDocument(ctx, uid, p.ID, SubmitKYCDocumentRequest{DocumentType: DocSelfie, FileURL: "media://" + media.String(), MediaID: &media})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Store().GetPartnerDocument(ctx, selfie.ID)
	if got.Status != "pending" || got.VerifiedByActor != nil || got.AutoCheckDetail == nil ||
		!contains(*got.AutoCheckDetail, "below threshold") || !contains(*got.AutoCheckDetail, "reference "+SelfieRefUploadedLicence) {
		t.Fatalf("borderline selfie = %+v", got)
	}
}

func TestPartnerApproval_OfflineRequiresHuman(t *testing.T) {
	TestManualRiderOnboardingOnDatabase(t)
}

// Mutation guards: an uploaded (non-DigiLocker) document is never
// auto-verified, a selfie below the threshold or without a verdict stays
// pending, and the partner is not approved while a document is pending;
// the human fallback (admin verify) then completes the approval.
func TestPartnerApproval_UploadedDocumentWaitsForHuman(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	svc.SetFaceComparer(&MockFaceComparer{Similarity: 96}, 80)
	uid, p := newCaptainUser(t, svc, "Manual Captain")
	media := uuid.New()
	dl, err := svc.SubmitKYCDocument(ctx, uid, p.ID, SubmitKYCDocumentRequest{DocumentType: DocDrivingLicence, FileURL: "media://" + media.String(), MediaID: &media})
	if err != nil {
		t.Fatal(err)
	}
	selfie, err := svc.SubmitKYCDocument(ctx, uid, p.ID, SubmitKYCDocumentRequest{DocumentType: DocSelfie, FileURL: "media://" + media.String(), MediaID: &media})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{dl.ID, selfie.ID} {
		doc, err := svc.Store().GetPartnerDocument(ctx, id)
		if err != nil || doc.Status != "pending" || doc.VerifiedByActor != nil {
			t.Fatal("upload auto verified")
		}
	}
	onboarding, err := svc.OnboardingStatusFor(ctx, uid)
	if err != nil || containsAll(onboarding.Missing, DocAadhaar) {
		t.Fatal("offline onboarding required Aadhaar")
	}
	if _, err := svc.ApprovePartner(ctx, p.ID, uuid.New()); err == nil {
		t.Fatal("unreviewed account approved")
	}
	if _, err := svc.VerifyDocument(ctx, selfie.ID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	current, _ := svc.GetMyPartner(ctx, uid)
	if current.Status == "approved" {
		t.Fatal("selfie review auto approved account")
	}
}

func containsAll(list []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, l := range list {
			if l == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// --- 3. Refunds by rule -------------------------------------------------------

// paidOnlineRide is a completed upi ride captured through the signed event,
// then put back in progress (a customer who paid before the captain's
// cancellation).
func paidOnlineRide(t *testing.T, svc *Service, fake *fakePayments, cust uuid.UUID) (*store.Ride, *store.Partner, *store.RidePayment, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	ride, partner, pay := completedOnlineRide(t, svc, cust, "upi")
	out, err := svc.CreateRidePaymentIntent(ctx, cust, ride.ID, "upi")
	if err != nil {
		t.Fatal(err)
	}
	if a, err := svc.Store().ApplyRidePaymentEvent(ctx, capturedEvent(ride, out.IntentID, pay.AmountPaise)); err != nil || a.Decision.Outcome != payments.OutcomePaid {
		t.Fatalf("capture: %+v %v", a, err)
	}
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_rides SET status = 'in_progress' WHERE id = $1`, ride.ID); err != nil {
		t.Fatal(err)
	}
	r, _ := svc.Store().GetRide(ctx, ride.ID)
	pay, _ = svc.Store().GetRidePaymentByRide(ctx, ride.ID)
	return r, partner, pay, out.IntentID
}

func TestAutoRefund_CaptainCancelAfterOnlinePayment(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	fake := newFakePayments()
	svc.SetPayments(fake, "dev")
	cust := uuid.New()
	ride, partner, pay, intent := paidOnlineRide(t, svc, fake, cust)

	cancelled, err := svc.CancelRide(ctx, partner.UserID, ride.ID, "partner", CancelRideRequest{Reason: "breakdown", ExpectedRevision: ride.Revision})
	if err != nil {
		t.Fatalf("captain cancel: %v", err)
	}
	if cancelled.Status != "cancelled_by_partner" {
		t.Fatalf("status = %s", cancelled.Status)
	}
	refunds, _ := svc.Store().ListRefundsByRide(ctx, ride.ID)
	if len(refunds) != 1 {
		t.Fatalf("refund rows = %+v", refunds)
	}
	r := refunds[0]
	if r.RuleCode != payments.RuleCaptainCancel || r.RequestedBy != payments.SystemActorID || r.AmountPaise != pay.AmountPaise || r.Status != store.RefundAccepted || r.PaymentID == nil || *r.PaymentID != pay.ID || r.IntentID != intent {
		t.Fatalf("refund row = %+v", r)
	}
	if len(fake.refundCalls) != 1 || fake.refundCalls[0].IntentID != intent || fake.refundCalls[0].AmountMinor != pay.AmountPaise || fake.refundCalls[0].Key != payments.RefundKey(r.ID) {
		t.Fatalf("payments refund calls = %+v", fake.refundCalls)
	}
	// The payment row moves to refunded only from the signed event.
	if p, _ := svc.Store().GetRidePaymentByRide(ctx, ride.ID); p.Status != payments.StatusSucceeded {
		t.Fatalf("payment before the refund event = %s", p.Status)
	}
	ev := payments.Event{EventID: "evt-refund-" + uuid.NewString(), EventType: events.EventPaymentRefunded, IntentID: intent.String(), ReferenceID: ride.ID, AmountMinor: pay.AmountPaise, Status: payments.StatusRefunded}
	if a, err := svc.Store().ApplyRidePaymentEvent(ctx, ev); err != nil || a.Decision.Outcome != payments.OutcomeRefunded {
		t.Fatalf("refund event: %+v %v", a, err)
	}
	if p, _ := svc.Store().GetRidePaymentByRide(ctx, ride.ID); p.Status != payments.StatusRefunded || p.RefundedPaise != pay.AmountPaise {
		t.Fatalf("payment after refund event = %+v", p)
	}
	if rr, _ := svc.Store().GetRideRefund(ctx, r.ID); rr.Status != store.RefundRefunded {
		t.Fatalf("refund row after event = %s", rr.Status)
	}
}

// Mutation guards for rule (a): a cash ride and a customer cancellation
// never file a refund; a second cancellation attempt never files a second.
func TestAutoRefund_CaptainCancelNeverForCashOrCustomer(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	fake := newFakePayments()
	svc.SetPayments(fake, "dev")

	// Cash: the captain confirmed collection, then (a re-billed flow) the
	// ride is cancelled by the captain.
	cust := uuid.New()
	ride, partner, _ := completedOnlineRide(t, svc, cust, "cash")
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_ride_payments SET status = 'succeeded' WHERE ride_id = $1`, ride.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_rides SET status = 'in_progress' WHERE id = $1`, ride.ID); err != nil {
		t.Fatal(err)
	}
	r, _ := svc.Store().GetRide(ctx, ride.ID)
	if _, err := svc.CancelRide(ctx, partner.UserID, ride.ID, "partner", CancelRideRequest{Reason: "x", ExpectedRevision: r.Revision}); err != nil {
		t.Fatal(err)
	}
	if refunds, _ := svc.Store().ListRefundsByRide(ctx, ride.ID); len(refunds) != 0 || len(fake.refundCalls) != 0 {
		t.Fatalf("cash ride refunded: %+v %+v", refunds, fake.refundCalls)
	}

	// Online and paid, but the CUSTOMER cancels: no refund by rule.
	cust2 := uuid.New()
	ride2, _, _, _ := paidOnlineRide(t, svc, fake, cust2)
	if _, err := svc.CancelRide(ctx, cust2, ride2.ID, "customer", CancelRideRequest{Reason: "changed my mind", ExpectedRevision: ride2.Revision}); err != nil {
		t.Fatal(err)
	}
	if refunds, _ := svc.Store().ListRefundsByRide(ctx, ride2.ID); len(refunds) != 0 || len(fake.refundCalls) != 0 {
		t.Fatalf("customer cancel refunded: %+v", refunds)
	}
}

func TestAutoRefund_DuplicateCapture(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	fake := newFakePayments()
	svc.SetPayments(fake, "dev")
	cust := uuid.New()
	ride, _, pay, intent := paidOnlineRide(t, svc, fake, cust)

	second := uuid.New()
	a, err := svc.Store().ApplyRidePaymentEvent(ctx, capturedEvent(ride, second, pay.AmountPaise))
	if err != nil || a.Decision.Outcome != payments.OutcomeDuplicateCapture || a.RefundID == uuid.Nil {
		t.Fatalf("duplicate capture: %+v %v", a, err)
	}
	svc.OnRidePaymentApplied(ctx, a)
	r, err := svc.Store().GetRideRefund(ctx, a.RefundID)
	if err != nil || r.RuleCode != payments.RuleDuplicateCapture || r.IntentID != second || r.AmountPaise != pay.AmountPaise || r.RequestedBy != payments.SystemActorID || r.Status != store.RefundAccepted {
		t.Fatalf("duplicate refund row = %+v %v", r, err)
	}
	if len(fake.refundCalls) != 1 || fake.refundCalls[0].IntentID != second || fake.refundCalls[0].AmountMinor != pay.AmountPaise {
		t.Fatalf("refund calls = %+v", fake.refundCalls)
	}
	// Reconciliation is marked; the paid row is untouched.
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider_payment_reconciliation WHERE ride_id = $1 AND canonical_status = 'duplicate_capture'`, ride.ID); n != 1 {
		t.Fatalf("duplicate reconciliation rows = %d", n)
	}
	if p, _ := svc.Store().GetRidePaymentByRide(ctx, ride.ID); p.Status != payments.StatusSucceeded || p.RefundedPaise != 0 || *p.IntentID != intent {
		t.Fatalf("paid row after duplicate = %+v", p)
	}
	// Mutation guard: the same duplicate re-delivered files no second refund
	// and sends no second command.
	again, _ := svc.Store().ApplyRidePaymentEvent(ctx, capturedEvent(ride, second, pay.AmountPaise))
	svc.OnRidePaymentApplied(ctx, again)
	if again.RefundID != a.RefundID || len(fake.refundCalls) != 1 {
		t.Fatalf("re-delivered duplicate: refund=%s calls=%d", again.RefundID, len(fake.refundCalls))
	}
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider_ride_refunds WHERE ride_id = $1`, ride.ID); n != 1 {
		t.Fatalf("refund rows = %d", n)
	}
	// Mutation guard: a capture with a different amount on another intent
	// is a mismatch, not a duplicate — nothing refunded.
	wrong, _ := svc.Store().ApplyRidePaymentEvent(ctx, capturedEvent(ride, uuid.New(), 100))
	if wrong.Decision.Outcome != payments.OutcomeMismatch || wrong.RefundID != uuid.Nil {
		t.Fatalf("wrong-amount second capture: %+v", wrong)
	}
	// The refund of the second intent settles the rule row and leaves the
	// ride's payment alone.
	ev := payments.Event{EventID: "evt-dup-refund-" + uuid.NewString(), EventType: events.EventPaymentRefunded, IntentID: second.String(), ReferenceID: ride.ID, AmountMinor: pay.AmountPaise, Status: payments.StatusRefunded}
	if a, err := svc.Store().ApplyRidePaymentEvent(ctx, ev); err != nil || a.Decision.Outcome != payments.OutcomeDuplicateRefunded {
		t.Fatalf("duplicate refunded: %+v %v", a, err)
	}
	if rr, _ := svc.Store().GetRideRefund(ctx, a.RefundID); rr.Status != store.RefundRefunded {
		t.Fatalf("rule row after refund event = %s", rr.Status)
	}
	if p, _ := svc.Store().GetRidePaymentByRide(ctx, ride.ID); p.Status != payments.StatusSucceeded || p.RefundedPaise != 0 {
		t.Fatalf("paid row after duplicate refund = %+v", p)
	}
}

func TestAutoRefund_CancellationFeePaidOnline(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	fake := newFakePayments()
	svc.SetPayments(fake, "dev")
	cust := uuid.New()
	q := mustEstimate(t, svc, fridayNoonIST, &cust, "auto", "")
	partner, seedRide := makeApprovedPartnerWithVehicle(t, svc)
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_rides SET status = 'expired' WHERE id = $1`, seedRide); err != nil {
		t.Fatal(err)
	}
	ride, err := bookQuote(t, svc, cust, q.QuoteID, "auto", "cash")
	if err != nil {
		t.Fatal(err)
	}
	// The estimate pinned the service clock to the fare window; the
	// cancellation stamps cancelled_at with the database clock, so the
	// facts below are all on real time.
	svc.SetClock(func() time.Time { return time.Now().UTC() })
	// Assigned 10 minutes ago: the customer's cancellation is outside the
	// 120 s free window and charges the fee.
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_rides SET status = 'partner_assigned', partner_id = $2, assigned_at = $3 WHERE id = $1`,
		ride.ID, partner.ID, time.Now().UTC().Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	r, _ := svc.Store().GetRide(ctx, ride.ID)
	cancelled, err := svc.CancelRide(ctx, cust, ride.ID, "customer", CancelRideRequest{Reason: "late", ExpectedRevision: r.Revision})
	if err != nil || cancelled.Status != "cancelled_by_customer" {
		t.Fatalf("customer cancel: %+v %v", cancelled, err)
	}
	facts, _ := svc.Store().GetRideCancellationFacts(ctx, ride.ID)
	if facts.CancellationFeePaise <= 0 || facts.CancelledByKind != "customer" {
		t.Fatalf("cancellation facts = %+v", facts)
	}
	fee := facts.CancellationFeePaise
	o, err := svc.Store().GetOutstandingByRide(ctx, ride.ID)
	if err != nil || o.Status != store.OutstandingPending || o.AmountPaise != fee {
		t.Fatalf("outstanding = %+v %v", o, err)
	}
	// Paid directly through its own intent and the signed capture.
	intent, err := svc.CreateOutstandingPaymentIntent(ctx, cust, o.ID, "upi")
	if err != nil {
		t.Fatal(err)
	}
	a, err := svc.Store().ApplyRidePaymentEvent(ctx, payments.Event{EventID: "evt-fee-" + uuid.NewString(), EventType: events.EventPaymentSucceeded, IntentID: intent.IntentID.String(),
		ReferenceID: o.ID, PayerID: cust, AmountMinor: fee, Currency: "INR", Status: "succeeded"})
	if err != nil || a.Decision.Outcome != payments.OutcomeSettled {
		t.Fatalf("fee capture: %+v %v", a, err)
	}
	svc.OnRidePaymentApplied(ctx, a)
	// Mutation guard: a fee owed (customer cancelled after the window) is
	// not refunded.
	if n := countRows(t, svc, `SELECT COUNT(*) FROM rider_ride_refunds WHERE outstanding_id = $1`, o.ID); n != 0 || len(fake.refundCalls) != 0 {
		t.Fatalf("owed fee refunded: rows=%d calls=%d", n, len(fake.refundCalls))
	}
	// The cancellation turns out inside the free window (the assignment
	// time is corrected): the fee goes back by rule.
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_rides SET assigned_at = cancelled_at - INTERVAL '30 seconds' WHERE id = $1`, ride.ID); err != nil {
		t.Fatal(err)
	}
	refund, err := svc.EvaluateCancellationFeeRefund(ctx, o.ID)
	if err != nil || refund == nil {
		t.Fatalf("fee refund: %+v %v", refund, err)
	}
	if refund.RuleCode != payments.RuleCancellationFee || refund.OutstandingID == nil || *refund.OutstandingID != o.ID || refund.PaymentID != nil ||
		refund.IntentID != intent.IntentID || refund.AmountPaise != fee || refund.RequestedBy != payments.SystemActorID || refund.Status != store.RefundAccepted {
		t.Fatalf("fee refund row = %+v", refund)
	}
	if len(fake.refundCalls) != 1 || fake.refundCalls[0].IntentID != intent.IntentID || fake.refundCalls[0].AmountMinor != fee {
		t.Fatalf("refund calls = %+v", fake.refundCalls)
	}
	// Re-evaluation files nothing more.
	if again, err := svc.EvaluateCancellationFeeRefund(ctx, o.ID); err != nil || again != nil || len(fake.refundCalls) != 1 {
		t.Fatalf("re-evaluation: %+v %v calls=%d", again, err, len(fake.refundCalls))
	}
	// The signed refund event marks the fee refunded.
	ev := payments.Event{EventID: "evt-fee-refund-" + uuid.NewString(), EventType: events.EventPaymentRefunded, IntentID: intent.IntentID.String(), ReferenceID: o.ID, AmountMinor: fee, Status: payments.StatusRefunded}
	if a, err := svc.Store().ApplyRidePaymentEvent(ctx, ev); err != nil || a.Decision.Outcome != payments.OutcomeOutstandingRefunded {
		t.Fatalf("fee refund event: %+v %v", a, err)
	}
	if o2, _ := svc.Store().GetOutstanding(ctx, o.ID); o2.Status != store.OutstandingRefunded {
		t.Fatalf("outstanding after refund = %s", o2.Status)
	}
	if rr, _ := svc.Store().GetRideRefund(ctx, refund.ID); rr.Status != store.RefundRefunded {
		t.Fatalf("fee refund row after event = %s", rr.Status)
	}
}
