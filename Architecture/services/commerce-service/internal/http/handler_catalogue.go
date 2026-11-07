// Catalogue route handlers: categories, product read and create, the
// storefront product listing, and product media and attribute routes. Moved
// out of handler.go unchanged.

package http

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Catalog handlers ────────────────────────────────────────────

// ListCategories GET /v1/commerce/categories — the strip above the grid.
//
// Each row now carries `image_url`/`thumbnail_url` (resolved from the
// category's own artwork in one media batch) and `product_count` (live
// products behind it). Without the count, the strip cannot grey out a
// category that would open onto nothing, which is what made
// `?category_id=` look broken — the filter worked, and every seeded
// category was empty.
//
// `?tree=true` nests children under their parents and adds `is_listable`,
// which is what a category PICKER needs — a flat list cannot show that
// "Textbooks" lives under "Books", and a seller choosing from it picks the
// browse heading their listing may not sit on.
//
// The flat answer is BYTE-IDENTICAL when the parameter is absent. The
// storefront and the phone both read this route and neither asked for a
// different shape, so the tree is a second response behind a flag rather than
// an extra field on the existing one — adding `children: []` to every row
// would change every byte of a payload two shipped clients decode.
func (h *Handler) ListCategories(c *gin.Context) {
	if c.Query("tree") == "true" {
		depth, _ := strconv.Atoi(c.Query("depth"))
		roots, err := h.svc.CategoryTree(c.Request.Context(), false, depth)
		if err != nil {
			handleErr(c, err)
			return
		}
		if roots == nil {
			roots = []*postgres.CategoryTreeNode{}
		}
		api.JSON(c.Writer, http.StatusOK, roots, nil)
		return
	}
	cats, err := h.svc.ListCategoryCards(c.Request.Context())
	if err != nil {
		handleErr(c, err)
		return
	}
	if cats == nil {
		// `null` is not an empty list. A client decoding this into an array
		// type gets a decode failure rather than "no categories", which is
		// how an unseeded table read as a broken endpoint. Migration 023
		// seeds the taxonomy; this makes the empty case honest regardless.
		cats = []*postgres.CategoryCard{}
	}
	api.JSON(c.Writer, http.StatusOK, cats, nil)
}

func (h *Handler) GetProduct(c *gin.Context) {
	id, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	if !h.productReadable(c, id) {
		return
	}
	p, variants, err := h.svc.GetProduct(c.Request.Context(), id)
	if err != nil {
		handleErr(c, err)
		return
	}
	// The heart state, and the gallery, on the one screen that needs both.
	//
	// `media` is in the detail body rather than behind a second request to
	// GET …/media because the detail page draws the carousel immediately: a
	// separate call means the hero image arrives after the price, and the
	// layout jumps. The dedicated route stays for the seller's editor.
	h.svc.MarkFavourites(c.Request.Context(), optionalUserID(c), []*postgres.Product{p})
	// The shopper's own like / dislike (signed in only; absent otherwise).
	h.svc.MarkViewerReaction(c.Request.Context(), optionalUserID(c), p)
	gallery, err := h.svc.ProductMedia(c.Request.Context(), id)
	if err != nil {
		handleErr(c, err)
		return
	}
	// The specifications block, which this endpoint has never returned.
	//
	// Detail was product + variants + media, so every category-specific fact a
	// seller entered — author, page count, weight — was stored and then
	// visible nowhere. A client wanting it had to call
	// GET …/attributes separately, which returns the untyped legacy rows and
	// no labels, so it could not draw a spec table from them either.
	//
	// Read from the typed rows, in group order, with codes beside labels. On
	// a product that has no typed values this is an empty list, which is the
	// same shape as before rather than a new failure mode.
	attributes, err := h.svc.ProductAttributeValues(c.Request.Context(), id)
	if err != nil {
		handleErr(c, err)
		return
	}
	// The try-on descriptor, when this product has one.
	//
	// In the detail body rather than behind its own request because the
	// "Try on" action sits beside the buy strip and must be drawn in the
	// first paint: a second call means the button appears a moment after the
	// price, which reads as a glitch on the one control the viewer is being
	// invited to trust with their camera.
	//
	// Absent for every product without a descriptor, which is nearly all of
	// them, so the field's presence is itself the capability signal and a
	// client that has never heard of try-on is unaffected. A read failure is
	// soft for the same reason the gallery's is: a product page that will
	// not load because the AR metadata is unavailable is worse than a page
	// with no try-on button.
	tryOn, tryOnErr := h.svc.ProductTryOn(c.Request.Context(), id)
	if tryOnErr != nil {
		logTryOnReadFailure(c.Request.Context(), id, tryOnErr)
		tryOn = nil
	}
	// Onto the product itself, not beside it. See Product.TryOn: as a
	// sibling of `product` the client never found it and the Try-on control
	// stayed hidden on a product that was correctly set up.
	if p != nil {
		p.TryOn = tryOn
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"product": p, "variants": variants, "media": gallery,
		"attributes": attributes,
	}, nil)
}

