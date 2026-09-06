package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/commerce-service/internal/kyc"
	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// ErrKYCNotConfigured is returned when an admin tries to run KYC verification
// but no validator has been wired (Phase 3.2). Operators should configure a
// vendor adapter or accept the stub for dev/QA.
var ErrKYCNotConfigured = fmt.Errorf("kyc validator not configured")

// ErrInvalidDocumentType is a KYC document kind the schema does not accept.
// Mapped to 400 at the edge, with the permitted vocabulary in the message.
var ErrInvalidDocumentType = errors.New("unsupported document_type")

// ─── Onboarding wizard ───────────────────────────────────────────

type StartOnboardingInput struct {
	UserID         uuid.UUID
	BusinessPageID *uuid.UUID
	StoreName      string
	Email          string
	SellerType     string
	BusinessType   string
}

// StartOnboarding creates a draft seller record. Idempotent — returns existing if already started.
func (s *Service) StartOnboarding(ctx context.Context, in StartOnboardingInput) (*postgres.Seller, error) {
	// Return existing draft if present
	existing, err := s.store.GetSellerByUserID(ctx, in.UserID)
	if err == nil {
		return existing, nil
	}

	if in.StoreName == "" {
		return nil, fmt.Errorf("store_name is required")
	}
	slug := uniqueSlug(slugify(in.StoreName))
	sel := &postgres.Seller{
		UserID:         in.UserID,
		BusinessPageID: in.BusinessPageID,
		StoreName:      in.StoreName,
		Email:          in.Email,
		Slug:           slug,
		SellerType:     coalesceStr(in.SellerType, "individual"),
		BusinessType:   coalesceStr(in.BusinessType, "individual"),
	}
	if err := s.store.StartSellerOnboarding(ctx, sel); err != nil {
		return nil, fmt.Errorf("start onboarding: %w", err)
	}
	return sel, nil
}

// GetOnboardingStatus returns the current seller draft/status for a user.
func (s *Service) GetOnboardingStatus(ctx context.Context, userID uuid.UUID) (*postgres.Seller, error) {
	return s.store.GetSellerOnboardingStatus(ctx, userID)
}

// SaveBasicInfo saves step 3 fields.
func (s *Service) SaveBasicInfo(ctx context.Context, userID uuid.UUID, in postgres.OnboardingBasicInput) error {
	if in.StoreName == "" || in.Email == "" {
		return fmt.Errorf("store_name and email are required")
	}
	if in.SellerType == "" {
		in.SellerType = "individual"
	}
	if in.BusinessType == "" {
		in.BusinessType = "individual"
	}
	return s.store.SaveOnboardingBasic(ctx, userID, in)
}

// SaveStorefront saves step 4 fields.
func (s *Service) SaveStorefront(ctx context.Context, userID uuid.UUID, in postgres.OnboardingStorefrontInput) error {
	// A storefront logo and banner are what a buyer uses to tell one seller
	// from another. Unverified, a seller could adopt a competitor's brand
	// imagery by id.
	if err := s.verifyMedia(ctx, userID, media.KindImage, in.LogoMediaID, in.BannerMediaID); err != nil {
		return err
	}
	return s.store.SaveOnboardingStorefront(ctx, userID, in)
}

// SaveDocuments saves step 5 KYC documents.
//
// This is the one that matters most. `seller_documents` holds PAN, Aadhaar and
// the cancelled cheque — the evidence a human reviewer looks at before
// approving a seller to take money. Nothing verified that the media id in each
// row belonged to the person submitting it, so a seller could point their KYC
// at somebody else's uploaded identity document and be approved on it. That
// makes the review meaningless, and it is how one person's identity documents
// end up attached to another person's payout account.
func (s *Service) SaveDocuments(ctx context.Context, userID uuid.UUID, docs []postgres.SellerDocument) error {
	// The allowed set, checked here rather than discovered in Postgres.
	//
	// `seller_documents.document_type` carries a CHECK constraint, so an
	// unknown type was refused — correctly — but the violation travelled to
	// the edge as an unmapped store error and became a 500. A seller who
	// typed the wrong document kind was told the platform was broken and
	// was never told which kinds exist. This names them.
	for _, d := range docs {
		if !postgres.ValidDocumentType(d.DocumentType) {
			return fmt.Errorf("%w: %q (allowed: %s)",
				ErrInvalidDocumentType, d.DocumentType,
				strings.Join(postgres.SellerDocumentTypes, ", "))
		}
	}
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("seller not found: %w", err)
	}
	// Every document, before any of them is stored. A partial write here
	// would leave a KYC set that is half verified and half not, and the
	// reviewer cannot tell which half.
	for i := range docs {
		if err := s.verifyMedia(ctx, userID, media.KindAny, &docs[i].MediaID); err != nil {
			return err
		}
	}
	return s.store.SaveOnboardingCompliance(ctx, sel.ID, docs)
}

// SaveFulfillment saves step 6 fields.
func (s *Service) SaveFulfillment(ctx context.Context, userID uuid.UUID, in postgres.OnboardingFulfillmentInput) error {
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("seller not found: %w", err)
	}
	return s.store.SaveOnboardingFulfillment(ctx, sel.ID, in)
}

