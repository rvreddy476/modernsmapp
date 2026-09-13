package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// seedDraftRestaurant creates a restaurant through the partner path, so it
// also pins that new restaurants start in DRAFT.
func seedDraftRestaurant(t *testing.T, s *Store) (ownerID, restaurantID uuid.UUID) {
	t.Helper()
	ownerID = uuid.New()
	r, err := s.CreatePartnerRestaurant(context.Background(), ownerID, PartnerRestaurantInput{
		Name: "Onboarding " + ownerID.String()[:8], Slug: "onboarding-" + ownerID.String(),
		AddressLine1: "1 Test Lane", City: "Bengaluru",
	})
	if err != nil {
		t.Fatalf("create restaurant: %v", err)
	}
	return ownerID, r.ID
}

func testSealedCompliance() ComplianceRecord {
	return ComplianceRecord{
		TaxCategory: "RESTAURANT_STANDALONE", LegalName: "Test Kitchens LLP",
		PANSealed: []byte("opaque-sealed-blob"), PANKeyVersion: 1, PANLookup: "lookup-hash",
		PANMasked: "****000Z", PANHolderType: "INDIVIDUAL",
	}
}

func testSealedPayout() PayoutAccountRecord {
	return PayoutAccountRecord{
		HolderName: "Test Holder", AccountSealed: []byte("opaque-sealed-account"), KeyVersion: 1,
		AccountLookup: "account-lookup-hash", AccountLast4: "6789", IFSC: "HDFC0000053",
		VerificationStatus: "NOT_VERIFIED", VerificationReason: "verification_pending_ops",
	}
}

func testLocation() onboarding.ValidatedLocation {
	return onboarding.ValidatedLocation{Latitude: 12.9716, Longitude: 77.5946, AddressLine1: "1 Test Lane", City: "Bengaluru", GooglePlaceID: "ChIJtestplace", DeliveryRadiusKM: 5}
}

func testFSSAI(expires time.Time) onboarding.ValidatedFSSAI {
	return onboarding.ValidatedFSSAI{LicenceNumber: "10099999000000", ExpiresOn: expires, MediaID: uuid.New()}
}

func synthAadhaarShapedNumber(t *testing.T) string {
	t.Helper()
	for c := '0'; c <= '9'; c++ {
		if n := "34567890123" + string(c); kyc.LooksLikeAadhaar(n) {
			return n
		}
	}
	t.Fatalf("no synthetic check digit")
	return ""
}

func fieldCodeOf(err error) string {
	var fe *onboarding.FieldError
	if errors.As(err, &fe) {
		return fe.Code
	}
	return ""
}

