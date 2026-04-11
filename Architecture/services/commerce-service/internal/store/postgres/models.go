package postgres

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors
var (
	ErrAlreadySubmitted = errors.New("seller application already submitted")
	ErrSellerNotApproved = errors.New("seller account not yet approved")
	ErrProductNotDraft   = errors.New("product is not in draft status")
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
	ID           uuid.UUID  `db:"id"`
	ParentID     *uuid.UUID `db:"parent_id"`
	Name         string     `db:"name"`
	Slug         string     `db:"slug"`
	Description  *string    `db:"description"`
	DisplayOrder int        `db:"display_order"`
	IsActive     bool       `db:"is_active"`
	IsFeatured   bool       `db:"is_featured"`
	CreatedAt    time.Time  `db:"created_at"`
}

// ─── Product ─────────────────────────────────────────────────

type Product struct {
	ID                   uuid.UUID  `db:"id"`
	SellerID             uuid.UUID  `db:"seller_id"`
	CategoryID           *uuid.UUID `db:"category_id"`
	BrandID              *uuid.UUID `db:"brand_id"`
	TaxClassID           *uuid.UUID `db:"tax_class_id"`
	Title                string     `db:"title"`
	ShortTitle           *string    `db:"short_title"`
	Slug                 string     `db:"slug"`
	Description          *string    `db:"description"`
	ShortDescription     *string    `db:"short_description"`
	ProductType          string     `db:"product_type"`
	Condition            string     `db:"condition"`
	SKURoot              *string    `db:"sku_root"`
	Status               string     `db:"status"`
	Visibility           string     `db:"visibility"`
	ApprovalStatus       string     `db:"approval_status"`
	RejectionReason      *string    `db:"rejection_reason"`
	PrimaryImageMediaID  *uuid.UUID `db:"primary_image_media_id"`
	WeightGrams          *int       `db:"weight_grams"`
	CountryOfOrigin      *string    `db:"country_of_origin"`
	WarrantyInfo         *string    `db:"warranty_info"`
	ReturnPolicyType     string     `db:"return_policy_type"`
	ReturnPolicyDays     int        `db:"return_policy_days"`
	HSNCode              *string    `db:"hsn_code"`
	MetaTitle            *string    `db:"meta_title"`
	MetaDescription      *string    `db:"meta_description"`
	AvgRating            float64    `db:"avg_rating"`
	ReviewCount          int        `db:"review_count"`
	OrderCount           int        `db:"order_count"`
	ViewCount            int64      `db:"view_count"`
	WishlistCount        int        `db:"wishlist_count"`
	IsFeatured           bool       `db:"is_featured"`
	CreatedAt            time.Time  `db:"created_at"`
	UpdatedAt            time.Time  `db:"updated_at"`
	PublishedAt          *time.Time `db:"published_at"`
}

// ─── Product Variant ─────────────────────────────────────────

