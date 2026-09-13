package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/food-service/internal/foodpii"
	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/payout"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/gst"
	"github.com/atpost/shared/pii"
	"github.com/google/uuid"
)

// Restaurant onboarding (Wave 1 B1) and payout accounts (B2).
//
// Identifiers are validated here, sealed here, and only the sealed form is
// handed to the store. Without FOOD_PII_KEYS the PII routes answer
// ErrPIINotConfigured before anything is validated, sealed or written.

// ErrPIINotConfigured maps to 503 PII_NOT_CONFIGURED.
var ErrPIINotConfigured = foodpii.ErrNotConfigured

// ErrBankVerificationUnavailable is returned when a configured verifier
// errors; the account is not saved half-verified.
var ErrBankVerificationUnavailable = errors.New("bank account verification is unavailable")

// EventRestaurantFSSAIExpired is emitted once per restaurant the expiry pass pauses.
const EventRestaurantFSSAIExpired = "food.restaurant.fssai_expired"

var onboardingRateTable = gst.DefaultRateTable()

// WithPII attaches the sealing configuration. nil keeps the PII routes at 503.
func (s *Service) WithPII(c *foodpii.Crypto) *Service {
	s.pii = c
	return s
}

// WithBankVerifier selects the payout-account verifier; nil keeps DisabledVerifier.
func (s *Service) WithBankVerifier(v payout.BankVerifier) *Service {
	if v != nil {
		s.bankVerifier = v
	}
	return s
}

// WithOnboardingClock pins "today" for onboarding date rules (tests).
func (s *Service) WithOnboardingClock(now func() time.Time) *Service {
	s.onboardingNow = now
	return s
}

func (s *Service) onboardingClock() time.Time {
	if s.onboardingNow != nil {
		return s.onboardingNow()
	}
	return time.Now()
}

func (s *Service) verifier() payout.BankVerifier {
	if s.bankVerifier == nil {
		return payout.DisabledVerifier{}
	}
	return s.bankVerifier
}

// ComplianceView is the compliance response: the stored, masked record plus
// who owes GST for the chosen category.
type ComplianceView struct {
	postgres.RestaurantCompliance
	GSTLiability  string `json:"gst_liability"`
	GSTINRequired bool   `json:"gstin_required"`
}

func (s *Service) SetRestaurantCompliance(ctx context.Context, ownerID, restaurantID uuid.UUID, in onboarding.ComplianceInput) (*ComplianceView, error) {
	if !s.pii.Configured() {
		return nil, ErrPIINotConfigured
	}
	v, err := onboarding.ValidateCompliance(onboardingRateTable, in, s.onboardingClock())
	if err != nil {
		return nil, err
	}
	sealed, err := s.pii.SealPAN(ctx, v.PAN.Normalized)
	if err != nil {
		return nil, err
	}
	rec := postgres.ComplianceRecord{
		TaxCategory:                 string(v.Category),
		LegalName:                   v.LegalName,
		PANSealed:                   sealed.Blob,
		PANKeyVersion:               sealed.KeyVersion,
		PANLookup:                   sealed.Lookup,
		PANMasked:                   pii.MaskPAN(v.PAN.Normalized),
		PANHolderType:               v.PAN.HolderType.String(),
		SpecifiedPremisesDeclaredAt: v.SpecifiedPremisesDeclaredAt,
	}
	if v.GSTIN != nil {
		gstin, state := v.GSTIN.Normalized, v.GSTIN.StateCode
		rec.GSTIN, rec.GSTINStateCode = &gstin, &state
	}
	out, err := s.store.SetRestaurantCompliance(ctx, ownerID, restaurantID, rec)
	if err != nil {
		return nil, err
	}
	return &ComplianceView{RestaurantCompliance: *out, GSTLiability: string(v.Liability), GSTINRequired: v.GSTINRequired}, nil
}

func (s *Service) SetRestaurantLocation(ctx context.Context, ownerID, restaurantID uuid.UUID, in onboarding.LocationInput) (*postgres.RestaurantLocation, error) {
	v, err := onboarding.ValidateLocation(in)
	if err != nil {
		return nil, err
	}
	return s.store.SetRestaurantLocation(ctx, ownerID, restaurantID, v)
}

func (s *Service) ReplaceOperatingHours(ctx context.Context, ownerID, restaurantID uuid.UUID, in []postgres.OperatingHoursInput) (*postgres.OperatingHours, error) {
	return s.store.ReplaceOperatingHours(ctx, ownerID, restaurantID, in)
}

func (s *Service) SetRestaurantAccepting(ctx context.Context, ownerID, restaurantID uuid.UUID, accepting bool) (*postgres.RestaurantAccepting, error) {
	return s.store.SetRestaurantAccepting(ctx, ownerID, restaurantID, accepting)
}

func (s *Service) SubmitRestaurantFSSAI(ctx context.Context, ownerID, restaurantID uuid.UUID, in onboarding.FSSAIInput) (*postgres.RestaurantFSSAI, error) {
	v, err := onboarding.ValidateFSSAI(in, s.onboardingClock())
	if err != nil {
		return nil, err
	}
	return s.store.SubmitRestaurantFSSAI(ctx, ownerID, restaurantID, v)
}