func TestCreatePartnerRestaurantStartsDraft(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ownerID, restaurantID := seedDraftRestaurant(t, s)
	r, err := s.GetPartnerRestaurant(context.Background(), ownerID, restaurantID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "DRAFT" {
		t.Fatalf("status = %s, want DRAFT until submit", r.Status)
	}
}

func TestOnboardingWritesAreOwnerOnly(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	_, restaurantID := seedDraftRestaurant(t, s)
	stranger := uuid.New()

	checks := map[string]func() error{
		"compliance": func() error {
			_, err := s.SetRestaurantCompliance(ctx, stranger, restaurantID, testSealedCompliance())
			return err
		},
		"location": func() error {
			_, err := s.SetRestaurantLocation(ctx, stranger, restaurantID, testLocation())
			return err
		},
		"hours": func() error {
			_, err := s.ReplaceOperatingHours(ctx, stranger, restaurantID, []OperatingHoursInput{{DayOfWeek: dayPtr(1), OpensAt: "10:00", ClosesAt: "22:00"}})
			return err
		},
		"accepting": func() error { _, err := s.SetRestaurantAccepting(ctx, stranger, restaurantID, false); return err },
		"fssai": func() error {
			_, err := s.SubmitRestaurantFSSAI(ctx, stranger, restaurantID, testFSSAI(time.Now().AddDate(1, 0, 0)))
			return err
		},
		"submit": func() error { _, err := s.SubmitRestaurantForReview(ctx, stranger, restaurantID); return err },
		"payout put": func() error {
			_, err := s.UpsertRestaurantPayoutAccount(ctx, stranger, restaurantID, testSealedPayout())
			return err
		},
		"payout get": func() error { _, err := s.GetRestaurantPayoutAccount(ctx, stranger, restaurantID); return err },
		"generic doc": func() error {
			_, err := s.AddRestaurantDocument(ctx, stranger, restaurantID, map[string]any{"document_type": "GST_CERTIFICATE"})
			return err
		},
		"owner restaurant": func() error { _, err := s.GetPartnerRestaurant(ctx, stranger, restaurantID); return err },
	}
	for name, fn := range checks {
		if err := fn(); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("%s by a stranger: err = %v, want pgx.ErrNoRows", name, err)
		}
	}

	var touched bool
	if err := s.db.QueryRow(ctx, `
		SELECT compliance_submitted_at IS NOT NULL OR latitude IS NOT NULL OR fssai_licence_number IS NOT NULL
			OR EXISTS (SELECT 1 FROM food.restaurant_operating_hours WHERE restaurant_id = $1)
			OR EXISTS (SELECT 1 FROM food.restaurant_documents WHERE restaurant_id = $1)
			OR EXISTS (SELECT 1 FROM food.payout_accounts WHERE owner_id = $1)
		FROM food.restaurants WHERE id = $1`, restaurantID).Scan(&touched); err != nil {
		t.Fatal(err)
	}
	if touched {
		t.Fatalf("a stranger's calls wrote to the restaurant")
	}
}

func TestSetRestaurantComplianceStoresSealedOnly(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, restaurantID := seedDraftRestaurant(t, s)

	unsealed := testSealedCompliance()
	unsealed.PANSealed = nil
	if _, err := s.SetRestaurantCompliance(ctx, ownerID, restaurantID, unsealed); err == nil {
		t.Fatalf("a record without a sealed PAN must be refused")
	}

	rec := testSealedCompliance()
	gstin, state := "29ZZZPZ0000Z1Z5", "29"
	rec.GSTIN, rec.GSTINStateCode = &gstin, &state
	got, err := s.SetRestaurantCompliance(ctx, ownerID, restaurantID, rec)
	if err != nil {
		t.Fatalf("compliance: %v", err)
	}
	if got.PANMasked != "****000Z" || got.TaxCategory != "RESTAURANT_STANDALONE" || got.GSTIN == nil || *got.GSTIN != gstin || got.ComplianceSubmittedAt == "" {
		t.Fatalf("returned = %+v", got)
	}

	bad := testSealedCompliance()
	bad.TaxCategory = "PLATFORM_FEE"
	if _, err := s.SetRestaurantCompliance(ctx, ownerID, restaurantID, bad); err == nil {
		t.Fatalf("the tax_category CHECK must refuse a non-restaurant category")
	}
}