type createProductReq struct {
	CategoryID       *uuid.UUID `json:"category_id"`
	BrandID          *uuid.UUID `json:"brand_id"`
	TaxClassID       *uuid.UUID `json:"tax_class_id"`
	Title            string     `json:"title" binding:"required"`
	ShortTitle       *string    `json:"short_title"`
	Description      *string    `json:"description"`
	ShortDescription *string    `json:"short_description"`
	BrandName        *string    `json:"brand_name"`
	ManufacturerName *string    `json:"manufacturer_name"`
	ProductType      string     `json:"product_type"`
	Condition        string     `json:"condition"`
	ReturnPolicyType string     `json:"return_policy_type"`
	ReturnPolicyDays int        `json:"return_policy_days"`
	HSNCode          *string    `json:"hsn_code"`
	// Logistics + legal-metrology (Phase 3.1 — schema has the columns).
	PrimaryImageMediaID *uuid.UUID         `json:"primary_image_media_id"`
	VideoMediaID        *uuid.UUID         `json:"video_media_id"`
	WeightGrams         *int               `json:"weight_grams"`
	LengthCm            *float64           `json:"length_cm"`
	WidthCm             *float64           `json:"width_cm"`
	HeightCm            *float64           `json:"height_cm"`
	CountryOfOrigin     *string            `json:"country_of_origin"`
	WarrantyInfo        *string            `json:"warranty_info"`
	SearchKeywords      []string           `json:"search_keywords"`
	MetaTitle           *string            `json:"meta_title"`
	MetaDescription     *string            `json:"meta_description"`
	Variants            []createVariantReq `json:"variants" binding:"required,min=1"`

	// Attributes are the category-specific answers, in the same shape the
	// PATCH route takes: [{"code": "pages", "value": 328}, …].
	//
	// Every value is validated against the category's published schema before
	// anything is written. Completeness is NOT required — a create that
	// demanded every mandatory field would make drafts impossible, and the
	// seller would type placeholders into fourteen controls to get past it.
	// The submit-for-review gate is where "is this finished?" is asked.
	Attributes []service.AttributeValueInput `json:"attributes"`

	// VariationAxes are the attributes this product varies on, in order —
	// `[{"code":"size","position":1},{"code":"colour","position":2}]`.
	// Absent or empty for a product that does not vary, which is what every
	// client written before this field sent and what most listings are.
	//
	// When present, every variant must carry exactly one `options` entry per
	// axis, and the VALUE must be a code the schema knows. Free text is
	// refused; see internal/service/variationaxes.go for why that refusal is
	// the whole point rather than an inconvenience.
	VariationAxes []service.VariationAxisInput `json:"variation_axes"`
}

