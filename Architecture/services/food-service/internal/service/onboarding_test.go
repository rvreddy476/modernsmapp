package service

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/foodpii"
	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/payout"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

// Synthetic identifiers only.
const (
	svcTestPAN     = "ZZZPZ0000Z"
	svcTestAccount = "000123456789"
)

type onboardingFakeStore struct {
	Store
	complianceCalls []postgres.ComplianceRecord
	payoutCalls     []postgres.PayoutAccountRecord
	paused          [][]uuid.UUID
}

func (f *onboardingFakeStore) SetRestaurantCompliance(_ context.Context, _, restaurantID uuid.UUID, rec postgres.ComplianceRecord) (*postgres.RestaurantCompliance, error) {
	f.complianceCalls = append(f.complianceCalls, rec)
	return &postgres.RestaurantCompliance{RestaurantID: restaurantID, TaxCategory: rec.TaxCategory, LegalName: rec.LegalName,
		GSTIN: rec.GSTIN, GSTINStateCode: rec.GSTINStateCode, PANMasked: rec.PANMasked, PANHolderType: rec.PANHolderType}, nil
}

func (f *onboardingFakeStore) UpsertRestaurantPayoutAccount(_ context.Context, _, restaurantID uuid.UUID, rec postgres.PayoutAccountRecord) (*postgres.PayoutAccount, error) {
	f.payoutCalls = append(f.payoutCalls, rec)
	return &postgres.PayoutAccount{OwnerType: "RESTAURANT", OwnerID: restaurantID, AccountNumberMasked: "****" + rec.AccountLast4, VerificationStatus: rec.VerificationStatus}, nil
}

func (f *onboardingFakeStore) UpsertDeliveryPartnerPayoutAccount(_ context.Context, _ uuid.UUID, rec postgres.PayoutAccountRecord) (*postgres.PayoutAccount, error) {
	f.payoutCalls = append(f.payoutCalls, rec)
	return &postgres.PayoutAccount{OwnerType: "DELIVERY_PARTNER", AccountNumberMasked: "****" + rec.AccountLast4, VerificationStatus: rec.VerificationStatus}, nil
}

func (f *onboardingFakeStore) GetRestaurantPayoutAccount(context.Context, uuid.UUID, uuid.UUID) (*postgres.PayoutAccount, error) {
	return &postgres.PayoutAccount{}, nil
}

func (f *onboardingFakeStore) GetDeliveryPartnerPayoutAccount(context.Context, uuid.UUID) (*postgres.PayoutAccount, error) {
	return &postgres.PayoutAccount{}, nil
}

func (f *onboardingFakeStore) GetPartnerRestaurant(_ context.Context, ownerID, restaurantID uuid.UUID) (*postgres.PartnerRestaurant, error) {
	return &postgres.PartnerRestaurant{ID: restaurantID, OwnerUserID: ownerID}, nil
}

func (f *onboardingFakeStore) GetDeliveryPartner(_ context.Context, userID uuid.UUID) (*postgres.DeliveryPartner, error) {
	return &postgres.DeliveryPartner{ID: uuid.New(), UserID: userID}, nil
}

func (f *onboardingFakeStore) PauseRestaurantsWithExpiredFSSAI(context.Context, int) ([]uuid.UUID, error) {
	if len(f.paused) == 0 {
		return nil, nil
	}
	ids := f.paused[0]
	f.paused = f.paused[1:]
	return ids, nil
}

func svcTestCrypto(t *testing.T) *foodpii.Crypto {
	t.Helper()
	c, err := foodpii.New(context.Background(), []foodpii.VersionedKey{{Version: 1, Key: bytes.Repeat([]byte{9}, 32)}}, []byte("service-unit-test-salt-000000001"))
	if err != nil {
		t.Fatalf("crypto: %v", err)
	}
	return c
}

var svcTestNow = func() time.Time { return time.Date(2026, 9, 13, 12, 0, 0, 0, onboarding.IST) }

func svcTestGSTIN(t *testing.T) string {
	t.Helper()
	d, err := kyc.GSTINCheckDigit("29" + svcTestPAN + "1Z")
	if err != nil {
		t.Fatal(err)
	}
	return "29" + svcTestPAN + "1Z" + string(d)
}

