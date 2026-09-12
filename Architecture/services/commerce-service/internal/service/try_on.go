// Face AR try-on: publishing a product's descriptor, and reading it back.
//
// The capability is opt-in per product (migration 034 argues why), and the
// category tree is a FENCE over which kinds a product may claim rather than a
// source of capability. A lipstick must not be publishable as eyewear: the AR
// effect for eyewear tracks a nose bridge and would render a frame across a
// mouth, and the viewer's conclusion would be that the platform is broken
// rather than that a field was typed wrong.
package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/atpost/commerce-service/internal/store/postgres"
)

var (
	// ErrTryOnKindNotAllowed means the kind is real but not offerable for
	// this product's category tree.
	ErrTryOnKindNotAllowed = errors.New("commerce: try-on kind not allowed for this category")
	// ErrTryOnInvalid means the descriptor itself is malformed.
	ErrTryOnInvalid = errors.New("commerce: invalid try-on descriptor")
)

// TryOnKind values, matching the CHECK in migration 034 and the client enum.
const (
	TryOnEyewear   = "eyewear"
	TryOnMakeup    = "makeup"
	TryOnJewellery = "jewellery"
	TryOnWatch     = "watch"
)

// MaxTryOnVariants caps the strip. Twelve is the most a viewer can scan on a
// phone while their own face is on screen; beyond that the strip becomes a
// catalogue and belongs on the product page instead.
const MaxTryOnVariants = 12

var (
	effectSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	hexPattern        = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// tryOnKindsByCategoryRoot is the fence, keyed by the ROOT category slug
// seeded in migration 023.
//
// Roots, not leaves: a seller's own sub-category is theirs to name, and
// keying on leaves would need an entry per new leaf and would fail closed on
// a category added after this map was written. Deliberately absent roots
// (electronics, grocery, books, automotive, toys, home, sports, handicrafts)
// admit no try-on at all, which is the correct default — nothing in them goes
// on a face.
//
// `fashion` admits eyewear only. Fashion is the broadest root in the seed and
// most of it is garments, which need body tracking rather than face tracking
// and are not in this capability at all.
var tryOnKindsByCategoryRoot = map[string][]string{
	"beauty-and-personal-care": {TryOnMakeup},
	"jewellery-and-watches":    {TryOnJewellery, TryOnWatch},
	"fashion":                  {TryOnEyewear},
}

// TryOnKindsForCategoryRoot returns the kinds a root admits, never nil.
// Pure, so the fence is testable without a database.
func TryOnKindsForCategoryRoot(rootSlug string) []string {
	kinds := tryOnKindsByCategoryRoot[strings.ToLower(strings.TrimSpace(rootSlug))]
	out := make([]string, len(kinds))
	copy(out, kinds)
	return out
}

// TryOnKindAllowed reports whether this root may offer this kind. Pure.
func TryOnKindAllowed(rootSlug, kind string) bool {
	for _, k := range TryOnKindsForCategoryRoot(rootSlug) {
		if k == kind {
			return true
		}
	}
	return false
}

// ValidateTryOnDescriptor checks the shape a client sent, independently of
// the category fence and of the database. Pure.
//
// It normalises as it goes — kind and slug lowercased and trimmed, variant
// labels trimmed — and returns the value to store, so the handler never
// stores a differently-spelled copy of what was validated.
func ValidateTryOnDescriptor(kind, effectSlug string, variants []postgres.TryOnVariant) (*postgres.ProductTryOn, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case TryOnEyewear, TryOnMakeup, TryOnJewellery, TryOnWatch:
	default:
		return nil, fmt.Errorf("%w: kind must be one of eyewear, makeup, jewellery, watch", ErrTryOnInvalid)
	}

	effectSlug = strings.ToLower(strings.TrimSpace(effectSlug))
	if !effectSlugPattern.MatchString(effectSlug) {
		return nil, fmt.Errorf("%w: effect_slug must be lowercase letters, digits, underscore or hyphen, up to 64 characters", ErrTryOnInvalid)
	}

	if len(variants) > MaxTryOnVariants {
		return nil, fmt.Errorf("%w: at most %d variants", ErrTryOnInvalid, MaxTryOnVariants)
	}

	out := make([]postgres.TryOnVariant, 0, len(variants))
	seen := make(map[string]struct{}, len(variants))
	for i, v := range variants {
		id := strings.TrimSpace(v.ID)
		label := strings.TrimSpace(v.Label)
		if id == "" {
			return nil, fmt.Errorf("%w: variant %d has no id", ErrTryOnInvalid, i+1)
		}
		if label == "" {
			return nil, fmt.Errorf("%w: variant %d has no label", ErrTryOnInvalid, i+1)
		}
		// A duplicate id would make the strip ambiguous: two entries would
		// resolve to the same purchasable colourway and the selected state
		// could not be drawn.
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("%w: variant id %q appears twice", ErrTryOnInvalid, id)
		}
		seen[id] = struct{}{}
		hex := strings.TrimSpace(v.Hex)
		if hex != "" && !hexPattern.MatchString(hex) {
			return nil, fmt.Errorf("%w: variant %q hex must be #RRGGBB", ErrTryOnInvalid, id)
		}
		// A look must be drivable: either a colour the effect can take or an
		// explicit call. Neither means the strip would show an entry that
		// changes nothing when tapped.
		if hex == "" && strings.TrimSpace(v.JS) == "" {
			return nil, fmt.Errorf("%w: variant %q needs a hex or a js call", ErrTryOnInvalid, id)
		}
		out = append(out, postgres.TryOnVariant{
			ID: id, Label: label, Hex: hex, JS: strings.TrimSpace(v.JS),
		})
	}

	return &postgres.ProductTryOn{
		Capable: true, Kind: kind, EffectSlug: effectSlug, Variants: out,
	}, nil
}

