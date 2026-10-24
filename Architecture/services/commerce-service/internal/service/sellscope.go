package service

// Sell scope — what a shop may list (migration 045).
//
// Founder, 24 Oct 2026: MSeller accepts a books-only seller with a PAN and a
// proof of address and no GSTIN, like Flipkart's "Only Books" path. Every
// other category still needs a GSTIN.
//
//	all_categories   the GSTIN is asked for, format- and checksum-checked
//	                 (shared/kyc), and stored on sellers.gst_number.
//	books_only       the PAN is asked for, checked (shared/kyc), and SEALED
//	                 under the kyc scope exactly as migration 035 seals every
//	                 seller PAN; plus the business name, a business address
//	                 (PUT /seller/address, address_type "business", sealed
//	                 like every seller address) and one proof of address
//	                 (PUT /onboarding/step/documents).
//
// A books-only shop may list only in a book category (the flagged category
// or anything beneath it) and only on a zero-rated tax class: printed books
// are GST-exempt, and a seller with no GSTIN must not collect GST at all.
// Either refusal is 403 SELLER_SCOPE_BOOKS_ONLY, naming the field.
//
// books_only → all_categories is the upgrade: a GSTIN whose embedded PAN is
// the PAN on file. There is no way back down — a shop with live listings in
// other categories cannot be narrowed under them.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/commerce-service/internal/store/postgres"
	sharedkyc "github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

// The refusals. Each is mapped at the edge (writeSellScopeError); none of
// their messages carries the value that was refused.
var (
	ErrSellScopeInvalid       = errors.New("sell_scope must be all_categories or books_only")
	ErrSellScopeFieldNotTaken = errors.New("that field is not taken on this sell scope")
	ErrGSTINRequired          = errors.New("a GSTIN is required to sell in all categories")
	ErrGSTINInvalid           = errors.New("that is not a valid GSTIN")
	ErrPANRequired            = errors.New("a PAN is required to sell books without a GSTIN")
	ErrPANInvalid             = errors.New("that is not a valid PAN")
	ErrBusinessNameRequired   = errors.New("the business name is required (2 to 200 characters)")
	ErrGSTINPANMismatch       = errors.New("the GSTIN is not registered to the PAN on file")
	// ErrSellerScopeBooksOnly is the listing refusal; it arrives wrapped in
	// a *SellerScopeBooksOnlyError that names the field.
	ErrSellerScopeBooksOnly = errors.New("this shop sells books only")
	// ErrDocumentNumberNotAccepted refuses a number on a document stored as
	// its image only (a bill or a bank statement, whose "number" is an
	// account number migration 035 would otherwise leave in plaintext).
	ErrDocumentNumberNotAccepted = errors.New("this document is stored as its image only; leave the number blank")
	// ErrDocumentBackNotAccepted refuses a back side on a single-sided
	// document, so a reviewer is never shown a "back" that is not one.
	ErrDocumentBackNotAccepted = errors.New("only a voter ID or a passport takes a back side")
)

// SellScopeFieldError says which field of the step a refusal is about.
type SellScopeFieldError struct {
	Field string
	Err   error
}

func (e *SellScopeFieldError) Error() string { return e.Field + ": " + e.Err.Error() }
func (e *SellScopeFieldError) Unwrap() error { return e.Err }

func fieldErr(field string, err error) error { return &SellScopeFieldError{Field: field, Err: err} }

// SellerScopeBooksOnlyError is a books-only shop trying to list outside its
// scope. Field is category_id or tax_class_id.
type SellerScopeBooksOnlyError struct {
	Field  string
	Reason string
}

func (e *SellerScopeBooksOnlyError) Error() string {
	return ErrSellerScopeBooksOnly.Error() + ": " + e.Reason
}
func (e *SellerScopeBooksOnlyError) Unwrap() error { return ErrSellerScopeBooksOnly }

// SellScopeStore is the slice of the store the sell-scope rules read and
// write. *postgres.Store in production; a fake in the unit tests.
type SellScopeStore interface {
	GetSellerByUserID(ctx context.Context, userID uuid.UUID) (*postgres.Seller, error)
	SaveOnboardingSellScope(ctx context.Context, userID uuid.UUID, w postgres.SellScopeWrite) error
	UpgradeSellScopeToAllCategories(ctx context.Context, sellerID uuid.UUID, gstin string) error
	CategoryIsBookCategory(ctx context.Context, id uuid.UUID) (bool, error)
	BookCategoryIDs(ctx context.Context) ([]uuid.UUID, error)
	TaxClassIsZeroRated(ctx context.Context, id uuid.UUID) (bool, error)
}