// createVariantReq accepts money in PAISE.
//
// The float fields are the legacy shape and are still read, because this route
// predates the minor-unit migration and an older client must not start
// failing. But they are the FALLBACK now, not the contract: `*_minor` wins
// whenever it is present.
//
// This is the seller's money-entry point — the one place a human types a price
// that every subsequent sale is charged at. Taking it as a float means
// `1299.99` arrives as 1299.9899999999998 and the stored paise depend on which
// way `math.Round` happens to fall. One paise on one listing is nothing; the
// same reasoning applied at checkout is what the whole minor-unit migration
// exists to remove, and leaving it in place at the point of ENTRY makes every
// downstream figure exact about a number that was already wrong.
type createVariantReq struct {
	SKU          string  `json:"sku" binding:"required"`
	Option1Name  *string `json:"option_1_name"`
	Option1Value *string `json:"option_1_value"`
	Option2Name  *string `json:"option_2_name"`
	Option2Value *string `json:"option_2_value"`
	Option3Name  *string `json:"option_3_name"`
	Option3Value *string `json:"option_3_value"`

	MRPMinor          *int64 `json:"mrp_minor"`
	SellingPriceMinor *int64 `json:"selling_price_minor"`
	CostPriceMinor    *int64 `json:"cost_price_minor"`

	// money-exempt: the legacy rupee shape, read only when the corresponding
	// *_minor field is absent. Retained so a client written before the minor
	// migration keeps working; every new client sends paise.
	MRP float64 `json:"mrp"`
	// money-exempt: legacy rupee shape, superseded by selling_price_minor.
	SellingPrice float64 `json:"selling_price"`
	// money-exempt: legacy rupee shape, superseded by cost_price_minor.
	CostPrice *float64 `json:"cost_price"`

	StockQty int `json:"stock_qty"`

	// Options are this variant's values on the product's declared axes —
	// `[{"code":"size","value":"l"}]`. The legacy `option_N_name` /
	// `option_N_value` pairs above are still accepted and still written, but
	// they are free text and nothing relates one variant's slots to
	// another's; a product that sends `variation_axes` should send these
	// instead and let the derived columns be derived.
	Options []service.VariantOptionInput `json:"options"`
}

// minorOrRupees resolves one money field, preferring paise.
//
// A rupee float is converted with the same rounding the store uses, so a
// legacy client and a modern one that send the same price get the same paise.
func minorOrRupees(minor *int64, rupees float64) int64 {
	if minor != nil {
		return *minor
	}
	return int64(math.Round(rupees * 100))
}

func (h *Handler) CreateProduct(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req createProductReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	seller, sellerOK := h.sellerForCaller(c, userID)
	if !sellerOK {
		return
	}

	// A GST class, or nothing is created.
	//
	// `tax_class_id` was optional, and a product without one is not merely
	// untaxed — it is UNSELLABLE. Checkout resolves the rate under a row lock
	// and refuses with PRODUCT_TAX_UNCONFIGURED when there is none, so the
	// listing went live, appeared in search, sat in a buyer's cart and failed
	// at the last step with an error the seller never saw.
	//
	// Refused rather than defaulted: 18% on a 5% item overcharges every buyer,
	// and 0% on an 18% item leaves the seller owing GST they never collected.
	// The rate is a fact about the goods that only the seller knows.
	if req.TaxClassID == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"TAX_CLASS_REQUIRED",
			"a GST rate is required; GET /v1/commerce/tax-classes lists the options", nil)
		return
	}

	variants := make([]service.CreateVariantInput, len(req.Variants))
	for i, v := range req.Variants {
		variants[i] = service.CreateVariantInput{
			SKU:               v.SKU,
			Option1Name:       v.Option1Name,
			Option1Value:      v.Option1Value,
			Option2Name:       v.Option2Name,
			Option2Value:      v.Option2Value,
			Option3Name:       v.Option3Name,
			Option3Value:      v.Option3Value,
			MRPMinor:          minorOrRupees(v.MRPMinor, v.MRP),
			SellingPriceMinor: minorOrRupees(v.SellingPriceMinor, v.SellingPrice),
			CostPriceMinor:    costMinor(v),
			StockQty:          v.StockQty,
			Options:           v.Options,
		}
	}

	p, err := h.svc.CreateProduct(c.Request.Context(), service.CreateProductInput{
		SellerID:            seller.ID,
		SellScope:           seller.SellScope,
		ActorUserID:         userID,
		CategoryID:          req.CategoryID,
		BrandID:             req.BrandID,
		TaxClassID:          req.TaxClassID,
		Title:               req.Title,
		ShortTitle:          req.ShortTitle,
		Description:         req.Description,
		ShortDescription:    req.ShortDescription,
		BrandName:           req.BrandName,
		ManufacturerName:    req.ManufacturerName,
		ProductType:         req.ProductType,
		Condition:           req.Condition,
		ReturnPolicyType:    req.ReturnPolicyType,
		ReturnPolicyDays:    req.ReturnPolicyDays,
		HSNCode:             req.HSNCode,
		PrimaryImageMediaID: req.PrimaryImageMediaID,
		VideoMediaID:        req.VideoMediaID,
		WeightGrams:         req.WeightGrams,
		LengthCm:            req.LengthCm,
		WidthCm:             req.WidthCm,
		HeightCm:            req.HeightCm,
		CountryOfOrigin:     req.CountryOfOrigin,
		WarrantyInfo:        req.WarrantyInfo,
		SearchKeywords:      req.SearchKeywords,
		MetaTitle:           req.MetaTitle,
		MetaDescription:     req.MetaDescription,
		Variants:            variants,
		Attributes:          req.Attributes,
		VariationAxes:       req.VariationAxes,
	})
	if err != nil {
		// The product-write mapper, not the generic one: an unknown category
		// and a refused attribute value both originate here and both need
		// their own code, and the attribute refusal has to carry per-field
		// detail a flat message cannot.
		writeProductWriteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, p, nil)
}