// ProductTryOn reads a product's descriptor for the detail page. A product
// with none returns (nil, nil), which the handler renders as an absent field.
func (s *Service) ProductTryOn(ctx context.Context, productID uuid.UUID) (*postgres.ProductTryOn, error) {
	return s.store.GetProductTryOn(ctx, productID)
}

// SetProductTryOn publishes a descriptor, seller-gated and category-fenced.
//
// Order matters: ownership first, so a caller who does not own the product
// learns nothing about its category from the error it gets back.
func (s *Service) SetProductTryOn(
	ctx context.Context,
	productID, actorUserID uuid.UUID,
	kind, effectSlug string,
	variants []postgres.TryOnVariant,
) (*postgres.ProductTryOn, error) {
	if err := s.assertProductSeller(ctx, productID, actorUserID); err != nil {
		return nil, err
	}
	descriptor, err := ValidateTryOnDescriptor(kind, effectSlug, variants)
	if err != nil {
		return nil, err
	}

	product, err := s.store.GetProductByID(ctx, productID)
	if err != nil {
		return nil, err
	}
	if product == nil {
		return nil, ErrProductNotFound
	}
	// No category means no fence can pass. Try-on is merchandising content
	// on a buyer's own face; publishing it against an uncategorised product
	// would put it outside the one check that keeps a lipstick from being
	// rendered as a pair of glasses.
	if product.CategoryID == nil {
		return nil, fmt.Errorf("%w: the product has no category", ErrTryOnKindNotAllowed)
	}
	root, err := s.store.CategoryRootSlug(ctx, *product.CategoryID)
	if err != nil {
		return nil, err
	}
	if !TryOnKindAllowed(root, descriptor.Kind) {
		allowed := TryOnKindsForCategoryRoot(root)
		if len(allowed) == 0 {
			return nil, fmt.Errorf("%w: nothing under %q can be tried on", ErrTryOnKindNotAllowed, root)
		}
		return nil, fmt.Errorf("%w: %q admits %s", ErrTryOnKindNotAllowed, root, strings.Join(allowed, ", "))
	}

	descriptor.ProductID = productID
	if err := s.store.UpsertProductTryOn(ctx, descriptor, actorUserID); err != nil {
		return nil, err
	}
	return descriptor, nil
}

// ClearProductTryOn withdraws the capability. Seller-gated, idempotent.
func (s *Service) ClearProductTryOn(ctx context.Context, productID, actorUserID uuid.UUID) error {
	if err := s.assertProductSeller(ctx, productID, actorUserID); err != nil {
		return err
	}
	return s.store.DeleteProductTryOn(ctx, productID)
}
