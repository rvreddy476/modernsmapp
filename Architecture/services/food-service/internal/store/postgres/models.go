package postgres

import "github.com/google/uuid"

type Pagination struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type Cuisine struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	ImageURL  string    `json:"image_url,omitempty"`
	SortOrder int       `json:"sort_order"`
}

type RestaurantSummary struct {
	ID                  uuid.UUID `json:"id"`
	Name                string    `json:"name"`
	Slug                string    `json:"slug"`
	Description         string    `json:"description,omitempty"`
	City                string    `json:"city"`
	State               string    `json:"state,omitempty"`
	Status              string    `json:"status"`
	IsOpen              bool      `json:"is_open"`
	IsAcceptingOrders   bool      `json:"is_accepting_orders"`
	AvgRating           float64   `json:"avg_rating"`
	RatingCount         int       `json:"rating_count"`
	MinOrderAmount      float64   `json:"min_order_amount"`
	PackagingFee        float64   `json:"packaging_fee"`
	AvgPreparationMins  int       `json:"avg_preparation_minutes"`
	HeroImageURL        string    `json:"hero_image_url,omitempty"`
	Cuisines            []string  `json:"cuisines"`
	EstimatedDelivery   string    `json:"estimated_delivery"`
	DeliveryFeeEstimate float64   `json:"delivery_fee_estimate"`
}

