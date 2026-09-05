package postgres

import (
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors
var (
	ErrAlreadySubmitted  = errors.New("seller application already submitted")
	ErrSellerNotApproved = errors.New("seller account not yet approved")
	ErrProductNotDraft   = errors.New("product is not in draft status")
	ErrProductNotFound   = errors.New("product not found")
)

// ─── Seller ──────────────────────────────────────────────────

type Seller struct {
	ID                uuid.UUID  `db:"id" json:"id"`
	UserID            uuid.UUID  `db:"user_id" json:"user_id"`
	BusinessPageID    *uuid.UUID `db:"business_page_id" json:"business_page_id,omitempty"`
	SellerType        string     `db:"seller_type" json:"seller_type"`
	BusinessType      string     `db:"business_type" json:"business_type"`
	StoreName         string     `db:"store_name" json:"store_name"`
	BrandName         *string    `db:"brand_name" json:"brand_name,omitempty"`
	LegalBusinessName *string    `db:"legal_business_name" json:"legal_business_name,omitempty"`
	OwnerName         *string    `db:"owner_name" json:"owner_name,omitempty"`
	Slug              string     `db:"slug" json:"slug"`
	Description       *string    `db:"description" json:"description,omitempty"`
	Tagline           *string    `db:"tagline" json:"tagline,omitempty"`
	SocialLinksJSON   []byte     `db:"social_links_json" json:"social_links_json,omitempty"`
	LogoMediaID       *uuid.UUID `db:"logo_media_id" json:"logo_media_id,omitempty"`
	BannerMediaID     *uuid.UUID `db:"banner_media_id" json:"banner_media_id,omitempty"`
	Email             string     `db:"email" json:"email"`
	Phone             *string    `db:"phone" json:"phone,omitempty"`
	GSTNumber         *string    `db:"gst_number" json:"gst_number,omitempty"`
	PANNumber         *string    `db:"pan_number" json:"pan_number,omitempty"`
	SupportPhone      *string    `db:"support_phone" json:"support_phone,omitempty"`
	SupportEmail      *string    `db:"support_email" json:"support_email,omitempty"`
	State             *string    `db:"state" json:"state,omitempty"`
	City              *string    `db:"city" json:"city,omitempty"`
	PostalCode        *string    `db:"postal_code" json:"postal_code,omitempty"`
	// Onboarding fields
	Status           string     `db:"status" json:"status"`
	OnboardingStep   int        `db:"onboarding_step" json:"onboarding_step"`
	SubmittedAt      *time.Time `db:"submitted_at" json:"submitted_at,omitempty"`
	ApprovedAt       *time.Time `db:"approved_at" json:"approved_at,omitempty"`
	RejectedAt       *time.Time `db:"rejected_at" json:"rejected_at,omitempty"`
	RejectionReason  *string    `db:"rejection_reason" json:"rejection_reason,omitempty"`
	ChangesRequested *string    `db:"changes_requested" json:"changes_requested,omitempty"`
	SuspensionReason *string    `db:"suspension_reason" json:"suspension_reason,omitempty"`
	// Legacy fields
	VerificationStatus string    `db:"verification_status" json:"verification_status"`
	StoreStatus        string    `db:"store_status" json:"store_status"`
	QualityScore       float64   `db:"quality_score" json:"quality_score"`
	PerformanceTier    string    `db:"performance_tier" json:"performance_tier"`
	AvgRating          float64   `db:"avg_rating" json:"avg_rating"`
	ReviewCount        int       `db:"review_count" json:"review_count"`
	FollowerCount      int       `db:"follower_count" json:"follower_count"`
	TotalProducts      int       `db:"total_products" json:"total_products"`
	TotalOrders        int       `db:"total_orders" json:"total_orders"`
	CreatedAt          time.Time `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time `db:"updated_at" json:"updated_at"`
}

// ─── Seller Document ─────────────────────────────────────────

type SellerDocument struct {
	ID                 uuid.UUID  `db:"id" json:"id"`
	SellerID           uuid.UUID  `db:"seller_id" json:"seller_id"`
	DocumentType       string     `db:"document_type" json:"document_type"`
	DocumentNumber     *string    `db:"document_number" json:"document_number,omitempty"`
	MediaID            uuid.UUID  `db:"media_id" json:"media_id"`
	VerificationStatus string     `db:"verification_status" json:"verification_status"`
	Remarks            *string    `db:"remarks" json:"remarks,omitempty"`
	UploadedAt         time.Time  `db:"uploaded_at" json:"uploaded_at"`
	ReviewedAt         *time.Time `db:"reviewed_at" json:"reviewed_at,omitempty"`
	ReviewedBy         *uuid.UUID `db:"reviewed_by" json:"reviewed_by,omitempty"`
}

// SellerDocumentTypes is the KYC document vocabulary.
//
// It MUST stay identical to the seller_documents.document_type CHECK
// constraint in migration 001. It lives here so the service can refuse an
// unknown type with a 400 that names the alternatives, instead of letting
// Postgres refuse it with a constraint violation the edge reported as 500.
var SellerDocumentTypes = []string{
	"gst_certificate", "pan_card", "aadhaar", "passport",
	"business_registration", "address_proof", "cancelled_cheque", "other",
}

// ValidDocumentType reports whether t is in SellerDocumentTypes.
func ValidDocumentType(t string) bool {
	for _, v := range SellerDocumentTypes {
		if v == t {
			return true
		}
	}
	return false
}

// ─── Onboarding input types ──────────────────────────────────

type OnboardingBasicInput struct {
	StoreName    string
	OwnerName    string
	BusinessType string
	SellerType   string
	Email        string
	Phone        *string
	State        *string
	City         *string
	PostalCode   *string
	Description  *string
}

type OnboardingStorefrontInput struct {
	BrandName       *string
	LogoMediaID     *uuid.UUID
	BannerMediaID   *uuid.UUID
	Tagline         *string
	SupportPhone    *string
	SupportEmail    *string
	SocialLinksJSON []byte
}

type OnboardingFulfillmentInput struct {
	DeliveryModes    []string
	CODEnabled       bool
	DispatchSLAHours int
	ReturnSupported  bool
	ReturnWindowDays int
}

type OnboardingPayoutInput struct {
	AccountHolderName string
	BankName          *string
	AccountNumber     string
	IFSCCode          *string
	UPIID             *string
}

// ─── Product Category ────────────────────────────────────────

type ProductCategory struct {
	ID           uuid.UUID  `db:"id" json:"id,omitempty"`
	ParentID     *uuid.UUID `db:"parent_id" json:"parent_id,omitempty"`
	Name         string     `db:"name" json:"name,omitempty"`
	Slug         string     `db:"slug" json:"slug,omitempty"`
	Description  *string    `db:"description" json:"description,omitempty"`
	DisplayOrder int        `db:"display_order" json:"display_order,omitempty"`
	IsActive     bool       `db:"is_active" json:"is_active,omitempty"`
	IsFeatured   bool       `db:"is_featured" json:"is_featured,omitempty"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at,omitempty"`
}

// ─── Product ─────────────────────────────────────────────────

type Product struct {
	ID                  uuid.UUID  `db:"id" json:"id,omitempty"`
	SellerID            uuid.UUID  `db:"seller_id" json:"seller_id,omitempty"`
	CategoryID          *uuid.UUID `db:"category_id" json:"category_id,omitempty"`
	BrandID             *uuid.UUID `db:"brand_id" json:"brand_id,omitempty"`
	TaxClassID          *uuid.UUID `db:"tax_class_id" json:"tax_class_id,omitempty"`
	Title               string     `db:"title" json:"title,omitempty"`
	ShortTitle          *string    `db:"short_title" json:"short_title,omitempty"`
	Slug                string     `db:"slug" json:"slug,omitempty"`
	Description         *string    `db:"description" json:"description,omitempty"`
	ShortDescription    *string    `db:"short_description" json:"short_description,omitempty"`
	ProductType         string     `db:"product_type" json:"product_type,omitempty"`
	Condition           string     `db:"condition" json:"condition,omitempty"`
	SKURoot             *string    `db:"sku_root" json:"sku_root,omitempty"`
	Status              string     `db:"status" json:"status,omitempty"`
	Visibility          string     `db:"visibility" json:"visibility,omitempty"`
	ApprovalStatus      string     `db:"approval_status" json:"approval_status,omitempty"`
	RejectionReason     *string    `db:"rejection_reason" json:"rejection_reason,omitempty"`
	PrimaryImageMediaID *uuid.UUID `db:"primary_image_media_id" json:"primary_image_media_id,omitempty"`
	// ImageURL and ThumbnailURL are hydrated from media-service on read.
	//
	// Without them commerce hands a client a bare media UUID and nothing
	// else, so no product screen can draw an image: the Android
	// `core:commerce` module has no dependency on `core:media`, which holds
	// the resolver, and adding one would pull the whole ExoPlayer stack into
	// a module that needs a URL. Resolving here fixes it once for every
	// client, iOS included.
	//
	// Empty when media-service is unreachable: the read path fails SOFT and
	// the client shows a placeholder. A catalogue that will not load because
	// the image service is down is worse than a catalogue of grey boxes.
	ImageURL         string     `db:"-" json:"image_url,omitempty"`
	ThumbnailURL     string     `db:"-" json:"thumbnail_url,omitempty"`
	ImageBlurhash    *string    `db:"-" json:"image_blurhash,omitempty"`
	SourceImageURL *string `db:"source_image_url" json:"source_image_url,omitempty"`
	// RetailerName is `sellers.store_name` — the shop the listing belongs to.
	//
	// It goes out under TWO json names. `seller_name` is the contract (it is
	// what the Android `ProductDto` reads, and what the cart and seller
	// surfaces have always called this field); `retailer_name` is retained
	// as an alias because the existing web caller reads it. Both are emitted
	// from this one field by Product.MarshalJSON, so they cannot drift.
	RetailerName     *string    `db:"retailer_name" json:"retailer_name,omitempty"`
	WeightGrams      *int       `db:"weight_grams" json:"weight_grams,omitempty"`
	LengthCm         *float64   `db:"length_cm" json:"length_cm,omitempty"`
	WidthCm          *float64   `db:"width_cm" json:"width_cm,omitempty"`
	HeightCm         *float64   `db:"height_cm" json:"height_cm,omitempty"`
	CountryOfOrigin  *string    `db:"country_of_origin" json:"country_of_origin,omitempty"`
	BrandName        *string    `db:"brand_name" json:"brand_name,omitempty"`
	ManufacturerName *string    `db:"manufacturer_name" json:"manufacturer_name,omitempty"`
	WarrantyInfo     *string    `db:"warranty_info" json:"warranty_info,omitempty"`
	ReturnPolicyType string     `db:"return_policy_type" json:"return_policy_type,omitempty"`
	ReturnPolicyDays int        `db:"return_policy_days" json:"return_policy_days,omitempty"`
	HSNCode          *string    `db:"hsn_code" json:"hsn_code,omitempty"`
	VideoMediaID     *uuid.UUID `db:"video_media_id" json:"video_media_id,omitempty"`
	SearchKeywords   []string   `db:"search_keywords" json:"search_keywords,omitempty"`
	MetaTitle        *string    `db:"meta_title" json:"meta_title,omitempty"`
	MetaDescription  *string    `db:"meta_description" json:"meta_description,omitempty"`
	AvgRating        float64    `db:"avg_rating" json:"avg_rating,omitempty"`
	ReviewCount      int        `db:"review_count" json:"review_count,omitempty"`
	OrderCount       int        `db:"order_count" json:"order_count,omitempty"`
	ViewCount        int64      `db:"view_count" json:"view_count,omitempty"`
	WishlistCount    int        `db:"wishlist_count" json:"wishlist_count,omitempty"`
	IsFeatured       bool       `db:"is_featured" json:"is_featured,omitempty"`
	CreatedAt        time.Time  `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt        time.Time  `db:"updated_at" json:"updated_at,omitempty"`
	PublishedAt      *time.Time `db:"published_at" json:"published_at,omitempty"`

	// Phase F1 — list-view enrichment. Populated by ListProducts via
	// LATERAL subqueries against product_variants so mobile/web can
	// render the catalog grid + add-to-cart without an N+1 detail
	// fetch. Nil on the detail endpoint (use the wrapped variants
	// array there instead).
	DefaultVariantID *uuid.UUID `json:"default_variant_id,omitempty"`

	// The catalogue's money, in PAISE.
	//
	// These are what a client renders. The float fields below are the
	// original shape and are kept for the existing web caller, but no new
	// client should read them: `min_selling_price` is a rupee float, and the
	// Android client — correctly written against the integer-paise rule the
	// rest of this service follows — expects `min_price_minor`. The two names
	// never matched, so every product in the app's catalogue rendered as ₹0.
	//
	// The float pair also slipped past the CI money gate, which greps for
	// float fields whose NAMES look like money: "MinSellingPrice" does not
	// match `(amount|price|total|...)`. Naming the paise fields `_minor` is
	// what keeps them legible to both the client and the gate.
	MinPriceMinor *int64 `json:"min_price_minor,omitempty"`
	MRPMinor      *int64 `json:"mrp_minor,omitempty"`

	// InStock is the derived boolean the grid actually needs. TotalStock is
	// retained for callers that show a count.
	InStock *bool `json:"in_stock,omitempty"`

	// Deprecated: rupee floats, kept for the existing web client.
	// money-exempt: legacy list-view fields, superseded by the _minor pair
	// above; no pricing path reads them.
	MinSellingPrice *float64 `json:"min_selling_price,omitempty"`
	MinMRP          *float64 `json:"min_mrp,omitempty"`
	TotalStock      *int     `json:"total_stock,omitempty"`
}

// MarshalJSON emits the store name under both wire names.
//
// The product body sent `retailer_name` and the app reads `seller_name`, so
// the shop's name was simply absent from every product screen. Renaming the
// field would break the web caller that reads `retailer_name`; emitting one
// value under both names breaks nobody, and doing it here — rather than by
// adding a second struct field that four separate scan sites would have to
// remember to fill — means the two can never disagree.
func (p Product) MarshalJSON() ([]byte, error) {
	type alias Product // sheds this method, so this is not infinite recursion
	return json.Marshal(struct {
		alias
		SellerName *string `json:"seller_name,omitempty"`
	}{alias(p), p.RetailerName})
}

// ProductMedia is one image / video / size-chart / infographic in a
// product's gallery. media_id refers to a media-service-owned asset.
type ProductMedia struct {
	ID        uuid.UUID `db:"id" json:"id,omitempty"`
	ProductID uuid.UUID `db:"product_id" json:"product_id,omitempty"`
	MediaID   uuid.UUID `db:"media_id" json:"media_id,omitempty"`
	MediaType string    `db:"media_type" json:"media_type,omitempty"`
	SortOrder int       `db:"sort_order" json:"sort_order"`
	CreatedAt time.Time `db:"created_at" json:"created_at,omitempty"`
}

// ProductAttribute is one free-form spec row (name / value / unit) in the
// product's structured-specifications block — e.g. {"RAM", "8", "GB"}.
type ProductAttribute struct {
	ID        uuid.UUID `db:"id" json:"id,omitempty"`
	ProductID uuid.UUID `db:"product_id" json:"product_id,omitempty"`
	Name      string    `db:"name" json:"name"`
	Value     string    `db:"value" json:"value"`
	Unit      *string   `db:"unit" json:"unit,omitempty"`
	SortOrder int       `db:"sort_order" json:"sort_order"`
}

// ─── Product Variant ─────────────────────────────────────────

type ProductVariant struct {
	ID           uuid.UUID `db:"id" json:"id,omitempty"`
	ProductID    uuid.UUID `db:"product_id" json:"product_id,omitempty"`
	SKU          string    `db:"sku" json:"sku,omitempty"`
	Barcode      *string   `db:"barcode" json:"barcode,omitempty"`
	Option1Name  *string   `db:"option_1_name" json:"option_1_name,omitempty"`
	Option1Value *string   `db:"option_1_value" json:"option_1_value,omitempty"`
	Option2Name  *string   `db:"option_2_name" json:"option_2_name,omitempty"`
	Option2Value *string   `db:"option_2_value" json:"option_2_value,omitempty"`
	Option3Name  *string   `db:"option_3_name" json:"option_3_name,omitempty"`
	Option3Value *string   `db:"option_3_value" json:"option_3_value,omitempty"`
	MRP          float64   `db:"mrp" json:"mrp,omitempty"`
	SellingPrice float64   `db:"selling_price" json:"selling_price,omitempty"`
	// The authoritative money, when the caller has it.
	//
	// Non-nil means paise came from the client and the rupee columns above
	// are a mirror written for the analytics readers that still scan them.
	// Nil means a legacy caller supplied only rupees, and the store converts
	// once at the boundary.
	MRPMinorIn          *int64     `db:"-" json:"-"`
	SellingPriceMinorIn *int64     `db:"-" json:"-"`
	CostPriceMinorIn    *int64     `db:"-" json:"-"`
	CostPrice           *float64   `db:"cost_price" json:"cost_price,omitempty"`
	CurrencyCode        string     `db:"currency_code" json:"currency_code,omitempty"`
	Status              string     `db:"status" json:"status,omitempty"`
	ImageMediaID        *uuid.UUID `db:"image_media_id" json:"image_media_id,omitempty"`
	WeightGrams         *int       `db:"weight_grams" json:"weight_grams,omitempty"`
	CreatedAt           time.Time  `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt           time.Time  `db:"updated_at" json:"updated_at,omitempty"`

	// ─── The variant's money and stock, ON THE WIRE ──────────────
	//
	// Every read that returns a variant to a client populates these. The
	// float pair above stayed the ONLY money a variant carried long after
	// the product LIST learned to send `min_price_minor` (see the comment
	// at the Product struct), so the catalogue grid showed the right price
	// and the product DETAIL page — the screen a buyer actually taps
	// "add to cart" on — rendered every variant at ₹0.00 and out of stock.
	// The Android `VariantDto` reads exactly these three names.
	//
	// Sourced the same way `min_price_minor` is: the minor column when it
	// holds a real value, and `ROUND(rupees*100)` when a legacy row still
	// carries the DEFAULT 0 there. A variant whose minor column is zero is
	// not a free variant, it is an unmigrated one.
	//
	// AvailableQty is `total_qty - reserved_qty` clamped at zero — the same
	// arithmetic InventoryItem.AvailableQty does in Go, done in SQL so the
	// read is one round trip instead of one per variant. Nil (rather than
	// 0) when the variant has no inventory row at all, so "no stock record"
	// and "sold out" stay distinguishable.
	MRPMinor          *int64 `db:"-" json:"mrp_minor,omitempty"`
	SellingPriceMinor *int64 `db:"-" json:"selling_price_minor,omitempty"`
	AvailableQty      *int   `db:"-" json:"available_qty,omitempty"`
}

// ─── Inventory ───────────────────────────────────────────────

type InventoryItem struct {
	ID            uuid.UUID `db:"id" json:"id,omitempty"`
	VariantID     uuid.UUID `db:"variant_id" json:"variant_id,omitempty"`
	SellerID      uuid.UUID `db:"seller_id" json:"seller_id,omitempty"`
	TotalQty      int       `db:"total_qty" json:"total_qty,omitempty"`
	ReservedQty   int       `db:"reserved_qty" json:"reserved_qty,omitempty"`
	DamagedQty    int       `db:"damaged_qty" json:"damaged_qty,omitempty"`
	ReturnedQty   int       `db:"returned_qty" json:"returned_qty,omitempty"`
	SafetyStock   int       `db:"safety_stock" json:"safety_stock,omitempty"`
	LowStockAlert int       `db:"low_stock_alert" json:"low_stock_alert,omitempty"`
	UpdatedAt     time.Time `db:"updated_at" json:"updated_at,omitempty"`
}

func (i *InventoryItem) AvailableQty() int {
	return i.TotalQty - i.ReservedQty
}

// ─── Cart ────────────────────────────────────────────────────

type Cart struct {
	ID        uuid.UUID  `db:"id" json:"id,omitempty"`
	UserID    uuid.UUID  `db:"user_id" json:"user_id,omitempty"`
	ExpiresAt *time.Time `db:"expires_at" json:"expires_at,omitempty"`
	UpdatedAt time.Time  `db:"updated_at" json:"updated_at,omitempty"`
}

type CartItem struct {
	ID            uuid.UUID `db:"id" json:"id,omitempty"`
	CartID        uuid.UUID `db:"cart_id" json:"cart_id,omitempty"`
	VariantID     uuid.UUID `db:"variant_id" json:"variant_id,omitempty"`
	ProductID     uuid.UUID `db:"product_id" json:"product_id,omitempty"`
	Quantity      int       `db:"quantity" json:"quantity,omitempty"`
	PriceSnapshot float64   `db:"price_snapshot" json:"price_snapshot,omitempty"`
	AddedAt       time.Time `db:"added_at" json:"added_at,omitempty"`
}

// ─── Order ───────────────────────────────────────────────────

type Order struct {
	ID                      uuid.UUID  `db:"id" json:"id,omitempty"`
	CustomerUserID          uuid.UUID  `db:"customer_user_id" json:"customer_user_id,omitempty"`
	OrderNumber             string     `db:"order_number" json:"order_number,omitempty"`
	Subtotal                float64    `db:"subtotal" json:"subtotal,omitempty"` // money-exempt: read model mirroring the deprecated orders.subtotal NUMERIC for analytics readers
	DiscountAmount          float64    `db:"discount_amount" json:"discount_amount,omitempty"`
	ShippingCharges         float64    `db:"shipping_charges" json:"shipping_charges,omitempty"`
	TaxAmount               float64    `db:"tax_amount" json:"tax_amount,omitempty"`
	CouponCode              *string    `db:"coupon_code" json:"coupon_code,omitempty"`
	CouponDiscount          float64    `db:"coupon_discount" json:"coupon_discount,omitempty"`
	FinalAmount             float64    `db:"final_amount" json:"final_amount,omitempty"`

	// ─── The authoritative money ────────────────────────────────
	//
	// Migration 007 made these the truth and stopped maintaining the
	// NUMERIC columns above, so on every order the P0 checkout has written
	// the rupee fields read 0.00. Anything that computed from them — the
	// order list before it moved to OrderCard, and ConfirmPayment's
	// expected-amount check, which derived paise as ROUND(FinalAmount*100)
	// and therefore expected ZERO — was working from a column nobody fills.
	//
	// Read them through Order.TotalMinor and friends, never directly: an
	// order written before 007 has the rupee column and a zero here, and
	// the accessors are where that fallback lives.
	SubtotalMinor        int64 `db:"subtotal_minor" json:"subtotal_minor,omitempty"`
	DiscountAmountMinor  int64 `db:"discount_amount_minor" json:"discount_minor,omitempty"`
	ShippingChargesMinor int64 `db:"shipping_charges_minor" json:"shipping_minor,omitempty"`
	TaxAmountMinor       int64 `db:"tax_amount_minor" json:"tax_minor,omitempty"`
	CouponDiscountMinor  int64 `db:"coupon_discount_minor" json:"coupon_discount_minor,omitempty"`
	FinalAmountMinor     int64 `db:"final_amount_minor" json:"total_minor,omitempty"`
	CurrencyCode            string     `db:"currency_code" json:"currency_code,omitempty"`
	PaymentMethod           *string    `db:"payment_method" json:"payment_method,omitempty"`
	PaymentStatus           string     `db:"payment_status" json:"payment_status,omitempty"`
	PaymentID               *string    `db:"payment_id" json:"payment_id,omitempty"`
	PaymentGateway          *string    `db:"payment_gateway" json:"payment_gateway,omitempty"`
	DeliveryAddressID       *uuid.UUID `db:"delivery_address_id" json:"delivery_address_id,omitempty"`
	DeliveryAddressSnapshot []byte     `db:"delivery_address_snapshot" json:"delivery_address_snapshot,omitempty"`
	// The sealed copy (scope order-snapshot). `json:"-"` is load-bearing:
	// this is ciphertext, it is useless to a client, and after the PII
	// cutover it is the ONLY place the buyer's name, phone, street and
	// landmark live. Order detail opens it through the cipher and returns
	// a real address; nothing serialises the blob itself.
	DeliveryAddressSnapshotEnc []byte `db:"delivery_address_snapshot_enc" json:"-"`
	GiftMessage             *string    `db:"gift_message" json:"gift_message,omitempty"`
	Status                  string     `db:"status" json:"status,omitempty"`
	CancellationReason      *string    `db:"cancellation_reason" json:"cancellation_reason,omitempty"`
	CancelledBy             *string    `db:"cancelled_by" json:"cancelled_by,omitempty"`
	IdempotencyKey          *string    `db:"idempotency_key" json:"idempotency_key,omitempty"`
	// ─── Phase 5 — B2B context (nullable on retail orders) ─────
	OrganizationID         *uuid.UUID `db:"organization_id" json:"organization_id,omitempty"`
	PONumber               *string    `db:"po_number" json:"po_number,omitempty"`
	CostCenter             *string    `db:"cost_center" json:"cost_center,omitempty"`
	BillingAddressSnapshot []byte     `db:"billing_address_snapshot" json:"billing_address_snapshot,omitempty"`
	InvoiceEmail           *string    `db:"invoice_email" json:"invoice_email,omitempty"`
	ApprovalStatus         *string    `db:"approval_status" json:"approval_status,omitempty"`
	ApprovedByUserID       *uuid.UUID `db:"approved_by_user_id" json:"approved_by_user_id,omitempty"`
	ApprovedAt             *time.Time `db:"approved_at" json:"approved_at,omitempty"`
	ApprovalNotes          *string    `db:"approval_notes" json:"approval_notes,omitempty"`
	CreditTermsDays        int        `db:"credit_terms_days" json:"credit_terms_days,omitempty"`
	PaymentDueDate         *time.Time `db:"payment_due_date" json:"payment_due_date,omitempty"`
	CreatedAt              time.Time  `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt              time.Time  `db:"updated_at" json:"updated_at,omitempty"`
}

type OrderItem struct {
	ID                  uuid.UUID  `db:"id" json:"id,omitempty"`
	OrderID             uuid.UUID  `db:"order_id" json:"order_id,omitempty"`
	ProductID           uuid.UUID  `db:"product_id" json:"product_id,omitempty"`
	VariantID           uuid.UUID  `db:"variant_id" json:"variant_id,omitempty"`
	SellerID            uuid.UUID  `db:"seller_id" json:"seller_id,omitempty"`
	ProductTitle        string     `db:"product_title" json:"product_title,omitempty"`
	VariantDetails      []byte     `db:"variant_details" json:"variant_details,omitempty"`
	SKU                 string     `db:"sku" json:"sku,omitempty"`
	Quantity            int        `db:"quantity" json:"quantity,omitempty"`
	UnitMRP             float64    `db:"unit_mrp" json:"unit_mrp,omitempty"`
	UnitPrice           float64    `db:"unit_price" json:"unit_price,omitempty"`
	DiscountAmount      float64    `db:"discount_amount" json:"discount_amount,omitempty"`
	TaxAmount           float64    `db:"tax_amount" json:"tax_amount,omitempty"`
	FinalPrice          float64    `db:"final_price" json:"final_price,omitempty"`
	Status              string     `db:"status" json:"status,omitempty"`
	ShipmentID          *uuid.UUID `db:"shipment_id" json:"shipment_id,omitempty"`
	TrackingNumber      *string    `db:"tracking_number" json:"tracking_number,omitempty"`
	ReturnEligibleUntil *time.Time `db:"return_eligible_until" json:"return_eligible_until,omitempty"`
	DeliveredAt         *time.Time `db:"delivered_at" json:"delivered_at,omitempty"`
	CreatedAt           time.Time  `db:"created_at" json:"created_at,omitempty"`

	// The line's money, in paise — same story as the order header above:
	// migration 007 stopped maintaining the NUMERIC columns, so summing
	// FinalPrice over a seller's lines produced 0 (which is exactly what
	// `seller_subtotal` reported on a ₹929 order).
	UnitMRPMinor        int64 `db:"unit_mrp_minor" json:"unit_mrp_minor,omitempty"`
	UnitPriceMinor      int64 `db:"unit_price_minor" json:"unit_price_minor,omitempty"`
	DiscountAmountMinor int64 `db:"discount_amount_minor" json:"discount_minor,omitempty"`
	TaxAmountMinor      int64 `db:"tax_amount_minor" json:"tax_minor,omitempty"`
	FinalPriceMinor     int64 `db:"final_price_minor" json:"final_price_minor,omitempty"`
}

// LineTotalMinor is the line's payable amount in paise, falling back to the
// deprecated rupee column for a row written before migration 007.
func (i *OrderItem) LineTotalMinor() int64 {
	return minorOrRupee(i.FinalPriceMinor, i.FinalPrice)
}

// TotalMinor is the order's payable amount in paise.
//
// Everything that needs the order total goes through here rather than
// reading either column, so the pre-007 fallback exists in one place.
func (o *Order) TotalMinor() int64 { return minorOrRupee(o.FinalAmountMinor, o.FinalAmount) }

// SubtotalMinorOrRupees etc. mirror TotalMinor for the remaining components.
func (o *Order) SubtotalMinorValue() int64 {
	return minorOrRupee(o.SubtotalMinor, o.Subtotal)
}
func (o *Order) DiscountMinorValue() int64 {
	return minorOrRupee(o.DiscountAmountMinor, o.DiscountAmount)
}
func (o *Order) ShippingMinorValue() int64 {
	return minorOrRupee(o.ShippingChargesMinor, o.ShippingCharges)
}
func (o *Order) TaxMinorValue() int64 { return minorOrRupee(o.TaxAmountMinor, o.TaxAmount) }

// minorOrRupee prefers the paise column and converts the deprecated rupee
// mirror only when paise is exactly zero.
//
// Zero is the sentinel because migration 007 defaulted these columns to 0
// rather than NULL, so "unset" and "genuinely free" are indistinguishable in
// the data. Reading a real ₹0 line as ₹0 either way makes that ambiguity
// harmless.
func minorOrRupee(minor int64, rupees float64) int64 {
	if minor != 0 {
		return minor
	}
	return int64(math.Round(rupees * 100))
}

type OrderStatusHistory struct {
	ID         uuid.UUID  `db:"id" json:"id,omitempty"`
	OrderID    uuid.UUID  `db:"order_id" json:"order_id,omitempty"`
	FromStatus *string    `db:"from_status" json:"from_status,omitempty"`
	ToStatus   string     `db:"to_status" json:"to_status,omitempty"`
	ChangedBy  *uuid.UUID `db:"changed_by" json:"changed_by,omitempty"`
	ActorType  string     `db:"actor_type" json:"actor_type,omitempty"`
	Notes      *string    `db:"notes" json:"notes,omitempty"`
	CreatedAt  time.Time  `db:"created_at" json:"created_at,omitempty"`
}

// ─── Payment ─────────────────────────────────────────────────

type Payment struct {
	ID             uuid.UUID  `db:"id" json:"id,omitempty"`
	OrderID        uuid.UUID  `db:"order_id" json:"order_id,omitempty"`
	UserID         uuid.UUID  `db:"user_id" json:"user_id,omitempty"`
	Amount         float64    `db:"amount" json:"amount,omitempty"`
	Currency       string     `db:"currency" json:"currency,omitempty"`
	PaymentMethod  string     `db:"payment_method" json:"payment_method,omitempty"`
	Gateway        string     `db:"gateway" json:"gateway,omitempty"`
	GatewayOrderID *string    `db:"gateway_order_id" json:"gateway_order_id,omitempty"`
	GatewayTxnID   *string    `db:"gateway_txn_id" json:"gateway_txn_id,omitempty"`
	Status         string     `db:"status" json:"status,omitempty"`
	IdempotencyKey *string    `db:"idempotency_key" json:"idempotency_key,omitempty"`
	RawResponse    []byte     `db:"raw_response" json:"raw_response,omitempty"`
	FailureReason  *string    `db:"failure_reason" json:"failure_reason,omitempty"`
	InitiatedAt    time.Time  `db:"initiated_at" json:"initiated_at,omitempty"`
	CompletedAt    *time.Time `db:"completed_at" json:"completed_at,omitempty"`
}

// ─── Shipping ────────────────────────────────────────────────

type ShippingPackage struct {
	ID                uuid.UUID  `db:"id" json:"id,omitempty"`
	OrderID           uuid.UUID  `db:"order_id" json:"order_id,omitempty"`
	SellerID          uuid.UUID  `db:"seller_id" json:"seller_id,omitempty"`
	ShippingPartnerID *uuid.UUID `db:"shipping_partner_id" json:"shipping_partner_id,omitempty"`
	AWBNumber         *string    `db:"awb_number" json:"awb_number,omitempty"`
	TrackingURL       *string    `db:"tracking_url" json:"tracking_url,omitempty"`
	WeightGrams       *int       `db:"weight_grams" json:"weight_grams,omitempty"`
	CurrentStatus     string     `db:"current_status" json:"current_status,omitempty"`
	CurrentLocation   *string    `db:"current_location" json:"current_location,omitempty"`
	EstimatedDelivery *time.Time `db:"estimated_delivery_date" json:"estimated_delivery_date,omitempty"`
	DeliveredAt       *time.Time `db:"delivered_at" json:"delivered_at,omitempty"`
	PickedUpAt        *time.Time `db:"picked_up_at" json:"picked_up_at,omitempty"`
	CreatedAt         time.Time  `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt         time.Time  `db:"updated_at" json:"updated_at,omitempty"`
}

// ─── Review ──────────────────────────────────────────────────

type Review struct {
	ID                 uuid.UUID  `db:"id" json:"id,omitempty"`
	ProductID          uuid.UUID  `db:"product_id" json:"product_id,omitempty"`
	SellerID           uuid.UUID  `db:"seller_id" json:"seller_id,omitempty"`
	OrderItemID        uuid.UUID  `db:"order_item_id" json:"order_item_id,omitempty"`
	ReviewerID         uuid.UUID  `db:"reviewer_id" json:"reviewer_id,omitempty"`
	Rating             int        `db:"rating" json:"rating,omitempty"`
	Title              *string    `db:"title" json:"title,omitempty"`
	Body               *string    `db:"body" json:"body,omitempty"`
	IsVerifiedPurchase bool       `db:"is_verified_purchase" json:"is_verified_purchase,omitempty"`
	IsPublished        bool       `db:"is_published" json:"is_published,omitempty"`
	HelpfulCount       int        `db:"helpful_count" json:"helpful_count,omitempty"`
	ModerationStatus   string     `db:"moderation_status" json:"moderation_status,omitempty"`
	SellerResponse     *string    `db:"seller_response" json:"seller_response,omitempty"`
	SellerRespondedAt  *time.Time `db:"seller_responded_at" json:"seller_responded_at,omitempty"`
	CreatedAt          time.Time  `db:"created_at" json:"created_at,omitempty"`
}

// ─── Return Request ──────────────────────────────────────────

type ReturnRequest struct {
	ID                uuid.UUID  `db:"id" json:"id,omitempty"`
	OrderID           uuid.UUID  `db:"order_id" json:"order_id,omitempty"`
	OrderItemID       uuid.UUID  `db:"order_item_id" json:"order_item_id,omitempty"`
	CustomerUserID    uuid.UUID  `db:"customer_user_id" json:"customer_user_id,omitempty"`
	SellerID          uuid.UUID  `db:"seller_id" json:"seller_id,omitempty"`
	ReasonCode        string     `db:"reason_code" json:"reason_code,omitempty"`
	ReasonDescription *string    `db:"reason_description" json:"reason_description,omitempty"`
	Status            string     `db:"status" json:"status,omitempty"`
	RequestedAt       time.Time  `db:"requested_at" json:"requested_at,omitempty"`
	ApprovedAt        *time.Time `db:"approved_at" json:"approved_at,omitempty"`
	RejectedAt        *time.Time `db:"rejected_at" json:"rejected_at,omitempty"`
	RejectionReason   *string    `db:"rejection_reason" json:"rejection_reason,omitempty"`
	RefundAmount      *float64   `db:"refund_amount" json:"refund_amount,omitempty"`
}

// ─── Coupon ──────────────────────────────────────────────────

type Coupon struct {
	ID                uuid.UUID  `db:"id" json:"id,omitempty"`
	SellerID          *uuid.UUID `db:"seller_id" json:"seller_id,omitempty"`
	Code              string     `db:"code" json:"code,omitempty"`
	Description       *string    `db:"description" json:"description,omitempty"`
	DiscountType      string     `db:"discount_type" json:"discount_type,omitempty"`
	DiscountValue     float64    `db:"discount_value" json:"discount_value,omitempty"`
	MaxDiscountAmount *float64   `db:"max_discount_amount" json:"max_discount_amount,omitempty"`
	MinOrderAmount    float64    `db:"min_order_amount" json:"min_order_amount,omitempty"`
	MaxUses           *int       `db:"max_uses" json:"max_uses,omitempty"`
	UsesCount         int        `db:"uses_count" json:"uses_count,omitempty"`
	MaxUsesPerUser    int        `db:"max_uses_per_user" json:"max_uses_per_user,omitempty"`
	ApplicableTo      string     `db:"applicable_to" json:"applicable_to,omitempty"`
	IsActive          bool       `db:"is_active" json:"is_active,omitempty"`
	StartsAt          time.Time  `db:"starts_at" json:"starts_at,omitempty"`
	ExpiresAt         *time.Time `db:"expires_at" json:"expires_at,omitempty"`
}

// ─── Customer Address ────────────────────────────────────────

type CustomerAddress struct {
	ID           uuid.UUID `db:"id" json:"id,omitempty"`
	UserID       uuid.UUID `db:"user_id" json:"user_id,omitempty"`
	Label        string    `db:"label" json:"label,omitempty"`
	ContactName  string    `db:"contact_name" json:"contact_name,omitempty"`
	Phone        string    `db:"phone" json:"phone,omitempty"`
	AddressLine1 string    `db:"address_line_1" json:"address_line_1,omitempty"`
	AddressLine2 *string   `db:"address_line_2" json:"address_line_2,omitempty"`
	Landmark     *string   `db:"landmark" json:"landmark,omitempty"`
	City         string    `db:"city" json:"city,omitempty"`
	State        string    `db:"state" json:"state,omitempty"`
	Country      string    `db:"country" json:"country,omitempty"`
	PostalCode   string    `db:"postal_code" json:"postal_code,omitempty"`
	AddressType  string    `db:"address_type" json:"address_type,omitempty"`
	IsDefault    bool      `db:"is_default" json:"is_default,omitempty"`
	CreatedAt    time.Time `db:"created_at" json:"created_at,omitempty"`
}

// ─── COD Remittance ──────────────────────────────────────────

// CODRemittance tracks one COD shipment's cash collection lifecycle: courier
// confirms delivery -> remittance row created in 'pending' -> Ops marks
// 'settled' once the cash transfers to the seller's payout account.
type CODRemittance struct {
	ID               uuid.UUID  `db:"id" json:"id,omitempty"`
	ShipmentID       uuid.UUID  `db:"shipment_id" json:"shipment_id,omitempty"`
	OrderID          uuid.UUID  `db:"order_id" json:"order_id,omitempty"`
	SellerID         uuid.UUID  `db:"seller_id" json:"seller_id,omitempty"`
	GrossAmount      float64    `db:"gross_amount" json:"gross_amount"`
	CommissionAmount float64    `db:"commission_amount" json:"commission_amount"`
	PlatformFee      float64    `db:"platform_fee" json:"platform_fee"`
	TDSAmount        float64    `db:"tds_amount" json:"tds_amount"`
	NetAmount        float64    `db:"net_amount" json:"net_amount"`
	CurrencyCode     string     `db:"currency_code" json:"currency_code,omitempty"`
	Status           string     `db:"status" json:"status,omitempty"`
	DeliveredAt      time.Time  `db:"delivered_at" json:"delivered_at,omitempty"`
	SettledAt        *time.Time `db:"settled_at" json:"settled_at,omitempty"`
	PayoutBatchID    *uuid.UUID `db:"payout_batch_id" json:"payout_batch_id,omitempty"`
	CreatedAt        time.Time  `db:"created_at" json:"created_at,omitempty"`
}

// ─── Support Ticket ──────────────────────────────────────────

type SupportTicket struct {
	ID          uuid.UUID  `db:"id" json:"id,omitempty"`
	UserID      uuid.UUID  `db:"user_id" json:"user_id,omitempty"`
	SellerID    *uuid.UUID `db:"seller_id" json:"seller_id,omitempty"`
	OrderID     *uuid.UUID `db:"order_id" json:"order_id,omitempty"`
	Category    string     `db:"category" json:"category,omitempty"`
	Subject     string     `db:"subject" json:"subject,omitempty"`
	Description string     `db:"description" json:"description,omitempty"`
	Priority    string     `db:"priority" json:"priority,omitempty"`
	Status      string     `db:"status" json:"status,omitempty"`
	AssignedTo  *uuid.UUID `db:"assigned_to" json:"assigned_to,omitempty"`
	CreatedAt   time.Time  `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt   time.Time  `db:"updated_at" json:"updated_at,omitempty"`
}

// ─── Payout ──────────────────────────────────────────────────

type PayoutBatch struct {
	ID           uuid.UUID  `db:"id" json:"id,omitempty"`
	BatchDate    time.Time  `db:"batch_date" json:"batch_date,omitempty"`
	CycleStart   time.Time  `db:"payout_cycle_start" json:"payout_cycle_start,omitempty"`
	CycleEnd     time.Time  `db:"payout_cycle_end" json:"payout_cycle_end,omitempty"`
	TotalSellers int        `db:"total_sellers" json:"total_sellers,omitempty"`
	TotalAmount  float64    `db:"total_amount" json:"total_amount,omitempty"`
	Status       string     `db:"status" json:"status,omitempty"`
	ProcessedAt  *time.Time `db:"processed_at" json:"processed_at,omitempty"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at,omitempty"`
}

type PayoutTransaction struct {
	ID                uuid.UUID  `db:"id" json:"id,omitempty"`
	BatchID           uuid.UUID  `db:"batch_id" json:"batch_id,omitempty"`
	SellerID          uuid.UUID  `db:"seller_id" json:"seller_id,omitempty"`
	GrossAmount       float64    `db:"gross_amount" json:"gross_amount,omitempty"`
	CommissionAmount  float64    `db:"commission_amount" json:"commission_amount,omitempty"`
	PlatformFee       float64    `db:"platform_fee" json:"platform_fee,omitempty"`
	TaxDeducted       float64    `db:"tax_deducted" json:"tax_deducted,omitempty"`
	AdjustmentAmount  float64    `db:"adjustment_amount" json:"adjustment_amount,omitempty"`
	NetAmount         float64    `db:"net_amount" json:"net_amount,omitempty"`
	BankAccountID     *uuid.UUID `db:"bank_account_id" json:"bank_account_id,omitempty"`
	TransferReference *string    `db:"transfer_reference" json:"transfer_reference,omitempty"`
	Status            string     `db:"status" json:"status,omitempty"`
	FailureReason     *string    `db:"failure_reason" json:"failure_reason,omitempty"`
	InitiatedAt       time.Time  `db:"initiated_at" json:"initiated_at,omitempty"`
	CompletedAt       *time.Time `db:"completed_at" json:"completed_at,omitempty"`
}

// ─── Phase 5 — B2B / Organizations ───────────────────────────

type Organization struct {
	ID                uuid.UUID  `db:"id" json:"id"`
	Name              string     `db:"name" json:"name"`
	LegalName         *string    `db:"legal_name" json:"legal_name,omitempty"`
	GSTIN             *string    `db:"gstin" json:"gstin,omitempty"`
	PAN               *string    `db:"pan" json:"pan,omitempty"`
	BillingEmail      *string    `db:"billing_email" json:"billing_email,omitempty"`
	BillingPhone      *string    `db:"billing_phone" json:"billing_phone,omitempty"`
	BillingAddressID  *uuid.UUID `db:"billing_address_id" json:"billing_address_id,omitempty"`
	ApprovalThreshold *float64   `db:"approval_threshold" json:"approval_threshold,omitempty"`
	CreditTermsDays   int        `db:"credit_terms_days" json:"credit_terms_days"`
	CreditLimit       *float64   `db:"credit_limit" json:"credit_limit,omitempty"`
	Status            string     `db:"status" json:"status"`
	CreatedByUserID   *uuid.UUID `db:"created_by_user_id" json:"created_by_user_id,omitempty"`
	CreatedAt         time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at" json:"updated_at"`
}

type OrganizationMember struct {
	ID             uuid.UUID  `db:"id" json:"id"`
	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	UserID         uuid.UUID  `db:"user_id" json:"user_id"`
	Role           string     `db:"role" json:"role"`
	Status         string     `db:"status" json:"status"`
	InvitedEmail   *string    `db:"invited_email" json:"invited_email,omitempty"`
	InvitedAt      time.Time  `db:"invited_at" json:"invited_at"`
	JoinedAt       *time.Time `db:"joined_at" json:"joined_at,omitempty"`
}

type OrganizationInvite struct {
	ID             uuid.UUID  `db:"id" json:"id"`
	OrganizationID uuid.UUID  `db:"organization_id" json:"organization_id"`
	Email          string     `db:"email" json:"email"`
	Role           string     `db:"role" json:"role"`
	Token          string     `db:"token" json:"token,omitempty"`
	InvitedBy      uuid.UUID  `db:"invited_by" json:"invited_by"`
	ExpiresAt      time.Time  `db:"expires_at" json:"expires_at"`
	AcceptedAt     *time.Time `db:"accepted_at" json:"accepted_at,omitempty"`
	CreatedAt      time.Time  `db:"created_at" json:"created_at"`
}
