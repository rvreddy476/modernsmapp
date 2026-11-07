// Catalogue: creating, reading and listing products, categories and
// variants (add, update, archive), plus product media and product attribute
// writes guarded by the product-owner check. Moved out of service.go unchanged.

package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Catalog ─────────────────────────────────────────────────

type CreateProductInput struct {
	SellerID uuid.UUID
	// SellScope is the seller's sellers.sell_scope (migration 045). A
	// books_only shop is held to book categories on a zero-rated class.
	SellScope string
	// ActorUserID is the human behind the seller account. Media ownership is
	// recorded against the USER who uploaded it, not the seller row, so this
	// is the identity the media check compares against.
	ActorUserID      uuid.UUID
	CategoryID       *uuid.UUID
	BrandID          *uuid.UUID
	TaxClassID       *uuid.UUID
	Title            string
	ShortTitle       *string
	Description      *string
	ShortDescription *string
	BrandName        *string
	ManufacturerName *string
	ProductType      string
	Condition        string
	ReturnPolicyType string
	ReturnPolicyDays int
	HSNCode          *string
	// Logistics & legal-metrology — schema has columns; UI exposes none today.
	PrimaryImageMediaID *uuid.UUID
	VideoMediaID        *uuid.UUID
	WeightGrams         *int
	LengthCm            *float64
	WidthCm             *float64
	HeightCm            *float64
	CountryOfOrigin     *string
	WarrantyInfo        *string
	SearchKeywords      []string
	MetaTitle           *string
	MetaDescription     *string
	Variants            []CreateVariantInput
	// Attributes are the category-specific answers, validated against the
	// category's published schema and written in the SAME transaction as the
	// product. Incomplete is fine — see productwrite.go on why a create must
	// not demand every required field.
	Attributes []AttributeValueInput

	// VariationAxes are the attributes this product varies on, in order.
	// Empty for a product that does not vary, which is most of them, and
	// which is what every caller written before migration 028 sends.
	//
	// Validated against the category's schema — an axis must be an attribute
	// the category asks for AND be marked as a variant axis for it — and
	// every variant must then carry exactly one value on every axis. See
	// internal/service/variationaxes.go, and in particular why the values
	// are codes rather than whatever the seller typed.
	VariationAxes []VariationAxisInput
}

type CreateVariantInput struct {
	SKU string
	// MRPMinor and SellingPriceMinor are the authority. The rupee fields
	// below are the legacy shape, kept because this path predates the
	// minor-unit migration; the edge resolves one from the other before
	// anything reaches here.
	MRPMinor          int64
	SellingPriceMinor int64
	CostPriceMinor    *int64
	Option1Name       *string
	Option1Value      *string
	Option2Name       *string
	Option2Value      *string
	Option3Name       *string
	Option3Value      *string
	MRP               float64
	SellingPrice      float64
	CostPrice         *float64
	StockQty          int

	// Options are this variant's values on the product's declared axes.
	// Exactly one per axis, no more and no fewer — a variant that answers
	// only half the matrix is not a distinct thing to sell.
	Options []VariantOptionInput
}