// ─── Product Media + Attributes (Phase 3.1) ──────────────────

// AddProductMedia POST /v1/commerce/products/:productId/media — seller only,
// must own the product AND own every asset.
//
// Two body shapes, and the difference is deliberate:
//
//	{"media_ids":["…","…"]}   SETS the gallery to exactly this, in this
//	                          order. The first is the cover. Max 8.
//	{"media_id":"…"}          the original single-append, retained for the
//	                          existing caller.
//
// `media_ids` is a replace and not an append because the client hands up the
// gallery it wants: an additive endpoint means a seller who removed a
// photograph in the editor and pressed save is still showing it. Sending both
// is refused rather than resolved — guessing which one the caller meant is
// how a "save" silently discards seven images.
func (h *Handler) AddProductMedia(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	var req setProductMediaReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	switch {
	case len(req.MediaIDs) > 0 && req.MediaID != nil:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"send media_ids (which sets the whole gallery) or media_id (which appends one), not both", nil)
		return

	case len(req.MediaIDs) > 0:
		out, err := h.svc.SetProductMedia(c.Request.Context(), productID, userID, req.MediaIDs, revalidateAck(c, req.Revalidate))
		if err != nil {
			writeStorefrontError(c, err)
			return
		}
		api.JSON(c.Writer, http.StatusOK, gin.H{"items": out}, nil)

	case req.MediaID != nil:
		if _, err := h.svc.AddProductMedia(c.Request.Context(), productID, userID,
			*req.MediaID, req.MediaType, req.SortOrder, revalidateAck(c, req.Revalidate)); err != nil {
			if errors.Is(err, service.ErrNotOrderOwner) {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "not your product", nil)
				return
			}
			writeStorefrontError(c, err)
			return
		}
		// The RESOLVED gallery, not the raw rows: the caller that appended an
		// image wants to draw it, and bare media UUIDs are what made the
		// original version of this route unusable from a phone.
		out, err := h.svc.ProductMedia(c.Request.Context(), productID)
		if err != nil {
			handleErr(c, err)
			return
		}
		api.JSON(c.Writer, http.StatusOK, gin.H{"items": out}, nil)

	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"media_ids (or media_id) is required", nil)
	}
}