func TestSetRestaurantLocationKeepsExactlyOneActiveArea(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, restaurantID := seedDraftRestaurant(t, s)

	loc := testLocation()
	if _, err := s.SetRestaurantLocation(ctx, ownerID, restaurantID, loc); err != nil {
		t.Fatalf("first: %v", err)
	}
	loc.DeliveryRadiusKM = 7.5
	loc.Latitude = 12.95
	got, err := s.SetRestaurantLocation(ctx, ownerID, restaurantID, loc)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if got.DeliveryRadiusKM != 7.5 || got.GooglePlaceID != "ChIJtestplace" {
		t.Fatalf("returned = %+v", got)
	}
	var active int
	var radius, clat, clng, rlat, rlng float64
	if err := s.db.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM food.restaurant_service_areas WHERE restaurant_id = $1 AND is_active),
			a.radius_km::float8, a.center_latitude::float8, a.center_longitude::float8, r.latitude::float8, r.longitude::float8
		FROM food.restaurants r
		JOIN food.restaurant_service_areas a ON a.restaurant_id = r.id AND a.is_active
		WHERE r.id = $1`, restaurantID).Scan(&active, &radius, &clat, &clng, &rlat, &rlng); err != nil {
		t.Fatal(err)
	}
	if active != 1 || radius != 7.5 || clat != rlat || clng != rlng || rlat != 12.95 {
		t.Fatalf("active=%d radius=%v centre=(%v,%v) restaurant=(%v,%v)", active, radius, clat, clng, rlat, rlng)
	}
}

func TestReplaceOperatingHoursReplacesTheSet(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	s.WithOrderingConfig(OrderingConfig{Location: time.UTC, Now: func() time.Time { return time.Date(2026, 9, 19, 1, 0, 0, 0, time.UTC) }})
	ctx := context.Background()
	ownerID, restaurantID := seedDraftRestaurant(t, s)

	if _, err := s.ReplaceOperatingHours(ctx, ownerID, restaurantID, []OperatingHoursInput{
		{DayOfWeek: dayPtr(1), OpensAt: "10:00", ClosesAt: "22:00"}, {DayOfWeek: dayPtr(2), OpensAt: "10:00", ClosesAt: "22:00"},
		{DayOfWeek: dayPtr(3), OpensAt: "10:00", ClosesAt: "22:00"},
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	got, err := s.ReplaceOperatingHours(ctx, ownerID, restaurantID, []OperatingHoursInput{
		{DayOfWeek: dayPtr(5), OpensAt: "18:00", ClosesAt: "02:00"}, {DayOfWeek: dayPtr(0), IsClosed: true},
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !got.IsOpenNow || len(got.Windows) != 2 {
		t.Fatalf("view = %+v (Saturday 01:00 is inside Friday's overnight window)", got)
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.restaurant_operating_hours WHERE restaurant_id = $1`, restaurantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rows = %d, want the replaced set of 2", n)
	}
	if _, err := s.ReplaceOperatingHours(ctx, ownerID, restaurantID, []OperatingHoursInput{{DayOfWeek: dayPtr(9), OpensAt: "10:00", ClosesAt: "22:00"}}); fieldCodeOf(err) != onboarding.CodeOperatingHoursDayInvalid {
		t.Fatalf("invalid day: %v", err)
	}
}