// WithSellScopeStore replaces the sell-scope store. Production never calls
// it (New wires the real store); the unit and handler tests do.
func (s *Service) WithSellScopeStore(st SellScopeStore) *Service {
	s.scope = st
	return s
}

func (s *Service) sellScopeStore() SellScopeStore {
	if s.scope != nil {
		return s.scope
	}
	if s.store != nil {
		return s.store
	}
	return nil
}

// ─── The "What will you sell?" step ─────────────────────────────────────

// SellScopeInput is PUT /onboarding/step/sell-scope as it arrives.
type SellScopeInput struct {
	SellScope    string
	GSTIN        *string
	PAN          *string
	BusinessName *string
}

// SellScopeSaved is the step's answer: what was stored, as the seller may
// see it again. The PAN comes back masked with its holder type (the fourth
// character), never in full.
type SellScopeSaved struct {
	SellScope     string  `json:"sell_scope"`
	GSTIN         *string `json:"gstin,omitempty"`
	BusinessName  *string `json:"business_name,omitempty"`
	PANMasked     string  `json:"pan_masked,omitempty"`
	PANHolderType string  `json:"pan_holder_type,omitempty"`
}

// sellScopeChoice is a validated step: everything normalised, nothing sealed.
type sellScopeChoice struct {
	scope        string
	gstin        string // all_categories
	pan          sharedkyc.PAN
	businessName string // "" = not given (all_categories only)
}

const businessNameMax = 200

func blank(p *string) bool { return p == nil || strings.TrimSpace(*p) == "" }

// validateSellScope is every rule of the step that needs nothing but the
// request. Pure, so each rule has a test that fails without it.
func validateSellScope(in SellScopeInput) (sellScopeChoice, error) {
	out := sellScopeChoice{scope: strings.ToLower(strings.TrimSpace(in.SellScope))}
	if !postgres.ValidSellScope(out.scope) {
		return out, fieldErr("sell_scope", ErrSellScopeInvalid)
	}
	if !blank(in.BusinessName) {
		name := strings.TrimSpace(*in.BusinessName)
		if n := len([]rune(name)); n < 2 || n > businessNameMax {
			return out, fieldErr("business_name", ErrBusinessNameRequired)
		}
		out.businessName = name
	}
	switch out.scope {
	case postgres.SellScopeAllCategories:
		if !blank(in.PAN) {
			// The GSTIN carries the PAN; a second copy would be a second
			// sealed identifier with nothing reading it.
			return out, fieldErr("pan", ErrSellScopeFieldNotTaken)
		}
		if blank(in.GSTIN) {
			return out, fieldErr("gstin", ErrGSTINRequired)
		}
		g, err := sharedkyc.ValidateGSTIN(*in.GSTIN)
		if err != nil {
			return out, fieldErr("gstin", fmt.Errorf("%w (%v)", ErrGSTINInvalid, err))
		}
		out.gstin = g.Normalized
	case postgres.SellScopeBooksOnly:
		if !blank(in.GSTIN) {
			// A seller who has a GSTIN is not on the books-only path; it
			// would print on their invoices beside "no GSTIN required".
			return out, fieldErr("gstin", ErrSellScopeFieldNotTaken)
		}
		if blank(in.PAN) {
			return out, fieldErr("pan", ErrPANRequired)
		}
		p, err := sharedkyc.ValidatePAN(*in.PAN)
		if err != nil {
			return out, fieldErr("pan", fmt.Errorf("%w (%v)", ErrPANInvalid, err))
		}
		out.pan = p
		if out.businessName == "" {
			return out, fieldErr("business_name", ErrBusinessNameRequired)
		}
	}
	return out, nil
}