type RestaurantDetail struct {
	RestaurantSummary
	Phone       string  `json:"phone,omitempty"`
	Email       string  `json:"email,omitempty"`
	AddressLine string  `json:"address_line"`
	PostalCode  string  `json:"postal_code,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
}

type MenuCategory struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	SortOrder   int        `json:"sort_order"`
	Items       []MenuItem `json:"items"`
}

type MenuItem struct {
	ID                 uuid.UUID `json:"id"`
	RestaurantID       uuid.UUID `json:"restaurant_id"`
	CategoryID         uuid.UUID `json:"category_id,omitempty"`
	Name               string    `json:"name"`
	Description        string    `json:"description,omitempty"`
	FoodType           string    `json:"food_type"`
	BasePrice          float64   `json:"base_price"`
	DiscountPrice      float64   `json:"discount_price,omitempty"`
	ImageURL           string    `json:"image_url,omitempty"`
	PreparationMinutes int       `json:"preparation_minutes"`
	IsAvailable        bool      `json:"is_available"`
	IsRecommended      bool      `json:"is_recommended"`
	TaxPercentage      float64   `json:"tax_percentage"`
}

type Cart struct {
	ID           uuid.UUID      `json:"id"`
	UserID       uuid.UUID      `json:"user_id"`
	RestaurantID *uuid.UUID     `json:"restaurant_id,omitempty"`
	Restaurant   string         `json:"restaurant,omitempty"`
	CouponCode   string         `json:"coupon_code,omitempty"`
	Items        []CartItem     `json:"items"`
	Totals       PriceBreakdown `json:"totals"`
}

type CartItem struct {
	ID              uuid.UUID  `json:"id"`
	RestaurantID    uuid.UUID  `json:"restaurant_id"`
	MenuItemID      uuid.UUID  `json:"menu_item_id"`
	VariantID       *uuid.UUID `json:"variant_id,omitempty"`
	Name            string     `json:"name"`
	ImageURL        string     `json:"image_url,omitempty"`
	FoodType        string     `json:"food_type"`
	Quantity        int        `json:"quantity"`
	UnitPrice       float64    `json:"unit_price"`
	TaxPercentage   float64    `json:"tax_percentage"`
	TaxAmount       float64    `json:"tax_amount"`
	LineTotal       float64    `json:"line_total"`
	ItemInstruction string     `json:"item_instruction,omitempty"`
}

type PriceBreakdown struct {
	ItemSubtotal       float64 `json:"item_subtotal"`
	AddonTotal         float64 `json:"addon_total"`
	PackagingFee       float64 `json:"packaging_fee"`
	TaxTotal           float64 `json:"tax_total"`
	DeliveryFee        float64 `json:"delivery_fee"`
	PlatformFee        float64 `json:"platform_fee"`
	RestaurantDiscount float64 `json:"restaurant_discount"`
	CouponDiscount     float64 `json:"coupon_discount"`
	FinalAmount        float64 `json:"final_amount"`
}

type Address struct {
	ID           uuid.UUID `json:"id"`
	UserID       uuid.UUID `json:"user_id"`
	Label        string    `json:"label,omitempty"`
	ReceiverName string    `json:"receiver_name,omitempty"`
	Phone        string    `json:"phone,omitempty"`
	AddressLine1 string    `json:"address_line1"`
	AddressLine2 string    `json:"address_line2,omitempty"`
	Landmark     string    `json:"landmark,omitempty"`
	City         string    `json:"city"`
	State        string    `json:"state,omitempty"`
	Country      string    `json:"country"`
	PostalCode   string    `json:"postal_code,omitempty"`
	Latitude     float64   `json:"latitude,omitempty"`
	Longitude    float64   `json:"longitude,omitempty"`
	IsDefault    bool      `json:"is_default"`
}

type AddressInput struct {
	Label        string
	ReceiverName string
	Phone        string
	AddressLine1 string
	AddressLine2 string
	Landmark     string
	City         string
	State        string
	Country      string
	PostalCode   string
	Latitude     *float64
	Longitude    *float64
	IsDefault    bool
}

type Order struct {
	ID                    uuid.UUID            `json:"id"`
	OrderNumber           string               `json:"order_number"`
	UserID                uuid.UUID            `json:"user_id"`
	RestaurantID          uuid.UUID            `json:"restaurant_id"`
	RestaurantName        string               `json:"restaurant_name"`
	Status                string               `json:"status"`
	PaymentStatus         string               `json:"payment_status"`
	PaymentMethod         string               `json:"payment_method"`
	Totals                PriceBreakdown       `json:"totals"`
	EstimatedPrepMins     int                  `json:"estimated_preparation_minutes,omitempty"`
	EstimatedDeliveryMins int                  `json:"estimated_delivery_minutes,omitempty"`
	PlacedAt              string               `json:"placed_at"`
	DeliveredAt           string               `json:"delivered_at,omitempty"`
	Items                 []OrderItem          `json:"items,omitempty"`
	History               []OrderStatusHistory `json:"history,omitempty"`
}

type WalletPaymentChargeDetails struct {
	OrderID           uuid.UUID `json:"order_id"`
	OrderNumber       string    `json:"order_number"`
	UserID            uuid.UUID `json:"user_id"`
	RestaurantOwnerID uuid.UUID `json:"restaurant_owner_id"`
	PaymentMethod     string    `json:"payment_method"`
	PaymentStatus     string    `json:"payment_status"`
	Amount            float64   `json:"amount"`
}

type PaymentIntegrationDetails struct {
	OrderID           uuid.UUID `json:"order_id"`
	OrderNumber       string    `json:"order_number"`
	UserID            uuid.UUID `json:"user_id"`
	RestaurantOwnerID uuid.UUID `json:"restaurant_owner_id"`
	PaymentMethod     string    `json:"payment_method"`
	PaymentStatus     string    `json:"payment_status"`
	ProviderPaymentID string    `json:"provider_payment_id,omitempty"`
	ProviderOrderID   string    `json:"provider_order_id,omitempty"`
	Amount            float64   `json:"amount"`
}

type SettlementGenerateInput struct {
	PeriodStart       string
	PeriodEnd         string
	RestaurantID      *uuid.UUID
	DeliveryPartnerID *uuid.UUID
	IdempotencyKey    string
}

type OrderItem struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	FoodType    string    `json:"food_type"`
	UnitPrice   float64   `json:"unit_price"`
	Quantity    int       `json:"quantity"`
	TaxAmount   float64   `json:"tax_amount"`
	LineTotal   float64   `json:"line_total"`
	Instruction string    `json:"instruction,omitempty"`
}

type OrderStatusHistory struct {
	FromStatus string `json:"from_status,omitempty"`
	ToStatus   string `json:"to_status"`
	Reason     string `json:"reason,omitempty"`
	CreatedAt  string `json:"created_at"`
}

type PartnerRestaurantInput struct {
	LegalName      string
	DisplayName    string
	Name           string
	Slug           string
	Description    string
	Phone          string
	Email          string
	AddressLine1   string
	AddressLine2   string
	City           string
	State          string
	PostalCode     string
	Latitude       *float64
	Longitude      *float64
	MinOrderAmount float64
	PackagingFee   float64
}

type PartnerRestaurant struct {
	ID                uuid.UUID `json:"id"`
	PartnerID         uuid.UUID `json:"partner_id"`
	OwnerUserID       uuid.UUID `json:"owner_user_id"`
	Name              string    `json:"name"`
	Slug              string    `json:"slug"`
	Description       string    `json:"description,omitempty"`
	Status            string    `json:"status"`
	IsOpen            bool      `json:"is_open"`
	IsAcceptingOrders bool      `json:"is_accepting_orders"`
	City              string    `json:"city"`
	State             string    `json:"state,omitempty"`
	MinOrderAmount    float64   `json:"min_order_amount"`
	PackagingFee      float64   `json:"packaging_fee"`
	CreatedAt         string    `json:"created_at"`
}

type MenuCategoryInput struct {
	Name        string
	Description string
	SortOrder   int
}

type MenuItemInput struct {
	Name               string
	Description        string
	FoodType           string
	BasePrice          float64
	DiscountPrice      *float64
	ImageURL           string
	PreparationMinutes int
	IsRecommended      bool
	TaxPercentage      float64
}

type DeliveryPartnerInput struct {
	FullName      string
	Phone         string
	Email         string
	VehicleType   string
	VehicleNumber string
	City          string
}

type DeliveryPartner struct {
	ID            uuid.UUID `json:"id"`
	UserID        uuid.UUID `json:"user_id"`
	FullName      string    `json:"full_name"`
	Phone         string    `json:"phone"`
	Email         string    `json:"email,omitempty"`
	Status        string    `json:"status"`
	VehicleType   string    `json:"vehicle_type,omitempty"`
	VehicleNumber string    `json:"vehicle_number,omitempty"`
	City          string    `json:"city,omitempty"`
	IsOnline      bool      `json:"is_online"`
	CreatedAt     string    `json:"created_at"`
}

type DeliveryAssignment struct {
	ID                    uuid.UUID  `json:"id"`
	OrderID               uuid.UUID  `json:"order_id"`
	OrderNumber           string     `json:"order_number"`
	RestaurantName        string     `json:"restaurant_name"`
	RestaurantID          uuid.UUID  `json:"restaurant_id"`
	DeliveryPartnerID     *uuid.UUID `json:"delivery_partner_id,omitempty"`
	Status                string     `json:"status"`
	OrderStatus           string     `json:"order_status"`
	DeliveryFee           float64    `json:"delivery_fee"`
	DeliveryPartnerPayout float64    `json:"delivery_partner_payout"`
	CreatedAt             string     `json:"created_at"`
}

type AdminDashboard struct {
	TotalOrdersToday        int     `json:"total_orders_today"`
	GMVToday                float64 `json:"gmv_today"`
	CancelledOrdersToday    int     `json:"cancelled_orders_today"`
	ActiveRestaurants       int     `json:"active_restaurants"`
	PendingRestaurants      int     `json:"pending_restaurants"`
	PendingDeliveryPartners int     `json:"pending_delivery_partners"`
	OnlineDeliveryPartners  int     `json:"online_delivery_partners"`
}