func seedMenuItem(t *testing.T, s *Store, restaurantID uuid.UUID, available bool) {
	t.Helper()
	ctx := context.Background()
	var categoryID uuid.UUID
	if err := s.db.QueryRow(ctx, `INSERT INTO food.menu_categories (restaurant_id, name, sort_order) VALUES ($1, 'Mains', 1) RETURNING id`, restaurantID).Scan(&categoryID); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO food.menu_items (restaurant_id, category_id, name, base_price, food_type, preparation_minutes, is_available, is_active, tax_percentage)
		VALUES ($1, $2, 'Test Dosa', 120, 'VEG', 10, $3, TRUE, 5)`, restaurantID, categoryID, available); err != nil {
		t.Fatalf("seed item: %v", err)
	}
}

func missingOf(t *testing.T, err error) []string {
	t.Helper()
	var nr *onboarding.NotReadyError
	if !errors.As(err, &nr) {
		t.Fatalf("err = %v, want NotReadyError", err)
	}
	return nr.Missing
}

func TestSubmitRestaurantForReviewReadiness(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, restaurantID := seedDraftRestaurant(t, s)

	_, err := s.SubmitRestaurantForReview(ctx, ownerID, restaurantID)
	if got := strings.Join(missingOf(t, err), ","); got != "location,operating_hours,compliance,fssai_document,payout_account,menu_item" {
		t.Fatalf("missing = %s", got)
	}

	steps := []struct {
		name string
		do   func()
	}{
		{"location", func() {
			if _, err := s.SetRestaurantLocation(ctx, ownerID, restaurantID, testLocation()); err != nil {
				t.Fatal(err)
			}
		}},
		{"operating_hours", func() {
			if _, err := s.ReplaceOperatingHours(ctx, ownerID, restaurantID, []OperatingHoursInput{{DayOfWeek: dayPtr(1), OpensAt: "10:00", ClosesAt: "22:00"}}); err != nil {
				t.Fatal(err)
			}
		}},
		{"compliance", func() {
			if _, err := s.SetRestaurantCompliance(ctx, ownerID, restaurantID, testSealedCompliance()); err != nil {
				t.Fatal(err)
			}
		}},
		{"fssai_document", func() {
			if _, err := s.SubmitRestaurantFSSAI(ctx, ownerID, restaurantID, testFSSAI(time.Now().AddDate(1, 0, 0))); err != nil {
				t.Fatal(err)
			}
		}},
		{"payout_account", func() {
			if _, err := s.UpsertRestaurantPayoutAccount(ctx, ownerID, restaurantID, testSealedPayout()); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for i, step := range steps {
		step.do()
		_, err := s.SubmitRestaurantForReview(ctx, ownerID, restaurantID)
		missing := missingOf(t, err)
		for _, m := range missing {
			for _, done := range steps[:i+1] {
				if m == done.name {
					t.Fatalf("after %s, %s is still reported missing: %v", step.name, m, missing)
				}
			}
		}
	}

	// An unavailable item does not count.
	seedMenuItem(t, s, restaurantID, false)
	if got := missingOf(t, func() error { _, err := s.SubmitRestaurantForReview(ctx, ownerID, restaurantID); return err }()); len(got) != 1 || got[0] != "menu_item" {
		t.Fatalf("missing = %v, want only menu_item", got)
	}
	seedMenuItem(t, s, restaurantID, true)

	sub, err := s.SubmitRestaurantForReview(ctx, ownerID, restaurantID)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if sub.Status != "PENDING_REVIEW" || sub.Missing == nil || len(sub.Missing) != 0 {
		t.Fatalf("submission = %+v", sub)
	}
	var partnerStatus string
	if err := s.db.QueryRow(ctx, `SELECT p.status::text FROM food.restaurant_partners p JOIN food.restaurants r ON r.partner_id = p.id WHERE r.id = $1`, restaurantID).Scan(&partnerStatus); err != nil {
		t.Fatal(err)
	}
	if partnerStatus != "PENDING_REVIEW" {
		t.Fatalf("partner status = %s", partnerStatus)
	}
	if _, err := s.SubmitRestaurantForReview(ctx, ownerID, restaurantID); !errors.Is(err, ErrRestaurantNotDraft) {
		t.Fatalf("second submit: %v, want ErrRestaurantNotDraft", err)
	}
}

func TestSubmitIgnoresRejectedFSSAI(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, restaurantID := seedDraftRestaurant(t, s)
	doc, err := s.SubmitRestaurantFSSAI(ctx, ownerID, restaurantID, testFSSAI(time.Now().AddDate(1, 0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdminDecideRestaurantDocument(ctx, uuid.New(), restaurantID, doc.Document.ID, "REJECTED", "unreadable"); err != nil {
		t.Fatal(err)
	}
	_, err = s.SubmitRestaurantForReview(ctx, ownerID, restaurantID)
	found := false
	for _, m := range missingOf(t, err) {
		found = found || m == "fssai_document"
	}
	if !found {
		t.Fatalf("a rejected FSSAI document must not satisfy the fssai step")
	}
}

func insertFSSAIDoc(t *testing.T, s *Store, restaurantID uuid.UUID, docType, status string, expires time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := s.db.QueryRow(context.Background(), `
		INSERT INTO food.restaurant_documents (restaurant_id, document_type, document_number, status, expires_at)
		VALUES ($1, $2, '10099999000000', $3::food.document_status, $4) RETURNING id`,
		restaurantID, docType, status, expires).Scan(&id); err != nil {
		t.Fatalf("insert doc: %v", err)
	}
	return id
}

func TestAdminDecideRestaurantDocument(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, restaurantID := seedDraftRestaurant(t, s)
	_, otherRestaurant := seedDraftRestaurant(t, s)
	adminID := uuid.New()

	fs, err := s.SubmitRestaurantFSSAI(ctx, ownerID, restaurantID, testFSSAI(time.Now().AddDate(1, 0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdminDecideRestaurantDocument(ctx, adminID, otherRestaurant, fs.Document.ID, "APPROVED", ""); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("decide through the wrong restaurant: %v", err)
	}
	doc, err := s.AdminDecideRestaurantDocument(ctx, adminID, restaurantID, fs.Document.ID, "APPROVED", "")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if doc.Status != "APPROVED" || doc.VerifiedBy == nil || *doc.VerifiedBy != adminID || doc.VerifiedAt == nil {
		t.Fatalf("doc = %+v", doc)
	}
	if _, err := s.AdminDecideRestaurantDocument(ctx, adminID, restaurantID, fs.Document.ID, "MAYBE", ""); err == nil {
		t.Fatalf("an unknown decision must be refused")
	}

	expired := insertFSSAIDoc(t, s, restaurantID, "FSSAI", "PENDING", time.Now().Add(-time.Hour))
	if _, err := s.AdminDecideRestaurantDocument(ctx, adminID, restaurantID, expired, "APPROVED", ""); !errors.Is(err, ErrDocumentExpired) {
		t.Fatalf("approve expired: %v, want ErrDocumentExpired", err)
	}
	rejected, err := s.AdminDecideRestaurantDocument(ctx, adminID, restaurantID, expired, "REJECTED", "expired")
	if err != nil || rejected.Status != "REJECTED" || rejected.RejectionReason == nil {
		t.Fatalf("reject expired: %+v %v", rejected, err)
	}
}

func TestAdminApproveRestaurantRequiresApprovedUnexpiredFSSAI(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	admin := uuid.New()

	_, none := seedDraftRestaurant(t, s)
	if err := s.AdminApproveRestaurant(ctx, admin, none, true, ""); !errors.Is(err, ErrFSSAIRequired) {
		t.Fatalf("no document: %v", err)
	}

	_, expired := seedDraftRestaurant(t, s)
	insertFSSAIDoc(t, s, expired, "FSSAI", "APPROVED", time.Now().Add(-time.Hour))
	if err := s.AdminApproveRestaurant(ctx, admin, expired, true, ""); !errors.Is(err, ErrFSSAIRequired) {
		t.Fatalf("expired document: %v", err)
	}
	if err := s.AdminSetRestaurantStatus(ctx, admin, expired, "ACTIVE", ""); !errors.Is(err, ErrFSSAIRequired) {
		t.Fatalf("status route with expired document: %v", err)
	}

	_, loose := seedDraftRestaurant(t, s)
	insertFSSAIDoc(t, s, loose, "fssai_licence", "APPROVED", time.Now().AddDate(1, 0, 0))
	if err := s.AdminApproveRestaurant(ctx, admin, loose, true, ""); !errors.Is(err, ErrFSSAIRequired) {
		t.Fatalf("type must match FSSAI exactly: %v", err)
	}

	_, pending := seedDraftRestaurant(t, s)
	insertFSSAIDoc(t, s, pending, "FSSAI", "PENDING", time.Now().AddDate(1, 0, 0))
	if err := s.AdminApproveRestaurant(ctx, admin, pending, true, ""); !errors.Is(err, ErrFSSAIRequired) {
		t.Fatalf("pending document: %v", err)
	}

	_, good := seedDraftRestaurant(t, s)
	insertFSSAIDoc(t, s, good, "FSSAI", "APPROVED", time.Now().AddDate(1, 0, 0))
	if err := s.AdminApproveRestaurant(ctx, admin, good, true, ""); err != nil {
		t.Fatalf("valid document: %v", err)
	}
	if err := s.AdminApproveRestaurant(ctx, admin, none, false, "no licence"); err != nil {
		t.Fatalf("rejection needs no licence: %v", err)
	}
}

func TestSetRestaurantAcceptingRequiresLiveRestaurant(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, restaurantID := seedDraftRestaurant(t, s)

	if _, err := s.SetRestaurantAccepting(ctx, ownerID, restaurantID, true); !errors.Is(err, ErrRestaurantNotLive) {
		t.Fatalf("draft: %v", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE food.restaurants SET status = 'ACTIVE' WHERE id = $1`, restaurantID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRestaurantAccepting(ctx, ownerID, restaurantID, true); !errors.Is(err, ErrFSSAIRequired) {
		t.Fatalf("active without licence: %v", err)
	}
	insertFSSAIDoc(t, s, restaurantID, "FSSAI", "APPROVED", time.Now().AddDate(1, 0, 0))
	got, err := s.SetRestaurantAccepting(ctx, ownerID, restaurantID, true)
	if err != nil || !got.IsAcceptingOrders {
		t.Fatalf("accept: %+v %v", got, err)
	}
	got, err = s.SetRestaurantAccepting(ctx, ownerID, restaurantID, false)
	if err != nil || got.IsAcceptingOrders {
		t.Fatalf("pause: %+v %v", got, err)
	}
}