// SavePayout saves step 7 bank details.
func (s *Service) SavePayout(ctx context.Context, userID uuid.UUID, in postgres.OnboardingPayoutInput) error {
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("seller not found: %w", err)
	}
	return s.store.SaveOnboardingPayout(ctx, sel.ID, in)
}

// SellerReadiness reports what a shop still needs before it can be reviewed.
//
// Exposed so the app can show the remaining checklist rather than letting a
// seller press Submit and be told no.
func (s *Service) SellerReadiness(ctx context.Context, userID uuid.UUID) (*postgres.SellerReadiness, error) {
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) {
			return nil, ErrNoSellerProfile
		}
		return nil, err
	}
	return s.store.SellerReadinessFor(ctx, sel.ID)
}

// SubmitApplication submits the seller application for review.
//
// It refuses an incomplete application. Before this check, any draft could be
// submitted: a shop with no PAN, no bank account and no pickup address landed
// in a human reviewer's queue as an empty application, the seller was told
// "submitted" and waited, and a reviewer who approved anyway put live a shop
// that could take money with no settlement path.
//
// The refusal names everything still missing rather than the first thing, so
// the app can render the whole remaining checklist. A reviewer queue is not a
// security boundary, and one missing item at a time turns a five-minute task
// into five round trips.
func (s *Service) SubmitApplication(ctx context.Context, userID uuid.UUID) error {
	ready, err := s.SellerReadiness(ctx, userID)
	if err != nil {
		return err
	}
	if !ready.Complete() {
		return fmt.Errorf("%w: %s", ErrApplicationIncomplete,
			strings.Join(ready.Missing(), ", "))
	}
	if err := s.store.SubmitSellerApplication(ctx, userID); err != nil {
		return err
	}
	s.publish(ctx, events.EventSellerSubmitted, map[string]any{"user_id": userID})
	return nil
}

// GetDashboard returns seller dashboard stats.
func (s *Service) GetDashboard(ctx context.Context, userID uuid.UUID) (*postgres.DashboardStats, error) {
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("seller not found")
	}
	return s.store.GetDashboardStats(ctx, sel.ID)
}

// SubmitProduct has moved to submitgate.go, where it acquired the
// completeness gate. It used to be four lines here and asked nothing about
// the listing at all.

// ─── Internal admin operations (called by admin-service) ─────────

func (s *Service) AdminListSellerQueue(ctx context.Context, limit, offset int) ([]*postgres.Seller, int, error) {
	return s.store.ListSellerQueue(ctx, limit, offset)
}

func (s *Service) AdminGetSeller(ctx context.Context, sellerID uuid.UUID) (*postgres.Seller, error) {
	return s.store.GetSellerByID(ctx, sellerID)
}

func (s *Service) AdminApproveSeller(ctx context.Context, sellerID, actorID uuid.UUID, notes string) error {
	if err := s.store.ApproveSellerByAdmin(ctx, sellerID, actorID, notes); err != nil {
		return err
	}
	// Include business_page_id so user-service can activate the page
	payload := map[string]any{"seller_id": sellerID, "actor_id": actorID}
	sel, err := s.store.GetSellerByID(ctx, sellerID)
	if err == nil && sel.BusinessPageID != nil {
		payload["business_page_id"] = *sel.BusinessPageID
	}
	s.publish(ctx, events.EventSellerApproved, payload)
	return nil
}

func (s *Service) AdminRejectSeller(ctx context.Context, sellerID, actorID uuid.UUID, reason, notes string) error {
	if err := s.store.RejectSellerByAdmin(ctx, sellerID, actorID, reason, notes); err != nil {
		return err
	}
	s.publish(ctx, events.EventSellerRejected, map[string]any{"seller_id": sellerID, "reason": reason})
	return nil
}

func (s *Service) AdminRequestSellerChanges(ctx context.Context, sellerID, actorID uuid.UUID, changes, notes string) error {
	return s.store.RequestSellerChanges(ctx, sellerID, actorID, changes, notes)
}

// AdminListPendingPayouts returns one row per seller with outstanding COD
// remittance balance, oldest delivery first. Phase 4.5 — feeds the admin
// payout reconciliation dashboard.
func (s *Service) AdminListPendingPayouts(ctx context.Context, limit int) ([]*postgres.PendingPayoutSummary, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.store.ListPendingPayoutsBySeller(ctx, limit)
}