func (s *Service) CreateProduct(ctx context.Context, in CreateProductInput) (*postgres.Product, error) {
	if in.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if len(in.Variants) == 0 {
		return nil, fmt.Errorf("at least one variant is required")
	}

	// The two checks the schema would otherwise make, by name and before
	// anything is read: a return window outside products.return_policy_type's
	// CHECK came back as the generic INVALID_VALUE, and a selling price above
	// the MRP was not checked anywhere on the server.
	if in.ReturnPolicyType != "" && !validReturnPolicyType(in.ReturnPolicyType) {
		return nil, fmt.Errorf("%w: got %q", ErrReturnPolicyInvalid, in.ReturnPolicyType)
	}
	for _, vi := range in.Variants {
		if err := priceWithinMRP(vi.MRPMinor, vi.SellingPriceMinor); err != nil {
			return nil, fmt.Errorf("%w (sku %s)", err, vi.SKU)
		}
	}

	// A books-only shop (migration 045) lists books, on GST 0%, and nothing
	// else. Before the category is looked up, so an unknown category from a
	// books-only shop is told what it may list rather than that it typo'd.
	if err := s.checkBooksOnlyListing(ctx, in.SellScope, in.CategoryID, in.TaxClassID, false); err != nil {
		return nil, err
	}

	// Before anything is written. Both ids arrive in the request body and
	// were stored unchecked, so a seller could point their listing at a
	// competitor's product photography by reading the media id out of the
	// competitor's public product JSON.
	if err := s.verifyMedia(ctx, in.ActorUserID, media.KindImage, in.PrimaryImageMediaID); err != nil {
		return nil, err
	}
	if err := s.verifyMedia(ctx, in.ActorUserID, media.KindVideo, in.VideoMediaID); err != nil {
		return nil, err
	}

	// The category, before anything is written and before the attribute
	// values are checked against it. Nothing in this service verified it: the
	// foreign key was the only guard, it reports an unnamed constraint
	// violation, and an unknown category also means the create form the
	// seller filled in had no fields on it at all.
	if err := s.requireCategory(ctx, in.CategoryID); err != nil {
		return nil, err
	}

	// Answers checked against the category's form. Every failure comes back
	// at once, keyed by code, so a form can put each message under its own
	// control. Incompleteness is NOT checked: a draft may be unfinished.
	attrSets, err := s.resolveAttributeValues(ctx, in.CategoryID, in.Attributes)
	if err != nil {
		return nil, err
	}

	// The matrix, checked before anything is written and against the same
	// category form the answers were. A nil result is "this product does not
	// vary", which is the answer for most listings and is not an error.
	vv := make([]variationVariant, len(in.Variants))
	for i, vi := range in.Variants {
		vv[i] = variationVariant{Name: vi.SKU, Options: vi.Options}
	}
	variation, err := s.resolveVariation(ctx, in.CategoryID, in.VariationAxes, vv)
	if err != nil {
		return nil, err
	}

	// Which vintage of the form these answers were checked against. Stamped
	// only when there were answers — a product with no typed values has not
	// been validated against anything, and claiming a version for it would
	// tell a later reconciliation pass to skip it.
	schemaVersion := 0
	if len(attrSets) > 0 {
		schemaVersion, err = s.publishedSchemaVersion(ctx)
		if err != nil {
			return nil, err
		}
	}

	productType := in.ProductType
	if productType == "" {
		productType = "physical"
	}

	p := &postgres.Product{
		SellerID:            in.SellerID,
		CategoryID:          in.CategoryID,
		BrandID:             in.BrandID,
		TaxClassID:          in.TaxClassID,
		Title:               in.Title,
		ShortTitle:          in.ShortTitle,
		Slug:                uniqueSlug(slugify(in.Title)),
		Description:         in.Description,
		ShortDescription:    in.ShortDescription,
		BrandName:           in.BrandName,
		ManufacturerName:    in.ManufacturerName,
		ProductType:         productType,
		Condition:           coalesceStr(in.Condition, "new"),
		Status:              "draft",
		Visibility:          "public",
		ApprovalStatus:      "draft",
		PrimaryImageMediaID: in.PrimaryImageMediaID,
		VideoMediaID:        in.VideoMediaID,
		WeightGrams:         in.WeightGrams,
		LengthCm:            in.LengthCm,
		WidthCm:             in.WidthCm,
		HeightCm:            in.HeightCm,
		CountryOfOrigin:     in.CountryOfOrigin,
		WarrantyInfo:        in.WarrantyInfo,
		ReturnPolicyType:    coalesceStr(in.ReturnPolicyType, "7_days"),
		ReturnPolicyDays:    coalesceInt(in.ReturnPolicyDays, 7),
		HSNCode:             in.HSNCode,
		SearchKeywords:      in.SearchKeywords,
		MetaTitle:           in.MetaTitle,
		MetaDescription:     in.MetaDescription,
		SchemaVersion:       schemaVersion,
	}

	// ─── ONE TRANSACTION, AND WHY ────────────────────────────
	//
	// This used to be: insert the product; then, in a loop, insert each
	// variant and upsert its inventory — three separate statements, none of
	// them in a transaction, with the inventory error swallowed into a
	// slog.Warn.
	//
	// Every early return in that loop leaked a half-built product. A create
	// that failed on the second variant's duplicate SKU left the product and
	// the first variant standing, invisible to the seller, who was shown an
	// error and reasonably assumed nothing had happened. Worse, the swallowed
	// inventory failure produced a variant with NO stock row: every
	// availability read in this service derives from `inventory_items`, so
	// the listing rendered, went into a cart, and was refused at checkout —
	// with a 200 on the create and no error line anywhere.
	//
	// Now the product, its variants, their inventory rows and its attribute
	// values are one unit. Either the whole listing exists or none of it
	// does, and the inventory insert is a hard failure rather than a warning.
	variants := make([]postgres.NewVariant, 0, len(in.Variants))
	for idx, vi := range in.Variants {
		v := &postgres.ProductVariant{
			ProductID:    p.ID,
			SKU:          vi.SKU,
			Option1Name:  vi.Option1Name,
			Option1Value: vi.Option1Value,
			Option2Name:  vi.Option2Name,
			Option2Value: vi.Option2Value,
			// Rupees are the mirror; paise are the truth. Both are written so
			// the analytics readers that still scan the NUMERIC columns keep
			// working through the dual-write window.
			MRP:                 float64(vi.MRPMinor) / 100.0,          // money-exempt: NUMERIC mirror of mrp_minor
			SellingPrice:        float64(vi.SellingPriceMinor) / 100.0, // money-exempt: NUMERIC mirror of selling_price_minor
			CostPrice:           rupeeMirror(vi.CostPriceMinor),
			MRPMinorIn:          &vi.MRPMinor,
			SellingPriceMinorIn: &vi.SellingPriceMinor,
			CostPriceMinorIn:    vi.CostPriceMinor,
			CurrencyCode:        "INR",
			Status:              "active",
		}
		nv := postgres.NewVariant{Variant: v, StockQty: vi.StockQty}
		if variation != nil {
			// Zipped back on by INDEX, which is safe because
			// resolveVariation returns a slice parallel to the one it was
			// given and refuses outright rather than returning a short one.
			nv.Options = variation.PerVariant[idx]
		}
		variants = append(variants, nv)
	}

	np := postgres.NewProduct{
		Product:    p,
		Variants:   variants,
		Attributes: attrSets,
	}
	if variation != nil {
		np.Axes = variation.Axes
	}
	if err := s.store.CreateProductAtomic(ctx, np); err != nil {
		return nil, err
	}

	// No event here any more, and its absence is the fix.
	//
	// This published "commerce.product.created". Nothing has ever consumed
	// it: search-service's consumer listens for events.ProductListed — the
	// literal "ProductListed" — so the two strings have never matched and
	// product search has never seen a live event. But the name was only the
	// visible half of the mistake. A create is the wrong MOMENT to tell a
	// search index about: every path into this function writes
	// status='draft', approval_status='draft' (see the struct above), so an
	// index driven by creation would index drafts, index listings a
	// moderator later rejects, and never hear about an approved listing
	// being taken down.
	//
	// The events search actually needs are commerce.product.published and
	// commerce.product.unpublished, fired from the transitions that move a
	// listing in and out of buyers' view. See internal/service/searchdoc.go.
	//
	// A grep for a "product created" event that finds nothing here should
	// find this comment instead.
	return p, nil
}