// SaveSellScope stores the step for the caller's own draft.
//
// Validation first, then the PAN seal, then the store: a service with no
// cipher refuses before anything is written, so a PAN is never stored in the
// clear (the same order SavePayout keeps).
func (s *Service) SaveSellScope(ctx context.Context, userID uuid.UUID, in SellScopeInput) (*SellScopeSaved, error) {
	choice, err := validateSellScope(in)
	if err != nil {
		return nil, err
	}
	w := postgres.SellScopeWrite{Scope: choice.scope}
	out := &SellScopeSaved{SellScope: choice.scope}
	if choice.businessName != "" {
		name := choice.businessName
		w.LegalBusinessName = &name
		out.BusinessName = &name
	}
	switch choice.scope {
	case postgres.SellScopeAllCategories:
		g := choice.gstin
		w.GSTIN = &g
		out.GSTIN = &g
	case postgres.SellScopeBooksOnly:
		pan := choice.pan.Normalized
		sealed, err := s.sealOrganizationPAN(ctx, &pan)
		if err != nil {
			return nil, err
		}
		w.PAN = sealed
		out.PANMasked = sealed.Masked
		out.PANHolderType = choice.pan.HolderType.String()
	}
	st := s.sellScopeStore()
	if st == nil {
		return nil, errors.New("commerce: no store configured")
	}
	if err := st.SaveOnboardingSellScope(ctx, userID, w); err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) {
			return nil, ErrNoSellerProfile
		}
		return nil, err
	}
	return out, nil
}

// ─── The upgrade ────────────────────────────────────────────────────────

// SellScopeView is GET /seller/sell-scope: the shop's scope and, for a
// books-only shop, the categories it may list in.
type SellScopeView struct {
	SellScope string `json:"sell_scope"`
	HasGSTIN  bool   `json:"has_gstin"`
	// BookCategoryIDs is every category a books-only shop may list in.
	// Present for every shop, so a client never needs a second answer.
	BookCategoryIDs []uuid.UUID `json:"book_category_ids"`
	// CanUpgrade: a books-only shop may add a GSTIN and sell everything.
	CanUpgrade bool `json:"can_upgrade"`
}

func (s *Service) callerSeller(ctx context.Context, st SellScopeStore, userID uuid.UUID) (*postgres.Seller, error) {
	sel, err := st.GetSellerByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) {
			return nil, ErrNoSellerProfile
		}
		return nil, err
	}
	if sel == nil {
		return nil, ErrNoSellerProfile
	}
	return sel, nil
}

// SellScope reads the caller's scope view.
func (s *Service) SellScope(ctx context.Context, userID uuid.UUID) (*SellScopeView, error) {
	st := s.sellScopeStore()
	if st == nil {
		return nil, errors.New("commerce: no store configured")
	}
	sel, err := s.callerSeller(ctx, st, userID)
	if err != nil {
		return nil, err
	}
	ids, err := st.BookCategoryIDs(ctx)
	if err != nil {
		return nil, err
	}
	return &SellScopeView{
		SellScope:       sel.SellScope,
		HasGSTIN:        !blank(sel.GSTNumber),
		BookCategoryIDs: ids,
		CanUpgrade:      sel.SellScope == postgres.SellScopeBooksOnly,
	}, nil
}

// UpgradeSellScope moves the caller's books-only shop to all categories.
//
// The GSTIN must be valid AND registered to the PAN already on file: the
// shop's identity was reviewed on that PAN, and a GSTIN of some other PAN is
// some other business. Format and checksum only, as everywhere else — the
// GSTN search API is the vendor follow-up AdminVerifySellerKYC names.
//
// The shop's approval is untouched: it was approved as this person, and
// listings in the new categories still go through product review.
func (s *Service) UpgradeSellScope(ctx context.Context, userID uuid.UUID, gstin string) (*SellScopeView, error) {
	if strings.TrimSpace(gstin) == "" {
		return nil, fieldErr("gstin", ErrGSTINRequired)
	}
	g, err := sharedkyc.ValidateGSTIN(gstin)
	if err != nil {
		return nil, fieldErr("gstin", fmt.Errorf("%w (%v)", ErrGSTINInvalid, err))
	}
	st := s.sellScopeStore()
	if st == nil {
		return nil, errors.New("commerce: no store configured")
	}
	sel, err := s.callerSeller(ctx, st, userID)
	if err != nil {
		return nil, err
	}
	if sel.SellScope != postgres.SellScopeBooksOnly {
		return nil, postgres.ErrSellScopeNotBooksOnly
	}
	pan, err := s.sellerPAN(ctx, sel)
	if err != nil {
		return nil, err
	}
	// No PAN on file cannot match anything: fail closed.
	if pan == "" || g.PAN != pan {
		return nil, fieldErr("gstin", ErrGSTINPANMismatch)
	}
	if err := st.UpgradeSellScopeToAllCategories(ctx, sel.ID, g.Normalized); err != nil {
		return nil, err
	}
	s.publish(ctx, "commerce.seller.sell_scope_upgraded", map[string]any{
		"seller_id": sel.ID, "user_id": sel.UserID, "sell_scope": postgres.SellScopeAllCategories,
	})
	ids, err := st.BookCategoryIDs(ctx)
	if err != nil {
		return nil, err
	}
	return &SellScopeView{
		SellScope:       postgres.SellScopeAllCategories,
		HasGSTIN:        true,
		BookCategoryIDs: ids,
		CanUpgrade:      false,
	}, nil
}