func (s *Service) SubmitRestaurantForReview(ctx context.Context, ownerID, restaurantID uuid.UUID) (*postgres.RestaurantSubmission, error) {
	return s.store.SubmitRestaurantForReview(ctx, ownerID, restaurantID)
}

func (s *Service) AdminDecideRestaurantDocument(ctx context.Context, adminID, restaurantID, documentID uuid.UUID, decision, reason string) (*postgres.RestaurantDocument, error) {
	d, err := onboarding.ValidateDocumentDecision(decision, reason)
	if err != nil {
		return nil, err
	}
	return s.store.AdminDecideRestaurantDocument(ctx, adminID, restaurantID, documentID, d, strings.TrimSpace(reason))
}

// ─── Payout accounts ────────────────────────────────────────────────────────

// sealPayoutAccount validates, seals and verifies. Callers have already
// confirmed the owner, so a paid verifier is never called for a stranger.
func (s *Service) sealPayoutAccount(ctx context.Context, in onboarding.PayoutAccountInput) (postgres.PayoutAccountRecord, error) {
	v, err := onboarding.ValidatePayoutAccount(in)
	if err != nil {
		return postgres.PayoutAccountRecord{}, err
	}
	sealed, err := s.pii.SealAccountNumber(ctx, v.AccountNumber)
	if err != nil {
		return postgres.PayoutAccountRecord{}, err
	}
	verification, err := s.verifier().Verify(ctx, payout.BankAccountDetails{HolderName: v.HolderName, AccountNumber: v.AccountNumber, IFSC: v.IFSC})
	if err != nil {
		slog.WarnContext(ctx, "food-service: bank verifier failed", "error", err)
		return postgres.PayoutAccountRecord{}, ErrBankVerificationUnavailable
	}
	rec := postgres.PayoutAccountRecord{
		HolderName:         v.HolderName,
		AccountSealed:      sealed.Blob,
		KeyVersion:         sealed.KeyVersion,
		AccountLookup:      sealed.Lookup,
		AccountLast4:       strings.TrimPrefix(pii.MaskAccountNumber(v.AccountNumber), "****"),
		IFSC:               v.IFSC,
		VerificationStatus: string(verification.Status),
		VerificationReason: verification.Reason,
		VerifiedName:       verification.VerifiedName,
	}
	if verification.Status == payout.StatusVerified {
		now := time.Now().UTC()
		rec.VerifiedAt = &now
	}
	return rec, nil
}

func (s *Service) PutRestaurantPayoutAccount(ctx context.Context, ownerID, restaurantID uuid.UUID, in onboarding.PayoutAccountInput) (*postgres.PayoutAccount, error) {
	if !s.pii.Configured() {
		return nil, ErrPIINotConfigured
	}
	if _, err := s.store.GetPartnerRestaurant(ctx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	rec, err := s.sealPayoutAccount(ctx, in)
	if err != nil {
		return nil, err
	}
	return s.store.UpsertRestaurantPayoutAccount(ctx, ownerID, restaurantID, rec)
}

func (s *Service) GetRestaurantPayoutAccount(ctx context.Context, ownerID, restaurantID uuid.UUID) (*postgres.PayoutAccount, error) {
	if !s.pii.Configured() {
		return nil, ErrPIINotConfigured
	}
	return s.store.GetRestaurantPayoutAccount(ctx, ownerID, restaurantID)
}

func (s *Service) PutDeliveryPayoutAccount(ctx context.Context, userID uuid.UUID, in onboarding.PayoutAccountInput) (*postgres.PayoutAccount, error) {
	if !s.pii.Configured() {
		return nil, ErrPIINotConfigured
	}
	if _, err := s.store.GetDeliveryPartner(ctx, userID); err != nil {
		return nil, err
	}
	rec, err := s.sealPayoutAccount(ctx, in)
	if err != nil {
		return nil, err
	}
	return s.store.UpsertDeliveryPartnerPayoutAccount(ctx, userID, rec)
}

func (s *Service) GetDeliveryPayoutAccount(ctx context.Context, userID uuid.UUID) (*postgres.PayoutAccount, error) {
	if !s.pii.Configured() {
		return nil, ErrPIINotConfigured
	}
	return s.store.GetDeliveryPartnerPayoutAccount(ctx, userID)
}

// ─── FSSAI expiry ───────────────────────────────────────────────────────────

const fssaiExpiryBatch = 100

// PauseExpiredFSSAIRestaurants stops orders at restaurants whose approved
// licence lapsed and emits food.restaurant.fssai_expired for each one it
// paused. The store returns only rows it flipped, so a restaurant is
// announced once, not every pass.
func (s *Service) PauseExpiredFSSAIRestaurants(ctx context.Context) (int, error) {
	total := 0
	for pass := 0; pass < 10; pass++ {
		ids, err := s.store.PauseRestaurantsWithExpiredFSSAI(ctx, fssaiExpiryBatch)
		if err != nil {
			return total, err
		}
		for _, id := range ids {
			s.emit(ctx, "food.restaurant."+id.String(), EventRestaurantFSSAIExpired, map[string]any{
				"restaurant_id":       id.String(),
				"reason":              "fssai_expired",
				"is_accepting_orders": false,
			})
		}
		total += len(ids)
		if len(ids) < fssaiExpiryBatch {
			break
		}
	}
	return total, nil
}