func (s *Service) GetProduct(ctx context.Context, productID uuid.UUID) (*postgres.Product, []*postgres.ProductVariant, error) {
	p, err := s.store.GetProductByID(ctx, productID)
	if err != nil {
		return nil, nil, err
	}
	variants, err := s.store.GetVariantsByProduct(ctx, productID)
	if err != nil {
		return nil, nil, err
	}
	go s.adjustProductViewCount(context.Background(), productID)
	s.hydrateProductImages(ctx, []*postgres.Product{p})
	s.HydrateBestCoupons(ctx, []*postgres.Product{p})
	return p, variants, nil
}

func (s *Service) ListCategories(ctx context.Context) ([]*postgres.ProductCategory, error) {
	return s.store.ListCategories(ctx)
}

func (s *Service) ListSellerProducts(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*postgres.Product, error) {
	// HP3: pagination clamped (default 20, max 200).
	limit, offset = clampListPagination(limit, offset)
	// publicOnly: this is the storefront. The route takes a seller id from the
	// URL and requires no authentication, so it must never show draft or
	// moderation-rejected products. Sellers read their own full catalogue
	// through ListMyProducts, which resolves the seller from the caller.
	products, _, err := s.store.ListSellerProducts(ctx, sellerID, "", true, limit, offset)
	s.hydrateProductImages(ctx, products)
	s.HydrateBestCoupons(ctx, products)
	return products, err
}