// AdminVerifySellerKYC runs the configured KYC adapter against the seller's
// stored GSTIN/PAN + primary payout account. The adapter's verdict is also
// stored on the seller row so the admin queue can render verification at a
// glance. The report is returned for the UI to show per-field detail.
//
// Phase 3.2: stub adapter returns format-only checks. A production deployment
// must wire a vendor (Karza/Signzy/Hyperverge) via WithKYC before approving
// sellers, otherwise admins are approving on signal alone.
func (s *Service) AdminVerifySellerKYC(ctx context.Context, sellerID uuid.UUID) (*kyc.Report, error) {
	if s.kyc == nil {
		return nil, ErrKYCNotConfigured
	}
	sel, err := s.store.GetSellerByID(ctx, sellerID)
	if err != nil {
		return nil, fmt.Errorf("load seller: %w", err)
	}
	snap := kyc.SellerSnapshot{}
	if sel.GSTNumber != nil {
		snap.GSTIN = *sel.GSTNumber
	}
	if sel.PANNumber != nil {
		snap.PAN = *sel.PANNumber
	}
	if pa, err := s.store.GetPrimaryPayoutAccount(ctx, sellerID); err == nil && pa != nil {
		snap.BankAccountNo = pa.AccountNumber
		if pa.IFSCCode != nil {
			snap.IFSC = *pa.IFSCCode
		}
		if pa.UPIID != nil {
			snap.UPI = *pa.UPIID
		}
	}
	rep, err := s.kyc.Verify(ctx, snap)
	if err != nil {
		return nil, fmt.Errorf("kyc verify: %w", err)
	}
	// B12 — a FORMAT check is not a verification.
	//
	// This wrote `verification_status = 'verified'` whenever the adapter's
	// AllValid was true. In production the configured adapter is
	// kyc.StubValidator, which does regex checks: a well-formed but entirely
	// fictitious PAN, GSTIN and bank account produce AllValid, the seller row
	// reads `verified`, and an admin approving from that queue is looking at
	// a field that says identity was confirmed when nothing was confirmed.
	// The seller then becomes payout-eligible. That is a fraud path, not a
	// display bug.
	//
	// A verdict may only write `verified` if it came from an adapter that
	// actually verifies. The stub's own report already tags every check with
	// Source="stub" precisely so this distinction is available — it simply
	// was not consulted. `format_ok` is a real, distinct state: the paperwork
	// parses, and identity is still unproven.
	status := "pending"
	switch {
	case rep.AllValid && s.kyc.Name() == "stub":
		status = "format_ok"
		slog.Warn("commerce: seller KYC passed FORMAT checks only; identity is not verified",
			"seller_id", sellerID, "adapter", "stub",
			"detail", "wire a vendor adapter (Karza/Signzy/Hyperverge) before treating this seller as verified")
	case rep.AllValid:
		status = "verified"
	}
	if err := s.store.SetSellerKYCVerificationStatus(ctx, sellerID, status); err != nil {
		// Verdict is the user-visible result; persistence failure is
		// logged via the publish path below and surfaces in the report.
		s.publish(ctx, "commerce.seller.kyc_persist_failed", map[string]any{
			"seller_id": sellerID, "error": err.Error(),
		})
	}
	s.publish(ctx, "commerce.seller.kyc_verified", map[string]any{
		"seller_id": sellerID, "adapter": s.kyc.Name(), "all_valid": rep.AllValid,
		// B12: consumers must be able to tell a format pass from a real
		// verification without knowing which adapter was configured.
		"status": status,
	})
	return rep, nil
}

func (s *Service) AdminSuspendSeller(ctx context.Context, sellerID, actorID uuid.UUID, reason, notes string) error {
	if err := s.store.SuspendSellerByAdmin(ctx, sellerID, actorID, reason, notes); err != nil {
		return err
	}
	s.publish(ctx, events.EventSellerSuspended, map[string]any{"seller_id": sellerID, "reason": reason})
	return nil
}

func (s *Service) AdminListProductQueue(ctx context.Context, limit, offset int) ([]*postgres.Product, int, error) {
	return s.store.ListProductQueue(ctx, limit, offset)
}

func (s *Service) AdminApproveProduct(ctx context.Context, productID, actorID uuid.UUID, notes string) error {
	if err := s.store.ApproveProductByAdmin(ctx, productID, actorID, notes); err != nil {
		return err
	}
	s.publish(ctx, events.EventProductApproved, map[string]any{"product_id": productID})
	// The listing is now live. This is THE transition search exists to hear
	// about — see internal/service/searchdoc.go. Separate from the
	// EventProductApproved above, which is a moderation-audit fact whose
	// consumers care that a human decided something; this one is a
	// visibility fact whose consumer cares what a buyer can now find.
	s.publishProductVisibility(ctx, productID)
	return nil
}

func (s *Service) AdminRejectProduct(ctx context.Context, productID, actorID uuid.UUID, reason string) error {
	if err := s.store.RejectProductByAdmin(ctx, productID, actorID, reason); err != nil {
		return err
	}
	// A rejected listing must leave the index. Before this line the index
	// had no way to learn that: nothing was published on rejection at all.
	s.publishProductVisibility(ctx, productID)
	return nil
}

// AdminRequestProductChanges parks the product so the seller can fix +
// resubmit. Phase 3.4 — admins previously had only approve/reject.
func (s *Service) AdminRequestProductChanges(ctx context.Context, productID, actorID uuid.UUID, message string) error {
	if err := s.store.RequestProductChangesByAdmin(ctx, productID, actorID, message); err != nil {
		return err
	}
	s.publishProductVisibility(ctx, productID)
	return nil
}