// ─── Listings ───────────────────────────────────────────────────────────

// checkBooksOnlyListing refuses a books-only shop's listing outside books.
//
// Only an explicit books_only scope is restricted: every caller passes the
// scope of a seller row read by GetSellerByUserID, which selects it.
//
// `complete` is the submit: an uncategorised draft is a real state (see
// requireCategory) and is allowed to be saved, but it may not go to review
// without a book category. A tax class is refused only when it charges GST;
// a missing one is the submit gate's own refusal.
func (s *Service) checkBooksOnlyListing(ctx context.Context, scope string, categoryID, taxClassID *uuid.UUID, complete bool) error {
	if scope != postgres.SellScopeBooksOnly {
		return nil
	}
	st := s.sellScopeStore()
	if st == nil {
		return errors.New("commerce: no store configured")
	}
	if categoryID == nil {
		if complete {
			return &SellerScopeBooksOnlyError{Field: "category_id",
				Reason: "choose a book category before sending this listing for review"}
		}
	} else {
		isBook, err := st.CategoryIsBookCategory(ctx, *categoryID)
		if err != nil {
			return err
		}
		if !isBook {
			return &SellerScopeBooksOnlyError{Field: "category_id",
				Reason: "a books-only shop can list only in book categories; add a GSTIN to sell in others"}
		}
	}
	if taxClassID != nil {
		zero, err := st.TaxClassIsZeroRated(ctx, *taxClassID)
		if err != nil {
			return err
		}
		if !zero {
			return &SellerScopeBooksOnlyError{Field: "tax_class_id",
				Reason: "books are GST-exempt and a shop without a GSTIN cannot charge GST; choose GST 0%"}
		}
	}
	return nil
}

// ─── Proof-of-address documents ─────────────────────────────────────────

// imageOnlyDocumentTypes are stored as their upload alone. The number on a
// bank statement is a bank account, and on a bill it is a consumer account;
// neither has a reason to sit in seller_documents.document_number, which
// migration 035 does not seal.
var imageOnlyDocumentTypes = []string{"electricity_bill", "telephone_bill", "bank_statement"}

// validateDocumentSides checks the migration-045 rules of a document set:
// a back side only on a two-sided document and never the front's own image,
// and no number on an image-only proof. The missing-back rule is readiness's
// (a voter ID without its back is saved, and does not count as a proof of
// address until the back is added), so a seller can upload one side at a time.
func validateDocumentSides(docs []postgres.SellerDocument) error {
	for _, d := range docs {
		if d.BackMediaID != nil && *d.BackMediaID != uuid.Nil {
			if !postgres.IsTwoSidedType(d.DocumentType) {
				return fmt.Errorf("%w (document_type %s)", ErrDocumentBackNotAccepted, d.DocumentType)
			}
			if *d.BackMediaID == d.MediaID {
				return fmt.Errorf("%w: the back must be a different image from the front", ErrDocumentBackNotAccepted)
			}
		}
		if d.DocumentNumber != nil && strings.TrimSpace(*d.DocumentNumber) != "" {
			for _, t := range imageOnlyDocumentTypes {
				if d.DocumentType == t {
					return fmt.Errorf("%w (document_type %s)", ErrDocumentNumberNotAccepted, d.DocumentType)
				}
			}
		}
	}
	return nil
}