// ListMyProducts is the seller's own catalogue — every status, including
// drafts and moderation rejections, because the seller needs to see and fix
// them. The seller id comes from the caller's profile, never from the URL.
func (s *Service) ListMyProducts(ctx context.Context, actorUserID uuid.UUID, status string, limit, offset int) ([]*postgres.Product, int, error) {
	seller, err := s.GetSellerProfile(ctx, actorUserID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) {
			return nil, 0, ErrNoSellerProfile
		}
		return nil, 0, err
	}
	limit, offset = clampListPagination(limit, offset)
	products, total, err := s.store.ListSellerProducts(ctx, seller.ID, status, false, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	s.hydrateProductImages(ctx, products)
	// The variant ids, on the seller's own list only. The public summary
	// carries default_variant_id and nothing else, and the stock/price
	// screens address a VARIANT — so a two-colour listing had one id to
	// edit with and one it could not reach.
	ids := make([]uuid.UUID, 0, len(products))
	for _, p := range products {
		ids = append(ids, p.ID)
	}
	variants, err := s.store.SellerVariantsForProducts(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	for _, p := range products {
		if rows := variants[p.ID]; len(rows) > 0 {
			p.Variants = rows
		} else {
			p.Variants = []postgres.SellerVariantRow{}
		}
	}
	return products, total, nil
}

// ListProducts returns the customer-facing product catalog: published +
// approved only. Optional category filter and title query. Returns total so
// the UI can paginate. Legacy offset/limit shape — prefer ListProductsFiltered
// at scale.
func (s *Service) ListProducts(ctx context.Context, categoryID *uuid.UUID, query string, limit, offset int) ([]*postgres.Product, int, error) {
	products, total, err := s.store.ListProducts(ctx, categoryID, query, limit, offset)
	s.hydrateProductImages(ctx, products)
	s.HydrateBestCoupons(ctx, products)
	return products, total, err
}

// ListProductsFilteredResult is the cursor-paged catalog response. NextCursor
// is empty on the last page.
type ListProductsFilteredResult struct {
	Items      []*postgres.Product
	NextCursor string
}

// ListProductsFiltered is the scale-friendly variant: keyset pagination
// (O(1) regardless of page depth) plus a rich filter set — price range,
// minimum rating, seller, in-stock. Wraps the store call with input
// hardening (limit clamp, query trim).
func (s *Service) ListProductsFiltered(ctx context.Context, f postgres.ProductFilter) (*ListProductsFilteredResult, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	items, next, err := s.store.ListProductsFiltered(ctx, f)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []*postgres.Product{}
	}
	s.hydrateProductImages(ctx, items)
	s.HydrateBestCoupons(ctx, items)
	return &ListProductsFilteredResult{Items: items, NextCursor: next}, nil
}

// ─── Variant CRUD (commerce TODO H#5) ─────────────────────────────────

// ListProductVariants returns every variant for a product including
// archived ones — public endpoint, no ownership check needed for read.
func (s *Service) ListProductVariants(ctx context.Context, productID uuid.UUID) ([]*postgres.ProductVariant, error) {
	return s.store.GetVariantsByProduct(ctx, productID)
}

// ErrStockNegative is returned when a variant is added with a negative
// opening stock. Zero is a real answer ("not in yet"); a negative is not.
var ErrStockNegative = errors.New("commerce: stock_qty cannot be negative")