// assertNoPlaintext walks every string and []byte field of v.
func assertNoPlaintext(t *testing.T, v any, secret string) {
	t.Helper()
	rv := reflect.ValueOf(v)
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		name := rv.Type().Field(i).Name
		switch {
		case f.Kind() == reflect.String && strings.Contains(f.String(), secret):
			t.Fatalf("field %s carries the plaintext", name)
		case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.Uint8 && bytes.Contains(f.Bytes(), []byte(secret)):
			t.Fatalf("field %s carries the plaintext", name)
		case f.Kind() == reflect.Pointer && !f.IsNil() && f.Elem().Kind() == reflect.String && strings.Contains(f.Elem().String(), secret) && name != "GSTIN":
			t.Fatalf("field %s carries the plaintext", name)
		}
	}
}

func TestPIIRoutesWithoutKeysNeverReachTheStore(t *testing.T) {
	st := &onboardingFakeStore{}
	svc := New(st).WithOnboardingClock(svcTestNow) // no WithPII: keys absent
	ctx := context.Background()
	owner, restaurant := uuid.New(), uuid.New()

	if _, err := svc.SetRestaurantCompliance(ctx, owner, restaurant, onboarding.ComplianceInput{TaxCategory: "RESTAURANT_STANDALONE", LegalName: "Test", PAN: svcTestPAN}); !errors.Is(err, ErrPIINotConfigured) {
		t.Fatalf("compliance: %v", err)
	}
	payoutIn := onboarding.PayoutAccountInput{HolderName: "Test", AccountNumber: svcTestAccount, IFSC: "HDFC0000053"}
	if _, err := svc.PutRestaurantPayoutAccount(ctx, owner, restaurant, payoutIn); !errors.Is(err, ErrPIINotConfigured) {
		t.Fatalf("restaurant payout: %v", err)
	}
	if _, err := svc.PutDeliveryPayoutAccount(ctx, owner, payoutIn); !errors.Is(err, ErrPIINotConfigured) {
		t.Fatalf("delivery payout: %v", err)
	}
	if _, err := svc.GetRestaurantPayoutAccount(ctx, owner, restaurant); !errors.Is(err, ErrPIINotConfigured) {
		t.Fatalf("restaurant payout get: %v", err)
	}
	if _, err := svc.GetDeliveryPayoutAccount(ctx, owner); !errors.Is(err, ErrPIINotConfigured) {
		t.Fatalf("delivery payout get: %v", err)
	}
	if len(st.complianceCalls) != 0 || len(st.payoutCalls) != 0 {
		t.Fatalf("store was called without keys: compliance=%d payout=%d", len(st.complianceCalls), len(st.payoutCalls))
	}
}

func TestSetRestaurantComplianceSealsPAN(t *testing.T) {
	st := &onboardingFakeStore{}
	crypto := svcTestCrypto(t)
	svc := New(st).WithPII(crypto).WithOnboardingClock(svcTestNow)
	ctx := context.Background()
	gstin := svcTestGSTIN(t)

	view, err := svc.SetRestaurantCompliance(ctx, uuid.New(), uuid.New(), onboarding.ComplianceInput{
		TaxCategory: "OUTDOOR_CATERING", LegalName: "Test Caterers", PAN: strings.ToLower(svcTestPAN), GSTIN: gstin,
	})
	if err != nil {
		t.Fatalf("compliance: %v", err)
	}
	if len(st.complianceCalls) != 1 {
		t.Fatalf("store calls = %d", len(st.complianceCalls))
	}
	rec := st.complianceCalls[0]
	assertNoPlaintext(t, rec, svcTestPAN)
	opened, err := crypto.OpenPAN(ctx, rec.PANSealed)
	if err != nil || opened != svcTestPAN {
		t.Fatalf("sealed PAN does not open to the normalised PAN (err %v)", err)
	}
	if rec.PANMasked != "****000Z" || rec.PANKeyVersion != 1 || rec.PANLookup == "" || rec.PANHolderType != "INDIVIDUAL" {
		t.Fatalf("record = masked %q version %d holder %q", rec.PANMasked, rec.PANKeyVersion, rec.PANHolderType)
	}
	if rec.GSTIN == nil || *rec.GSTIN != gstin || rec.GSTINStateCode == nil || *rec.GSTINStateCode != "29" {
		t.Fatalf("gstin not written normalised")
	}
	if view.GSTLiability != "SUPPLIER" || !view.GSTINRequired || view.PANMasked != "****000Z" {
		t.Fatalf("view = %+v", view)
	}
}

