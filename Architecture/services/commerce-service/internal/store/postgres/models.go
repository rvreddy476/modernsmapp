package postgres

import (
	"errors"
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
	ID                 uuid.UUID  `db:"id" json:"id"`
	UserID             uuid.UUID  `db:"user_id" json:"user_id"`
	BusinessPageID     *uuid.UUID `db:"business_page_id" json:"business_page_id,omitempty"`
	SellerType         string     `db:"seller_type" json:"seller_type"`
	BusinessType       string     `db:"business_type" json:"business_type"`
	StoreName          string     `db:"store_name" json:"store_name"`
	BrandName          *string    `db:"brand_name" json:"brand_name,omitempty"`
	LegalBusinessName  *string    `db:"legal_business_name" json:"legal_business_name,omitempty"`
	OwnerName          *string    `db:"owner_name" json:"owner_name,omitempty"`
	Slug               string     `db:"slug" json:"slug"`
	Description        *string    `db:"description" json:"description,omitempty"`
	Tagline            *string    `db:"tagline" json:"tagline,omitempty"`
	SocialLinksJSON    []byte     `db:"social_links_json" json:"social_links_json,omitempty"`
	LogoMediaID        *uuid.UUID `db:"logo_media_id" json:"logo_media_id,omitempty"`
	BannerMediaID      *uuid.UUID `db:"banner_media_id" json:"banner_media_id,omitempty"`
	Email              string     `db:"email" json:"email"`
	Phone              *string    `db:"phone" json:"phone,omitempty"`
	GSTNumber          *string    `db:"gst_number" json:"gst_number,omitempty"`
	PANNumber          *string    `db:"pan_number" json:"pan_number,omitempty"`
	SupportPhone       *string    `db:"support_phone" json:"support_phone,omitempty"`
	SupportEmail       *string    `db:"support_email" json:"support_email,omitempty"`
	State              *string    `db:"state" json:"state,omitempty"`
	City               *string    `db:"city" json:"city,omitempty"`
	PostalCode         *string    `db:"postal_code" json:"postal_code,omitempty"`
	// Onboarding fields
	Status             string     `db:"status" json:"status"`
	OnboardingStep     int        `db:"onboarding_step" json:"onboarding_step"`
	SubmittedAt        *time.Time `db:"submitted_at" json:"submitted_at,omitempty"`
	ApprovedAt         *time.Time `db:"approved_at" json:"approved_at,omitempty"`
	RejectedAt         *time.Time `db:"rejected_at" json:"rejected_at,omitempty"`
	RejectionReason    *string    `db:"rejection_reason" json:"rejection_reason,omitempty"`
	ChangesRequested   *string    `db:"changes_requested" json:"changes_requested,omitempty"`
	SuspensionReason   *string    `db:"suspension_reason" json:"suspension_reason,omitempty"`
	// Legacy fields
	VerificationStatus string     `db:"verification_status" json:"verification_status"`
	StoreStatus        string     `db:"store_status" json:"store_status"`
	QualityScore       float64    `db:"quality_score" json:"quality_score"`
	PerformanceTier    string     `db:"performance_tier" json:"performance_tier"`
	AvgRating          float64    `db:"avg_rating" json:"avg_rating"`
	ReviewCount        int        `db:"review_count" json:"review_count"`
	FollowerCount      int        `db:"follower_count" json:"follower_count"`
	TotalProducts      int        `db:"total_products" json:"total_products"`
	TotalOrders        int        `db:"total_orders" json:"total_orders"`
	CreatedAt          time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time  `db:"updated_at" json:"updated_at"`
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
	ID                   uuid.UUID  `db:"id" json:"id,omitempty"`
	SellerID             uuid.UUID  `db:"seller_id" json:"seller_id,omitempty"`
	CategoryID           *uuid.UUID `db:"category_id" json:"category_id,omitempty"`
	BrandID              *uuid.UUID `db:"brand_id" json:"brand_id,omitempty"`
	TaxClassID           *uuid.UUID `db:"tax_class_id" json:"tax_class_id,omitempty"`
	Title                string     `db:"title" json:"title,omitempty"`
	ShortTitle           *string    `db:"short_title" json:"short_title,omitempty"`
	Slug                 string     `db:"slug" json:"slug,omitempty"`
	Description          *string    `db:"description" json:"description,omitempty"`
	ShortDescription     *string    `db:"short_description" json:"short_description,omitempty"`
	ProductType          string     `db:"product_type" json:"product_type,omitempty"`
	Condition            string     `db:"condition" json:"condition,omitempty"`
	SKURoot              *string    `db:"sku_root" json:"sku_root,omitempty"`
	Status               string     `db:"status" json:"status,omitempty"`
	Visibility           string     `db:"visibility" json:"visibility,omitempty"`
	ApprovalStatus       string     `db:"approval_status" json:"approval_status,omitempty"`
	RejectionReason      *string    `db:"rejection_reason" json:"rejection_reason,omitempty"`
	PrimaryImageMediaID  *uuid.UUID `db:"primary_image_media_id" json:"primary_image_media_id,omitempty"`
	WeightGrams          *int       `db:"weight_grams" json:"weight_grams,omitempty"`
	LengthCm             *float64   `db:"length_cm" json:"length_cm,omitempty"`
	WidthCm              *float64   `db:"width_cm" json:"width_cm,omitempty"`
	HeightCm             *float64   `db:"height_cm" json:"height_cm,omitempty"`
	CountryOfOrigin      *string    `db:"country_of_origin" json:"country_of_origin,omitempty"`
	BrandName            *string    `db:"brand_name" json:"brand_name,omitempty"`
	ManufacturerName     *string    `db:"manufacturer_name" json:"manufacturer_name,omitempty"`
	WarrantyInfo         *string    `db:"warranty_info" json:"warranty_info,omitempty"`
	ReturnPolicyType     string     `db:"return_policy_type" json:"return_policy_type,omitempty"`
	ReturnPolicyDays     int        `db:"return_policy_days" json:"return_policy_days,omitempty"`
	HSNCode              *string    `db:"hsn_code" json:"hsn_code,omitempty"`
	VideoMediaID         *uuid.UUID `db:"video_media_id" json:"video_media_id,omitempty"`
	SearchKeywords       []string   `db:"search_keywords" json:"search_keywords,omitempty"`
	MetaTitle            *string    `db:"meta_title" json:"meta_title,omitempty"`
	MetaDescription      *string    `db:"meta_description" json:"meta_description,omitempty"`
	AvgRating            float64    `db:"avg_rating" json:"avg_rating,omitempty"`
	ReviewCount          int        `db:"review_count" json:"review_count,omitempty"`
	OrderCount           int        `db:"order_count" json:"order_count,omitempty"`
	ViewCount            int64      `db:"view_count" json:"view_count,omitempty"`
	WishlistCount        int        `db:"wishlist_count" json:"wishlist_count,omitempty"`
	IsFeatured           bool       `db:"is_featured" json:"is_featured,omitempty"`
	CreatedAt            time.Time  `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt            time.Time  `db:"updated_at" json:"updated_at,omitempty"`
	PublishedAt          *time.Time `db:"published_at" json:"published_at,omitempty"`

	// Phase F1 — list-view enrichment. Populated by ListProducts via
	// LATERAL subqueries against product_variants so mobile/web can
	// render the catalog grid + add-to-cart without an N+1 detail
	// fetch. Nil on the detail endpoint (use the wrapped variants
	// array there instead).
	DefaultVariantID *uuid.UUID `json:"default_variant_id,omitempty"`
	MinSellingPrice  *float64   `json:"min_selling_price,omitempty"`
	MinMRP           *float64   `json:"min_mrp,omitempty"`
	TotalStock       *int       `json:"total_stock,omitempty"`
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
	ID             uuid.UUID  `db:"id" json:"id,omitempty"`
	ProductID      uuid.UUID  `db:"product_id" json:"product_id,omitempty"`
	SKU            string     `db:"sku" json:"sku,omitempty"`
	Barcode        *string    `db:"barcode" json:"barcode,omitempty"`
	Option1Name    *string    `db:"option_1_name" json:"option_1_name,omitempty"`
	Option1Value   *string    `db:"option_1_value" json:"option_1_value,omitempty"`
	Option2Name    *string    `db:"option_2_name" json:"option_2_name,omitempty"`
	Option2Value   *string    `db:"option_2_value" json:"option_2_value,omitempty"`
	Option3Name    *string    `db:"option_3_name" json:"option_3_name,omitempty"`
	Option3Value   *string    `db:"option_3_value" json:"option_3_value,omitempty"`
	MRP            float64    `db:"mrp" json:"mrp,omitempty"`
	SellingPrice   float64    `db:"selling_price" json:"selling_price,omitempty"`
	CostPrice      *float64   `db:"cost_price" json:"cost_price,omitempty"`
	CurrencyCode   string     `db:"currency_code" json:"currency_code,omitempty"`
	Status         string     `db:"status" json:"status,omitempty"`
	ImageMediaID   *uuid.UUID `db:"image_media_id" json:"image_media_id,omitempty"`
	WeightGrams    *int       `db:"weight_grams" json:"weight_grams,omitempty"`
	CreatedAt      time.Time  `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt      time.Time  `db:"updated_at" json:"updated_at,omitempty"`
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
	Subtotal                float64    `db:"subtotal" json:"subtotal,omitempty"`
	DiscountAmount          float64    `db:"discount_amount" json:"discount_amount,omitempty"`
	ShippingCharges         float64    `db:"shipping_charges" json:"shipping_charges,omitempty"`
	TaxAmount               float64    `db:"tax_amount" json:"tax_amount,omitempty"`
	CouponCode              *string    `db:"coupon_code" json:"coupon_code,omitempty"`
	CouponDiscount          float64    `db:"coupon_discount" json:"coupon_discount,omitempty"`
	FinalAmount             float64    `db:"final_amount" json:"final_amount,omitempty"`
	CurrencyCode            string     `db:"currency_code" json:"currency_code,omitempty"`
	PaymentMethod           *string    `db:"payment_method" json:"payment_method,omitempty"`
	PaymentStatus           string     `db:"payment_status" json:"payment_status,omitempty"`
	PaymentID               *string    `db:"payment_id" json:"payment_id,omitempty"`
	PaymentGateway          *string    `db:"payment_gateway" json:"payment_gateway,omitempty"`
	DeliveryAddressID       *uuid.UUID `db:"delivery_address_id" json:"delivery_address_id,omitempty"`
	DeliveryAddressSnapshot []byte     `db:"delivery_address_snapshot" json:"delivery_address_snapshot,omitempty"`
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
	ID              uuid.UUID  `db:"id" json:"id,omitempty"`
	OrderID         uuid.UUID  `db:"order_id" json:"order_id,omitempty"`
	UserID          uuid.UUID  `db:"user_id" json:"user_id,omitempty"`
	Amount          float64    `db:"amount" json:"amount,omitempty"`
	Currency        string     `db:"currency" json:"currency,omitempty"`
	PaymentMethod   string     `db:"payment_method" json:"payment_method,omitempty"`
	Gateway         string     `db:"gateway" json:"gateway,omitempty"`
	GatewayOrderID  *string    `db:"gateway_order_id" json:"gateway_order_id,omitempty"`
	GatewayTxnID    *string    `db:"gateway_txn_id" json:"gateway_txn_id,omitempty"`
	Status          string     `db:"status" json:"status,omitempty"`
	IdempotencyKey  *string    `db:"idempotency_key" json:"idempotency_key,omitempty"`
	RawResponse     []byte     `db:"raw_response" json:"raw_response,omitempty"`
	FailureReason   *string    `db:"failure_reason" json:"failure_reason,omitempty"`
	InitiatedAt     time.Time  `db:"initiated_at" json:"initiated_at,omitempty"`
	CompletedAt     *time.Time `db:"completed_at" json:"completed_at,omitempty"`
}