// ListProductMedia GET /v1/commerce/products/:productId/media — public.
//
// Returns RESOLVED URLs, in one media batch. It used to return the raw
// `product_media` rows — a media UUID, a sort order and a timestamp — which
// no client could draw anything from, so the route existed and no screen
// called it.
func (h *Handler) ListProductMedia(c *gin.Context) {
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	if !h.productReadable(c, productID) {
		return
	}
	out, err := h.svc.ProductMedia(c.Request.Context(), productID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": out}, nil)
}

type setProductAttrsReq struct {
	Attributes []productAttrPayload `json:"attributes"`
}

type productAttrPayload struct {
	Name  string  `json:"name" binding:"required"`
	Value string  `json:"value" binding:"required"`
	Unit  *string `json:"unit"`
}

// SetProductAttributes PUT /v1/commerce/products/:productId/attributes —
// seller only. Replaces the product's structured spec block in one call.
func (h *Handler) SetProductAttributes(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	var req setProductAttrsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	attrs := make([]postgres.ProductAttribute, 0, len(req.Attributes))
	for _, a := range req.Attributes {
		attrs = append(attrs, postgres.ProductAttribute{
			Name: a.Name, Value: a.Value, Unit: a.Unit,
		})
	}
	out, err := h.svc.SetProductAttributes(c.Request.Context(), productID, userID, attrs)
	if err != nil {
		if errors.Is(err, service.ErrNotOrderOwner) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "not your product", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "SET_ATTRS_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"attributes": out}, nil)
}

// GetProductAttributes GET /v1/commerce/products/:productId/attributes — public.
func (h *Handler) GetProductAttributes(c *gin.Context) {
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	if !h.productReadable(c, productID) {
		return
	}
	out, err := h.svc.GetProductAttributes(c.Request.Context(), productID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"attributes": out}, nil)
}

// ListProducts is the customer-facing global product browse endpoint.
//
//	GET /v1/commerce/products
//	  ?category={uuid}     optional
//	  &q={text}            optional title search
//	  &min_price={num}     optional inclusive lower bound
//	  &max_price={num}     optional inclusive upper bound
//	  &min_rating={1..5}   optional
//	  &seller={uuid}       optional storefront filter
//	  &in_stock=true       optional, restrict to variants with stock
//	  &limit={1..100}      default 20
//	  &cursor={opaque}     keyset cursor (preferred); omit on first page
//	  &offset={int}        legacy offset; ignored when cursor is set
//
// Cursor-paged response: { items: [...], next_cursor: "..." }.
// next_cursor is empty when there are no more pages. Cursor pagination
// stays O(1) at any catalog depth — required for billions-of-users scale.
//
// When neither cursor nor offset is supplied, returns the first page
// cursor-paginated. Legacy offset is kept for admin grids that need a
// total count.
func (h *Handler) ListProducts(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Both spellings. The store has always filtered on category and the
	// handler has always read `?category=`, but every client, the category
	// strip and the documented contract say `?category_id=` — so the filter
	// worked and nothing that used it ever reached the code, which read as
	// "there is nothing behind the categories".
	var categoryID *uuid.UUID
	cat := c.Query("category_id")
	if cat == "" {
		cat = c.Query("category")
	}
	if cat != "" {
		id, err := uuid.Parse(cat)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CATEGORY", "category_id must be a UUID", nil)
			return
		}
		categoryID = &id
	}
	var sellerID *uuid.UUID
	if sel := c.Query("seller"); sel != "" {
		id, err := uuid.Parse(sel)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SELLER", "seller must be a UUID", nil)
			return
		}
		sellerID = &id
	}
	minPrice, _ := strconv.ParseFloat(c.Query("min_price"), 64)
	maxPrice, _ := strconv.ParseFloat(c.Query("max_price"), 64)
	minRating, _ := strconv.ParseFloat(c.Query("min_rating"), 64)
	inStock := strings.EqualFold(c.Query("in_stock"), "true")
	cursor := c.Query("cursor")

	// Legacy offset path — still supported for admin grids that need
	// a total count. New clients should drop the offset param and
	// follow next_cursor.
	if cursor == "" && c.Query("offset") != "" {
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if offset < 0 {
			offset = 0
		}
		products, total, err := h.svc.ListProducts(c.Request.Context(), categoryID, c.Query("q"), limit, offset)
		if err != nil {
			handleErr(c, err)
			return
		}
		if products == nil {
			products = []*postgres.Product{}
		}
		h.svc.MarkFavourites(c.Request.Context(), optionalUserID(c), products)
		api.JSON(c.Writer, http.StatusOK, gin.H{
			"items":  products,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		}, nil)
		return
	}

	result, err := h.svc.ListProductsFiltered(c.Request.Context(), postgres.ProductFilter{
		CategoryID:  categoryID,
		Query:       c.Query("q"),
		MinPrice:    minPrice,
		MaxPrice:    maxPrice,
		MinRating:   minRating,
		SellerID:    sellerID,
		InStockOnly: inStock,
		Limit:       limit,
		Cursor:      cursor,
	})
	if err != nil {
		handleErr(c, err)
		return
	}
	// One favourites query for the page, after the products are known.
	h.svc.MarkFavourites(c.Request.Context(), optionalUserID(c), result.Items)
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items":       result.Items,
		"next_cursor": result.NextCursor,
		"limit":       limit,
	}, nil)
}

// costMinor resolves the optional cost price, preferring paise.
//
// Cost price stays optional: it is the seller's own margin bookkeeping and no
// buyer-facing figure depends on it. Absent means absent, not zero — a
// recorded cost of ₹0.00 would read as "this cost me nothing".
func costMinor(v createVariantReq) *int64 {
	if v.CostPriceMinor != nil {
		return v.CostPriceMinor
	}
	if v.CostPrice == nil {
		return nil
	}
	minor := int64(math.Round(*v.CostPrice * 100))
	return &minor
}