func TestPauseRestaurantsWithExpiredFSSAI(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()

	activate := func(id uuid.UUID) {
		if _, err := s.db.Exec(ctx, `UPDATE food.restaurants SET status = 'ACTIVE', is_open = TRUE, is_accepting_orders = TRUE WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	_, lapsed := seedDraftRestaurant(t, s)
	activate(lapsed)
	insertFSSAIDoc(t, s, lapsed, "FSSAI", "APPROVED", time.Now().Add(-time.Minute))

	_, renewed := seedDraftRestaurant(t, s)
	activate(renewed)
	insertFSSAIDoc(t, s, renewed, "FSSAI", "APPROVED", time.Now().AddDate(0, -1, 0))
	insertFSSAIDoc(t, s, renewed, "FSSAI", "APPROVED", time.Now().AddDate(1, 0, 0))

	_, legacy := seedDraftRestaurant(t, s)
	activate(legacy)

	paused := map[uuid.UUID]bool{}
	for i := 0; i < 20; i++ {
		ids, err := s.PauseRestaurantsWithExpiredFSSAI(ctx, 200)
		if err != nil {
			t.Fatalf("pause: %v", err)
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			paused[id] = true
		}
	}
	if !paused[lapsed] || paused[renewed] || paused[legacy] {
		t.Fatalf("paused lapsed=%v renewed=%v legacy=%v", paused[lapsed], paused[renewed], paused[legacy])
	}
	var accepting bool
	if err := s.db.QueryRow(ctx, `SELECT is_accepting_orders FROM food.restaurants WHERE id = $1`, lapsed).Scan(&accepting); err != nil {
		t.Fatal(err)
	}
	if accepting {
		t.Fatalf("lapsed restaurant still accepting orders")
	}
	again, err := s.PauseRestaurantsWithExpiredFSSAI(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range again {
		if id == lapsed {
			t.Fatalf("an already paused restaurant was reported again")
		}
	}
}

func TestRestaurantDocumentNumbersRefuseAadhaar(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, restaurantID := seedDraftRestaurant(t, s)
	synthetic := synthAadhaarShapedNumber(t)

	_, err := s.AddRestaurantDocument(ctx, ownerID, restaurantID, map[string]any{"document_type": "TRADE_LICENCE", "document_number": synthetic})
	if fieldCodeOf(err) != onboarding.CodeAadhaarNotAllowed {
		t.Fatalf("generic document: %v", err)
	}
	grouped := synthetic[0:4] + " " + synthetic[4:8] + " " + synthetic[8:12]
	_, err = s.AddRestaurantDocument(ctx, ownerID, restaurantID, map[string]any{"document_type": "TRADE_LICENCE", "document_number": "ref " + grouped})
	if fieldCodeOf(err) != onboarding.CodeAadhaarNotAllowed {
		t.Fatalf("grouped number: %v", err)
	}
	fs := testFSSAI(time.Now().AddDate(1, 0, 0))
	fs.LicenceNumber = synthetic
	if _, err := s.SubmitRestaurantFSSAI(ctx, ownerID, restaurantID, fs); fieldCodeOf(err) != onboarding.CodeAadhaarNotAllowed {
		t.Fatalf("fssai path at the store boundary: %v", err)
	}
	if _, err := s.AddRestaurantDocument(ctx, ownerID, restaurantID, map[string]any{"document_type": "fssai", "document_number": "10099999000000"}); fieldCodeOf(err) != onboarding.CodeFSSAIUseDedicatedRoute {
		t.Fatalf("generic FSSAI upload: %v", err)
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.restaurant_documents WHERE restaurant_id = $1`, restaurantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("refused documents were written: %d rows", n)
	}
	if _, err := s.AddRestaurantDocument(ctx, ownerID, restaurantID, map[string]any{"document_type": "TRADE_LICENCE", "document_number": "TL/2026/0001"}); err != nil {
		t.Fatalf("ordinary document: %v", err)
	}
}

func TestPayoutAccountUpsertAndMaskedRead(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, restaurantID := seedDraftRestaurant(t, s)

	if _, err := s.GetRestaurantPayoutAccount(ctx, ownerID, restaurantID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("absent account: %v", err)
	}
	unsealed := testSealedPayout()
	unsealed.AccountSealed = nil
	if _, err := s.UpsertRestaurantPayoutAccount(ctx, ownerID, restaurantID, unsealed); err == nil {
		t.Fatalf("an unsealed record must be refused")
	}
	long := testSealedPayout()
	long.AccountLast4 = "456789"
	if _, err := s.UpsertRestaurantPayoutAccount(ctx, ownerID, restaurantID, long); err == nil {
		t.Fatalf("more than four clear digits must be refused")
	}

	if _, err := s.UpsertRestaurantPayoutAccount(ctx, ownerID, restaurantID, testSealedPayout()); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second := testSealedPayout()
	second.AccountLast4 = "4321"
	second.HolderName = "Second Holder"
	got, err := s.UpsertRestaurantPayoutAccount(ctx, ownerID, restaurantID, second)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if got.AccountNumberMasked != "****4321" || got.HolderName != "Second Holder" || got.OwnerType != "RESTAURANT" || got.VerificationStatus != "NOT_VERIFIED" {
		t.Fatalf("returned = %+v", got)
	}
	read, err := s.GetRestaurantPayoutAccount(ctx, ownerID, restaurantID)
	if err != nil || read.AccountNumberMasked != "****4321" {
		t.Fatalf("read = %+v %v", read, err)
	}
	var rows int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.payout_accounts WHERE owner_type = 'RESTAURANT' AND owner_id = $1`, restaurantID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want one per owner", rows)
	}

	riderUser := uuid.New()
	if _, err := s.UpsertDeliveryPartnerPayoutAccount(ctx, riderUser, testSealedPayout()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("no delivery profile: %v", err)
	}
	if _, err := s.UpsertDeliveryPartner(ctx, riderUser, DeliveryPartnerInput{FullName: "Test Rider", Phone: "+919000000001"}); err != nil {
		t.Fatal(err)
	}
	rider, err := s.UpsertDeliveryPartnerPayoutAccount(ctx, riderUser, testSealedPayout())
	if err != nil || rider.OwnerType != "DELIVERY_PARTNER" || rider.AccountNumberMasked != "****6789" {
		t.Fatalf("rider = %+v %v", rider, err)
	}
	if r2, err := s.GetDeliveryPartnerPayoutAccount(ctx, riderUser); err != nil || r2.OwnerID != rider.OwnerID {
		t.Fatalf("rider read = %+v %v", r2, err)
	}
	if _, err := s.GetDeliveryPartnerPayoutAccount(ctx, uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("another user's read: %v", err)
	}
}