// ─── Shipping ────────────────────────────────────────────────

type ShippingPackage struct {
	ID                  uuid.UUID  `db:"id" json:"id,omitempty"`
	OrderID             uuid.UUID  `db:"order_id" json:"order_id,omitempty"`
	SellerID            uuid.UUID  `db:"seller_id" json:"seller_id,omitempty"`
	ShippingPartnerID   *uuid.UUID `db:"shipping_partner_id" json:"shipping_partner_id,omitempty"`
	AWBNumber           *string    `db:"awb_number" json:"awb_number,omitempty"`
	TrackingURL         *string    `db:"tracking_url" json:"tracking_url,omitempty"`
	WeightGrams         *int       `db:"weight_grams" json:"weight_grams,omitempty"`
	CurrentStatus       string     `db:"current_status" json:"current_status,omitempty"`
	CurrentLocation     *string    `db:"current_location" json:"current_location,omitempty"`
	EstimatedDelivery   *time.Time `db:"estimated_delivery_date" json:"estimated_delivery_date,omitempty"`
	DeliveredAt         *time.Time `db:"delivered_at" json:"delivered_at,omitempty"`
	PickedUpAt          *time.Time `db:"picked_up_at" json:"picked_up_at,omitempty"`
	CreatedAt           time.Time  `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt           time.Time  `db:"updated_at" json:"updated_at,omitempty"`
}

// ─── Review ──────────────────────────────────────────────────

type Review struct {
	ID                  uuid.UUID `db:"id" json:"id,omitempty"`
	ProductID           uuid.UUID `db:"product_id" json:"product_id,omitempty"`
	SellerID            uuid.UUID `db:"seller_id" json:"seller_id,omitempty"`
	OrderItemID         uuid.UUID `db:"order_item_id" json:"order_item_id,omitempty"`
	ReviewerID          uuid.UUID `db:"reviewer_id" json:"reviewer_id,omitempty"`
	Rating              int       `db:"rating" json:"rating,omitempty"`
	Title               *string   `db:"title" json:"title,omitempty"`
	Body                *string   `db:"body" json:"body,omitempty"`
	IsVerifiedPurchase  bool      `db:"is_verified_purchase" json:"is_verified_purchase,omitempty"`
	IsPublished         bool      `db:"is_published" json:"is_published,omitempty"`
	HelpfulCount        int       `db:"helpful_count" json:"helpful_count,omitempty"`
	ModerationStatus    string    `db:"moderation_status" json:"moderation_status,omitempty"`
	SellerResponse      *string   `db:"seller_response" json:"seller_response,omitempty"`
	SellerRespondedAt   *time.Time `db:"seller_responded_at" json:"seller_responded_at,omitempty"`
	CreatedAt           time.Time `db:"created_at" json:"created_at,omitempty"`
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
	ID                 uuid.UUID  `db:"id" json:"id,omitempty"`
	SellerID           *uuid.UUID `db:"seller_id" json:"seller_id,omitempty"`
	Code               string     `db:"code" json:"code,omitempty"`
	Description        *string    `db:"description" json:"description,omitempty"`
	DiscountType       string     `db:"discount_type" json:"discount_type,omitempty"`
	DiscountValue      float64    `db:"discount_value" json:"discount_value,omitempty"`
	MaxDiscountAmount  *float64   `db:"max_discount_amount" json:"max_discount_amount,omitempty"`
	MinOrderAmount     float64    `db:"min_order_amount" json:"min_order_amount,omitempty"`
	MaxUses            *int       `db:"max_uses" json:"max_uses,omitempty"`
	UsesCount          int        `db:"uses_count" json:"uses_count,omitempty"`
	MaxUsesPerUser     int        `db:"max_uses_per_user" json:"max_uses_per_user,omitempty"`
	ApplicableTo       string     `db:"applicable_to" json:"applicable_to,omitempty"`
	IsActive           bool       `db:"is_active" json:"is_active,omitempty"`
	StartsAt           time.Time  `db:"starts_at" json:"starts_at,omitempty"`
	ExpiresAt          *time.Time `db:"expires_at" json:"expires_at,omitempty"`
}

// ─── Customer Address ────────────────────────────────────────

type CustomerAddress struct {
	ID             uuid.UUID `db:"id" json:"id,omitempty"`
	UserID         uuid.UUID `db:"user_id" json:"user_id,omitempty"`
	Label          string    `db:"label" json:"label,omitempty"`
	ContactName    string    `db:"contact_name" json:"contact_name,omitempty"`
	Phone          string    `db:"phone" json:"phone,omitempty"`
	AddressLine1   string    `db:"address_line_1" json:"address_line_1,omitempty"`
	AddressLine2   *string   `db:"address_line_2" json:"address_line_2,omitempty"`
	Landmark       *string   `db:"landmark" json:"landmark,omitempty"`
	City           string    `db:"city" json:"city,omitempty"`
	State          string    `db:"state" json:"state,omitempty"`
	Country        string    `db:"country" json:"country,omitempty"`
	PostalCode     string    `db:"postal_code" json:"postal_code,omitempty"`
	AddressType    string    `db:"address_type" json:"address_type,omitempty"`
	IsDefault      bool      `db:"is_default" json:"is_default,omitempty"`
	CreatedAt      time.Time `db:"created_at" json:"created_at,omitempty"`
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
	ID               uuid.UUID  `db:"id" json:"id,omitempty"`
	BatchDate        time.Time  `db:"batch_date" json:"batch_date,omitempty"`
	CycleStart       time.Time  `db:"payout_cycle_start" json:"payout_cycle_start,omitempty"`
	CycleEnd         time.Time  `db:"payout_cycle_end" json:"payout_cycle_end,omitempty"`
	TotalSellers     int        `db:"total_sellers" json:"total_sellers,omitempty"`
	TotalAmount      float64    `db:"total_amount" json:"total_amount,omitempty"`
	Status           string     `db:"status" json:"status,omitempty"`
	ProcessedAt      *time.Time `db:"processed_at" json:"processed_at,omitempty"`
	CreatedAt        time.Time  `db:"created_at" json:"created_at,omitempty"`
}

type PayoutTransaction struct {
	ID               uuid.UUID  `db:"id" json:"id,omitempty"`
	BatchID          uuid.UUID  `db:"batch_id" json:"batch_id,omitempty"`
	SellerID         uuid.UUID  `db:"seller_id" json:"seller_id,omitempty"`
	GrossAmount      float64    `db:"gross_amount" json:"gross_amount,omitempty"`
	CommissionAmount float64    `db:"commission_amount" json:"commission_amount,omitempty"`
	PlatformFee      float64    `db:"platform_fee" json:"platform_fee,omitempty"`
	TaxDeducted      float64    `db:"tax_deducted" json:"tax_deducted,omitempty"`
	AdjustmentAmount float64    `db:"adjustment_amount" json:"adjustment_amount,omitempty"`
	NetAmount        float64    `db:"net_amount" json:"net_amount,omitempty"`
	BankAccountID    *uuid.UUID `db:"bank_account_id" json:"bank_account_id,omitempty"`
	TransferReference *string   `db:"transfer_reference" json:"transfer_reference,omitempty"`
	Status           string     `db:"status" json:"status,omitempty"`
	FailureReason    *string    `db:"failure_reason" json:"failure_reason,omitempty"`
	InitiatedAt      time.Time  `db:"initiated_at" json:"initiated_at,omitempty"`
	CompletedAt      *time.Time `db:"completed_at" json:"completed_at,omitempty"`
}

// ─── Phase 5 — B2B / Organizations ───────────────────────────

type Organization struct {
	ID                 uuid.UUID  `db:"id" json:"id"`
	Name               string     `db:"name" json:"name"`
	LegalName          *string    `db:"legal_name" json:"legal_name,omitempty"`
	GSTIN              *string    `db:"gstin" json:"gstin,omitempty"`
	PAN                *string    `db:"pan" json:"pan,omitempty"`
	BillingEmail       *string    `db:"billing_email" json:"billing_email,omitempty"`
	BillingPhone       *string    `db:"billing_phone" json:"billing_phone,omitempty"`
	BillingAddressID   *uuid.UUID `db:"billing_address_id" json:"billing_address_id,omitempty"`
	ApprovalThreshold  *float64   `db:"approval_threshold" json:"approval_threshold,omitempty"`
	CreditTermsDays    int        `db:"credit_terms_days" json:"credit_terms_days"`
	CreditLimit        *float64   `db:"credit_limit" json:"credit_limit,omitempty"`
	Status             string     `db:"status" json:"status"`
	CreatedByUserID    *uuid.UUID `db:"created_by_user_id" json:"created_by_user_id,omitempty"`
	CreatedAt          time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time  `db:"updated_at" json:"updated_at"`
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