type ProductVariant struct {
	ID             uuid.UUID  `db:"id"`
	ProductID      uuid.UUID  `db:"product_id"`
	SKU            string     `db:"sku"`
	Barcode        *string    `db:"barcode"`
	Option1Name    *string    `db:"option_1_name"`
	Option1Value   *string    `db:"option_1_value"`
	Option2Name    *string    `db:"option_2_name"`
	Option2Value   *string    `db:"option_2_value"`
	Option3Name    *string    `db:"option_3_name"`
	Option3Value   *string    `db:"option_3_value"`
	MRP            float64    `db:"mrp"`
	SellingPrice   float64    `db:"selling_price"`
	CostPrice      *float64   `db:"cost_price"`
	CurrencyCode   string     `db:"currency_code"`
	Status         string     `db:"status"`
	ImageMediaID   *uuid.UUID `db:"image_media_id"`
	WeightGrams    *int       `db:"weight_grams"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
}

// ─── Inventory ───────────────────────────────────────────────

type InventoryItem struct {
	ID            uuid.UUID `db:"id"`
	VariantID     uuid.UUID `db:"variant_id"`
	SellerID      uuid.UUID `db:"seller_id"`
	TotalQty      int       `db:"total_qty"`
	ReservedQty   int       `db:"reserved_qty"`
	DamagedQty    int       `db:"damaged_qty"`
	ReturnedQty   int       `db:"returned_qty"`
	SafetyStock   int       `db:"safety_stock"`
	LowStockAlert int       `db:"low_stock_alert"`
	UpdatedAt     time.Time `db:"updated_at"`
}

func (i *InventoryItem) AvailableQty() int {
	return i.TotalQty - i.ReservedQty
}

// ─── Cart ────────────────────────────────────────────────────

type Cart struct {
	ID        uuid.UUID  `db:"id"`
	UserID    uuid.UUID  `db:"user_id"`
	ExpiresAt *time.Time `db:"expires_at"`
	UpdatedAt time.Time  `db:"updated_at"`
}

type CartItem struct {
	ID            uuid.UUID `db:"id"`
	CartID        uuid.UUID `db:"cart_id"`
	VariantID     uuid.UUID `db:"variant_id"`
	ProductID     uuid.UUID `db:"product_id"`
	Quantity      int       `db:"quantity"`
	PriceSnapshot float64   `db:"price_snapshot"`
	AddedAt       time.Time `db:"added_at"`
}

// ─── Order ───────────────────────────────────────────────────

type Order struct {
	ID                      uuid.UUID  `db:"id"`
	CustomerUserID          uuid.UUID  `db:"customer_user_id"`
	OrderNumber             string     `db:"order_number"`
	Subtotal                float64    `db:"subtotal"`
	DiscountAmount          float64    `db:"discount_amount"`
	ShippingCharges         float64    `db:"shipping_charges"`
	TaxAmount               float64    `db:"tax_amount"`
	CouponCode              *string    `db:"coupon_code"`
	CouponDiscount          float64    `db:"coupon_discount"`
	FinalAmount             float64    `db:"final_amount"`
	CurrencyCode            string     `db:"currency_code"`
	PaymentMethod           *string    `db:"payment_method"`
	PaymentStatus           string     `db:"payment_status"`
	PaymentID               *string    `db:"payment_id"`
	PaymentGateway          *string    `db:"payment_gateway"`
	DeliveryAddressID       *uuid.UUID `db:"delivery_address_id"`
	DeliveryAddressSnapshot []byte     `db:"delivery_address_snapshot"`
	GiftMessage             *string    `db:"gift_message"`
	Status                  string     `db:"status"`
	CancellationReason      *string    `db:"cancellation_reason"`
	CancelledBy             *string    `db:"cancelled_by"`
	IdempotencyKey          *string    `db:"idempotency_key"`
	CreatedAt               time.Time  `db:"created_at"`
	UpdatedAt               time.Time  `db:"updated_at"`
}

type OrderItem struct {
	ID                  uuid.UUID  `db:"id"`
	OrderID             uuid.UUID  `db:"order_id"`
	ProductID           uuid.UUID  `db:"product_id"`
	VariantID           uuid.UUID  `db:"variant_id"`
	SellerID            uuid.UUID  `db:"seller_id"`
	ProductTitle        string     `db:"product_title"`
	VariantDetails      []byte     `db:"variant_details"`
	SKU                 string     `db:"sku"`
	Quantity            int        `db:"quantity"`
	UnitMRP             float64    `db:"unit_mrp"`
	UnitPrice           float64    `db:"unit_price"`
	DiscountAmount      float64    `db:"discount_amount"`
	TaxAmount           float64    `db:"tax_amount"`
	FinalPrice          float64    `db:"final_price"`
	Status              string     `db:"status"`
	ShipmentID          *uuid.UUID `db:"shipment_id"`
	TrackingNumber      *string    `db:"tracking_number"`
	ReturnEligibleUntil *time.Time `db:"return_eligible_until"`
	DeliveredAt         *time.Time `db:"delivered_at"`
	CreatedAt           time.Time  `db:"created_at"`
}

type OrderStatusHistory struct {
	ID         uuid.UUID  `db:"id"`
	OrderID    uuid.UUID  `db:"order_id"`
	FromStatus *string    `db:"from_status"`
	ToStatus   string     `db:"to_status"`
	ChangedBy  *uuid.UUID `db:"changed_by"`
	ActorType  string     `db:"actor_type"`
	Notes      *string    `db:"notes"`
	CreatedAt  time.Time  `db:"created_at"`
}

// ─── Payment ─────────────────────────────────────────────────

type Payment struct {
	ID              uuid.UUID  `db:"id"`
	OrderID         uuid.UUID  `db:"order_id"`
	UserID          uuid.UUID  `db:"user_id"`
	Amount          float64    `db:"amount"`
	Currency        string     `db:"currency"`
	PaymentMethod   string     `db:"payment_method"`
	Gateway         string     `db:"gateway"`
	GatewayOrderID  *string    `db:"gateway_order_id"`
	GatewayTxnID    *string    `db:"gateway_txn_id"`
	Status          string     `db:"status"`
	IdempotencyKey  *string    `db:"idempotency_key"`
	RawResponse     []byte     `db:"raw_response"`
	FailureReason   *string    `db:"failure_reason"`
	InitiatedAt     time.Time  `db:"initiated_at"`
	CompletedAt     *time.Time `db:"completed_at"`
}

// ─── Shipping ────────────────────────────────────────────────

type ShippingPackage struct {
	ID                  uuid.UUID  `db:"id"`
	OrderID             uuid.UUID  `db:"order_id"`
	SellerID            uuid.UUID  `db:"seller_id"`
	ShippingPartnerID   *uuid.UUID `db:"shipping_partner_id"`
	AWBNumber           *string    `db:"awb_number"`
	TrackingURL         *string    `db:"tracking_url"`
	WeightGrams         *int       `db:"weight_grams"`
	CurrentStatus       string     `db:"current_status"`
	CurrentLocation     *string    `db:"current_location"`
	EstimatedDelivery   *time.Time `db:"estimated_delivery_date"`
	DeliveredAt         *time.Time `db:"delivered_at"`
	PickedUpAt          *time.Time `db:"picked_up_at"`
	CreatedAt           time.Time  `db:"created_at"`
	UpdatedAt           time.Time  `db:"updated_at"`
}

// ─── Review ──────────────────────────────────────────────────

type Review struct {
	ID                  uuid.UUID `db:"id"`
	ProductID           uuid.UUID `db:"product_id"`
	SellerID            uuid.UUID `db:"seller_id"`
	OrderItemID         uuid.UUID `db:"order_item_id"`
	ReviewerID          uuid.UUID `db:"reviewer_id"`
	Rating              int       `db:"rating"`
	Title               *string   `db:"title"`
	Body                *string   `db:"body"`
	IsVerifiedPurchase  bool      `db:"is_verified_purchase"`
	IsPublished         bool      `db:"is_published"`
	HelpfulCount        int       `db:"helpful_count"`
	CreatedAt           time.Time `db:"created_at"`
}

// ─── Return Request ──────────────────────────────────────────

type ReturnRequest struct {
	ID                uuid.UUID  `db:"id"`
	OrderID           uuid.UUID  `db:"order_id"`
	OrderItemID       uuid.UUID  `db:"order_item_id"`
	CustomerUserID    uuid.UUID  `db:"customer_user_id"`
	SellerID          uuid.UUID  `db:"seller_id"`
	ReasonCode        string     `db:"reason_code"`
	ReasonDescription *string    `db:"reason_description"`
	Status            string     `db:"status"`
	RequestedAt       time.Time  `db:"requested_at"`
	ApprovedAt        *time.Time `db:"approved_at"`
	RejectedAt        *time.Time `db:"rejected_at"`
	RejectionReason   *string    `db:"rejection_reason"`
	RefundAmount      *float64   `db:"refund_amount"`
}

// ─── Coupon ──────────────────────────────────────────────────

type Coupon struct {
	ID                 uuid.UUID  `db:"id"`
	SellerID           *uuid.UUID `db:"seller_id"`
	Code               string     `db:"code"`
	Description        *string    `db:"description"`
	DiscountType       string     `db:"discount_type"`
	DiscountValue      float64    `db:"discount_value"`
	MaxDiscountAmount  *float64   `db:"max_discount_amount"`
	MinOrderAmount     float64    `db:"min_order_amount"`
	MaxUses            *int       `db:"max_uses"`
	UsesCount          int        `db:"uses_count"`
	MaxUsesPerUser     int        `db:"max_uses_per_user"`
	ApplicableTo       string     `db:"applicable_to"`
	IsActive           bool       `db:"is_active"`
	StartsAt           time.Time  `db:"starts_at"`
	ExpiresAt          *time.Time `db:"expires_at"`
}

// ─── Customer Address ────────────────────────────────────────

type CustomerAddress struct {
	ID             uuid.UUID `db:"id"`
	UserID         uuid.UUID `db:"user_id"`
	Label          string    `db:"label"`
	ContactName    string    `db:"contact_name"`
	Phone          string    `db:"phone"`
	AddressLine1   string    `db:"address_line_1"`
	AddressLine2   *string   `db:"address_line_2"`
	Landmark       *string   `db:"landmark"`
	City           string    `db:"city"`
	State          string    `db:"state"`
	Country        string    `db:"country"`
	PostalCode     string    `db:"postal_code"`
	AddressType    string    `db:"address_type"`
	IsDefault      bool      `db:"is_default"`
	CreatedAt      time.Time `db:"created_at"`
}

// ─── Support Ticket ──────────────────────────────────────────

type SupportTicket struct {
	ID          uuid.UUID  `db:"id"`
	UserID      uuid.UUID  `db:"user_id"`
	SellerID    *uuid.UUID `db:"seller_id"`
	OrderID     *uuid.UUID `db:"order_id"`
	Category    string     `db:"category"`
	Subject     string     `db:"subject"`
	Description string     `db:"description"`
	Priority    string     `db:"priority"`
	Status      string     `db:"status"`
	AssignedTo  *uuid.UUID `db:"assigned_to"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
}