func TestComplianceValidationRefusesBeforeSealing(t *testing.T) {
	st := &onboardingFakeStore{}
	svc := New(st).WithPII(svcTestCrypto(t)).WithOnboardingClock(svcTestNow)
	_, err := svc.SetRestaurantCompliance(context.Background(), uuid.New(), uuid.New(), onboarding.ComplianceInput{
		TaxCategory: "RESTAURANT_SPECIFIED_PREMISES", LegalName: "Test", PAN: svcTestPAN, SpecifiedPremisesDeclaredAt: "2026-09-01",
	})
	var fe *onboarding.FieldError
	if !errors.As(err, &fe) || fe.Code != onboarding.CodeGSTINRequired {
		t.Fatalf("err = %v, want FOOD_GSTIN_REQUIRED", err)
	}
	if len(st.complianceCalls) != 0 {
		t.Fatalf("an invalid submission reached the store")
	}
}

func TestPayoutAccountIsSealedAndLeftUnverified(t *testing.T) {
	st := &onboardingFakeStore{}
	crypto := svcTestCrypto(t)
	svc := New(st).WithPII(crypto).WithOnboardingClock(svcTestNow) // default verifier
	ctx := context.Background()

	out, err := svc.PutRestaurantPayoutAccount(ctx, uuid.New(), uuid.New(), onboarding.PayoutAccountInput{HolderName: "Test Holder", AccountNumber: svcTestAccount, IFSC: "hdfc0000053"})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := svc.PutDeliveryPayoutAccount(ctx, uuid.New(), onboarding.PayoutAccountInput{HolderName: "Test Rider", AccountNumber: svcTestAccount, IFSC: "HDFC0000053"}); err != nil {
		t.Fatalf("delivery put: %v", err)
	}
	for _, rec := range st.payoutCalls {
		assertNoPlaintext(t, rec, svcTestAccount)
		assertNoPlaintext(t, rec, svcTestAccount[:8])
		opened, err := crypto.OpenAccountNumber(ctx, rec.AccountSealed)
		if err != nil || opened != svcTestAccount {
			t.Fatalf("sealed account does not open (err %v)", err)
		}
		if rec.AccountLast4 != "6789" || rec.IFSC != "HDFC0000053" || rec.AccountLookup == "" {
			t.Fatalf("record = last4 %q ifsc %q", rec.AccountLast4, rec.IFSC)
		}
		if rec.VerificationStatus != string(payout.StatusNotVerified) || rec.VerificationReason != payout.ReasonVerificationPendingOps || rec.VerifiedAt != nil {
			t.Fatalf("verification = %s / %s", rec.VerificationStatus, rec.VerificationReason)
		}
	}
	if out.AccountNumberMasked != "****6789" {
		t.Fatalf("masked = %q", out.AccountNumberMasked)
	}
}

type stubVerifier struct{ v payout.Verification }

func (s stubVerifier) Verify(context.Context, payout.BankAccountDetails) (payout.Verification, error) {
	return s.v, nil
}

func TestPayoutAccountRecordsAVerifiedResult(t *testing.T) {
	st := &onboardingFakeStore{}
	svc := New(st).WithPII(svcTestCrypto(t)).WithOnboardingClock(svcTestNow).
		WithBankVerifier(stubVerifier{payout.Verification{Status: payout.StatusVerified, VerifiedName: "TEST HOLDER"}})
	if _, err := svc.PutRestaurantPayoutAccount(context.Background(), uuid.New(), uuid.New(), onboarding.PayoutAccountInput{HolderName: "Test Holder", AccountNumber: svcTestAccount, IFSC: "HDFC0000053"}); err != nil {
		t.Fatal(err)
	}
	rec := st.payoutCalls[0]
	if rec.VerificationStatus != "VERIFIED" || rec.VerifiedName != "TEST HOLDER" || rec.VerifiedAt == nil {
		t.Fatalf("verified result not recorded: %s %q %v", rec.VerificationStatus, rec.VerifiedName, rec.VerifiedAt)
	}
}

type eventCapture struct{ events [][2]string }

func (c *eventCapture) Publish(_ context.Context, topic, eventType string, _ any) error {
	c.events = append(c.events, [2]string{topic, eventType})
	return nil
}

func TestPauseExpiredFSSAIRestaurantsEmitsOncePerRestaurant(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	st := &onboardingFakeStore{paused: [][]uuid.UUID{{a, b}}}
	cap := &eventCapture{}
	svc := New(st)
	svc.rtPublisher = cap
	n, err := svc.PauseExpiredFSSAIRestaurants(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("n = %d err = %v", n, err)
	}
	want := [][2]string{{"food.restaurant." + a.String(), "food.restaurant.fssai_expired"}, {"food.restaurant." + b.String(), "food.restaurant.fssai_expired"}}
	if !reflect.DeepEqual(cap.events, want) {
		t.Fatalf("events = %v, want %v", cap.events, want)
	}
}