// AddProductVariant creates a new variant on an existing product, with its
// opening stock.
// Authorization: actor must be the owning seller of the product. The
// SKU must be unique per seller (the import path enforces this; here
// the DB UNIQUE constraint on (sku, seller_id) is the final guard).
func (s *Service) AddProductVariant(ctx context.Context, actorID, productID uuid.UUID, v *postgres.ProductVariant, stockQty int) (*postgres.ProductVariant, error) {
	// Input validation before anything is read: a selling price above the
	// MRP is wrong whoever sends it (see ErrSellingAboveMRP).
	if v.MRPMinorIn != nil && v.SellingPriceMinorIn != nil {
		if err := priceWithinMRP(*v.MRPMinorIn, *v.SellingPriceMinorIn); err != nil {
			return nil, err
		}
	}
	product, err := s.store.GetProductByID(ctx, productID)
	if err != nil {
		return nil, err
	}
	if product == nil {
		return nil, postgres.ErrProductNotFound
	}
	// Verify the actor owns this product's seller account.
	seller, err := s.GetSellerProfile(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if seller == nil || seller.ID != product.SellerID {
		return nil, ErrNotProductOwner
	}
	// A price, in whichever shape the caller sent it.
	//
	// The HTTP body no longer marks the rupee floats `required` — it could
	// not, once paise became an accepted shape — so the "is there a price at
	// all?" check moves here, where it applies to internal callers too. A
	// variant with no positive selling price is not a cheap variant, it is a
	// listing the buyer can take for nothing.
	if v.SellingPriceMinorIn == nil || *v.SellingPriceMinorIn <= 0 {
		return nil, fmt.Errorf("%w: selling_price_minor", postgres.ErrPriceNotPositive)
	}
	if v.MRPMinorIn == nil || *v.MRPMinorIn <= 0 {
		return nil, fmt.Errorf("%w: mrp_minor", postgres.ErrPriceNotPositive)
	}
	if stockQty < 0 {
		return nil, ErrStockNegative
	}
	v.ProductID = productID
	if v.Status == "" {
		v.Status = "active"
	}
	if v.CurrencyCode == "" {
		v.CurrencyCode = "INR"
	}
	// The variant and its stock row, in one transaction.
	//
	// POST /products writes the two together for the variants it creates;
	// this route inserted the variant and then upserted a stock row of ZERO
	// — the request carried no stock figure at all, so a seller adding
	// "size XL, 40 in stock" to a live listing got a variant with nothing to
	// sell and no error, and the inventory failure was swallowed into a log
	// line. The opening stock now travels with the variant, exactly as it
	// does on create.
	if err := s.store.CreateVariantWithStock(ctx, v, product.SellerID, stockQty); err != nil {
		return nil, err
	}
	// Return what was STORED, not what was built in memory.
	//
	// The in-memory value carries the caller's paise in the `*MinorIn`
	// fields, which are `json:"-"` — they are inputs. So the 201 body was
	// the one variant response in the service with no `mrp_minor`,
	// `selling_price_minor` or `available_qty` on it, and a client that
	// rendered the create response got ₹0.00 and out-of-stock for the
	// variant it had just priced. Re-reading also picks up the inventory row
	// created a line above. UpdateProductVariant already works this way.
	stored, err := s.store.GetVariantByID(ctx, v.ID)
	if err != nil {
		// The variant exists; only the read-back failed. Returning the
		// in-memory value is worse than useless here — it is the shape that
		// caused the defect — so report the failure.
		return nil, fmt.Errorf("variant created but could not be read back: %w", err)
	}
	return stored, nil
}

// UpdateProductVariant patches an existing variant. Authorization same
// as AddProductVariant — actor must own the parent product's seller.
//
// Four rules on top of ownership, all decided against what the variant
// HOLDS rather than what the body carries (see checkVariantPatch in
// reviewedcontent.go):
//
//   - the SKU is editable while the listing is a draft and locked once it
//     has been approved (ErrSKULocked). It used to be dropped silently;
//   - an option or image change on an APPROVED listing is a reviewed-content
//     change: refused with RevalidationRequiredError until the caller
//     acknowledges it with `ack`, then applied and the listing returned to
//     review in the same transaction;
//   - a PRICE change goes live at once (the MSeller rule: price and stock
//     changes do not go back through checks) but is validated: never above
//     the MRP (ErrSellingAboveMRP), positive and whole paise (the store);
//   - `status` must be one of the values the schema allows.
//
// Returns whether the listing was sent back to review, so the handler can
// say so in the same response as the success.
func (s *Service) UpdateProductVariant(ctx context.Context, actorID, variantID uuid.UUID, updates map[string]any, ack bool) (*postgres.ProductVariant, bool, error) {
	variant, err := s.store.GetVariantByID(ctx, variantID)
	if err != nil {
		return nil, false, err
	}
	if variant == nil {
		return nil, false, postgres.ErrProductNotFound
	}
	product, err := s.store.GetProductByID(ctx, variant.ProductID)
	if err != nil {
		return nil, false, err
	}
	seller, err := s.GetSellerProfile(ctx, actorID)
	if err != nil {
		return nil, false, err
	}
	if seller == nil || seller.ID != product.SellerID {
		return nil, false, ErrNotProductOwner
	}

	changed, err := checkVariantPatch(product, variant, updates)
	if err != nil {
		return nil, false, err
	}

	revalidate, err := gateApprovedEdit(product, changed, ack)
	if err != nil {
		return nil, false, err
	}
	if err := s.store.UpdateVariant(ctx, variantID, updates, revalidate); err != nil {
		return nil, false, err
	}
	if revalidate {
		s.publish(ctx, "commerce.product.updated", map[string]any{
			"product_id": product.ID, "seller_id": product.SellerID, "revalidated": true,
		})
		// The listing has left sale; search must stop offering it. Same
		// transition PATCH /products takes, see UpdateProduct.
		s.publishProductVisibility(ctx, product.ID)
	}
	stored, err := s.store.GetVariantByID(ctx, variantID)
	return stored, revalidate, err
}

// ErrVariantFieldRequired is returned when a PATCH /variants/:id body blanks
// a field the row cannot do without.
var ErrVariantFieldRequired = errors.New("commerce: this variant field cannot be blank")

// ArchiveProductVariant soft-deletes (status=archived) a variant.
// Deleting variants is intentionally not supported — orders + cart
// items reference variants by ID and need to keep resolving.
func (s *Service) ArchiveProductVariant(ctx context.Context, actorID, variantID uuid.UUID) error {
	variant, err := s.store.GetVariantByID(ctx, variantID)
	if err != nil {
		return err
	}
	if variant == nil {
		return postgres.ErrProductNotFound
	}
	product, err := s.store.GetProductByID(ctx, variant.ProductID)
	if err != nil {
		return err
	}
	seller, err := s.GetSellerProfile(ctx, actorID)
	if err != nil {
		return err
	}
	if seller == nil || seller.ID != product.SellerID {
		return ErrNotProductOwner
	}
	return s.store.ArchiveVariant(ctx, variantID)
}

// ─── Product Media + Attributes (Phase 3.1) ──────────────────

// assertProductSeller verifies the actor's seller account owns the
// product. Used by the media + attributes mutation endpoints to keep
// sellers out of each other's catalogs.
func (s *Service) assertProductSeller(ctx context.Context, productID, actorUserID uuid.UUID) error {
	product, err := s.store.GetProductByID(ctx, productID)
	if err != nil || product == nil {
		return fmt.Errorf("product not found")
	}
	seller, err := s.GetSellerProfile(ctx, actorUserID)
	if err != nil || seller == nil {
		return ErrNotOrderOwner // reused — caller maps to 403
	}
	if product.SellerID != seller.ID {
		return ErrNotOrderOwner
	}
	return nil
}

// AddProductMedia attaches a media-service asset to a product's gallery.
//
// An append to an approved listing's gallery is a reviewed-content change:
// refused until `ack`, then applied and the listing returned to review. See
// reviewedcontent.go.
func (s *Service) AddProductMedia(ctx context.Context, productID, actorUserID, mediaID uuid.UUID, mediaType string, sortOrder int, ack bool) ([]postgres.ProductMedia, error) {
	current, err := s.store.ProductMediaIDs(ctx, productID)
	if err != nil {
		return nil, err
	}
	product, revalidate, err := s.galleryEditGate(ctx, productID, actorUserID, append(current, mediaID), ack)
	if err != nil {
		return nil, err
	}
	// Owning the product is not owning the media. This route checked the
	// first and not the second, so any seller could hang any asset in the
	// system off their own gallery.
	//
	// KindAny: this endpoint carries its own mediaType for images and video
	// alike, and constraining it here would reject a legitimate video gallery
	// entry. Ownership, readiness and moderation are still all enforced.
	if err := s.verifyMedia(ctx, actorUserID, media.KindAny, &mediaID); err != nil {
		return nil, err
	}
	if err := s.store.AddProductMedia(ctx, productID, mediaID, mediaType, sortOrder, revalidate); err != nil {
		return nil, err
	}
	if revalidate {
		s.afterGalleryRevalidation(ctx, product)
	}
	return s.store.ListProductMedia(ctx, productID)
}

// ListProductMedia is a public read used by the product detail page.
func (s *Service) ListProductMedia(ctx context.Context, productID uuid.UUID) ([]postgres.ProductMedia, error) {
	return s.store.ListProductMedia(ctx, productID)
}

// SetProductAttributes replaces the product's spec block in one call.
func (s *Service) SetProductAttributes(ctx context.Context, productID, actorUserID uuid.UUID, attrs []postgres.ProductAttribute) ([]postgres.ProductAttribute, error) {
	if err := s.assertProductSeller(ctx, productID, actorUserID); err != nil {
		return nil, err
	}
	if err := s.store.SetProductAttributes(ctx, productID, attrs); err != nil {
		return nil, err
	}
	return s.store.GetProductAttributes(ctx, productID)
}

// GetProductAttributes returns the spec block — public.
func (s *Service) GetProductAttributes(ctx context.Context, productID uuid.UUID) ([]postgres.ProductAttribute, error) {
	return s.store.GetProductAttributes(ctx, productID)
}