// ─── Payout ──────────────────────────────────────────────────

type PayoutBatch struct {
	ID               uuid.UUID  `db:"id"`
	BatchDate        time.Time  `db:"batch_date"`
	CycleStart       time.Time  `db:"payout_cycle_start"`
	CycleEnd         time.Time  `db:"payout_cycle_end"`
	TotalSellers     int        `db:"total_sellers"`
	TotalAmount      float64    `db:"total_amount"`
	Status           string     `db:"status"`
	ProcessedAt      *time.Time `db:"processed_at"`
	CreatedAt        time.Time  `db:"created_at"`
}

type PayoutTransaction struct {
	ID               uuid.UUID  `db:"id"`
	BatchID          uuid.UUID  `db:"batch_id"`
	SellerID         uuid.UUID  `db:"seller_id"`
	GrossAmount      float64    `db:"gross_amount"`
	CommissionAmount float64    `db:"commission_amount"`
	PlatformFee      float64    `db:"platform_fee"`
	TaxDeducted      float64    `db:"tax_deducted"`
	AdjustmentAmount float64    `db:"adjustment_amount"`
	NetAmount        float64    `db:"net_amount"`
	BankAccountID    *uuid.UUID `db:"bank_account_id"`
	TransferReference *string   `db:"transfer_reference"`
	Status           string     `db:"status"`
	FailureReason    *string    `db:"failure_reason"`
	InitiatedAt      time.Time  `db:"initiated_at"`
	CompletedAt      *time.Time `db:"completed_at"`
}
