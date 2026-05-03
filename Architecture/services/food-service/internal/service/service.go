package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

type Store interface {
	ListCuisines(ctx context.Context) ([]postgres.Cuisine, error)
	ListRestaurants(ctx context.Context, filter postgres.RestaurantFilter) ([]postgres.RestaurantSummary, error)
	GetRestaurant(ctx context.Context, id uuid.UUID) (*postgres.RestaurantDetail, error)
	GetMenu(ctx context.Context, restaurantID uuid.UUID) ([]postgres.MenuCategory, error)
	GetCart(ctx context.Context, userID uuid.UUID) (*postgres.Cart, error)
	AddCartItem(ctx context.Context, userID uuid.UUID, in postgres.AddCartItemInput) (*postgres.Cart, error)
	UpdateCartItem(ctx context.Context, userID, itemID uuid.UUID, in postgres.UpdateCartItemInput) (*postgres.Cart, error)
	RemoveCartItem(ctx context.Context, userID, itemID uuid.UUID) error
	ClearCart(ctx context.Context, userID uuid.UUID) error
	ApplyCoupon(ctx context.Context, userID uuid.UUID, code string) (*postgres.Cart, error)
	ListAddresses(ctx context.Context, userID uuid.UUID) ([]postgres.Address, error)
	CreateAddress(ctx context.Context, userID uuid.UUID, in postgres.AddressInput) (*postgres.Address, error)
	UpdateAddress(ctx context.Context, userID, addressID uuid.UUID, in postgres.AddressInput) (*postgres.Address, error)
	DeleteAddress(ctx context.Context, userID, addressID uuid.UUID) error
	PlaceOrder(ctx context.Context, userID uuid.UUID, in postgres.PlaceOrderInput, idempotencyKey string) (*postgres.Order, error)
	ListOrders(ctx context.Context, userID uuid.UUID) ([]postgres.Order, error)
	GetOrder(ctx context.Context, userID, orderID uuid.UUID) (*postgres.Order, error)
	WalletPaymentChargeDetails(ctx context.Context, userID, orderID uuid.UUID) (*postgres.WalletPaymentChargeDetails, error)
	PaymentIntegrationDetails(ctx context.Context, orderID uuid.UUID) (*postgres.PaymentIntegrationDetails, error)
	GetOrderTracking(ctx context.Context, userID, orderID uuid.UUID) (map[string]any, error)
	CreatePaymentIntent(ctx context.Context, userID, orderID uuid.UUID, method, idempotencyKey string) (map[string]any, error)
	AttachPaymentProviderReference(ctx context.Context, userID, orderID uuid.UUID, providerPaymentID, providerOrderID string, raw map[string]any) error
	ConfirmPayment(ctx context.Context, userID, orderID uuid.UUID, providerPaymentID, providerReference string) (*postgres.Order, error)
	CancelOrder(ctx context.Context, userID, orderID uuid.UUID, reason string) (*postgres.Order, error)
	RateRestaurant(ctx context.Context, userID, orderID uuid.UUID, rating int, review string) (map[string]any, error)
	RateDelivery(ctx context.Context, userID, orderID uuid.UUID, rating int, review string) (map[string]any, error)
	CreatePartnerRestaurant(ctx context.Context, ownerID uuid.UUID, in postgres.PartnerRestaurantInput) (*postgres.PartnerRestaurant, error)
	ListPartnerRestaurants(ctx context.Context, ownerID uuid.UUID) ([]postgres.PartnerRestaurant, error)
	GetPartnerRestaurant(ctx context.Context, ownerID, restaurantID uuid.UUID) (*postgres.PartnerRestaurant, error)
	UpdatePartnerRestaurant(ctx context.Context, ownerID, restaurantID uuid.UUID, in postgres.PartnerRestaurantInput) (*postgres.PartnerRestaurant, error)
	AddRestaurantDocument(ctx context.Context, ownerID, restaurantID uuid.UUID, input map[string]any) (map[string]any, error)
	AddRestaurantImage(ctx context.Context, ownerID, restaurantID uuid.UUID, input map[string]any) (map[string]any, error)
	CreateMenuCategory(ctx context.Context, ownerID, restaurantID uuid.UUID, in postgres.MenuCategoryInput) (*postgres.MenuCategory, error)
	ListMenuCategories(ctx context.Context, ownerID, restaurantID uuid.UUID) ([]postgres.MenuCategory, error)
	UpdateMenuCategory(ctx context.Context, ownerID, categoryID uuid.UUID, in postgres.MenuCategoryInput) (*postgres.MenuCategory, error)
	DeleteMenuCategory(ctx context.Context, ownerID, categoryID uuid.UUID) error
	CreateMenuItem(ctx context.Context, ownerID, restaurantID, categoryID uuid.UUID, in postgres.MenuItemInput) (*postgres.MenuItem, error)
	UpdateMenuItem(ctx context.Context, ownerID, itemID uuid.UUID, in postgres.MenuItemInput) (*postgres.MenuItem, error)
	DeleteMenuItem(ctx context.Context, ownerID, itemID uuid.UUID) error
	SetMenuItemAvailability(ctx context.Context, ownerID, itemID uuid.UUID, available bool) error
	ListPartnerOrders(ctx context.Context, ownerID, restaurantID uuid.UUID) ([]postgres.Order, error)
	PartnerUpdateOrderStatus(ctx context.Context, ownerID, orderID uuid.UUID, toStatus, reason, idempotencyKey string) (*postgres.Order, error)
	PartnerRestaurantSettlements(ctx context.Context, ownerID, restaurantID uuid.UUID) ([]map[string]any, error)
	PartnerRestaurantSummary(ctx context.Context, ownerID, restaurantID uuid.UUID) (map[string]any, error)
	UpsertDeliveryPartner(ctx context.Context, userID uuid.UUID, in postgres.DeliveryPartnerInput) (*postgres.DeliveryPartner, error)
	GetDeliveryPartner(ctx context.Context, userID uuid.UUID) (*postgres.DeliveryPartner, error)
	AddDeliveryDocument(ctx context.Context, userID uuid.UUID, input map[string]any) (map[string]any, error)
	SetDeliveryAvailability(ctx context.Context, userID uuid.UUID, online bool) (*postgres.DeliveryPartner, error)
	ListDeliveryAssignments(ctx context.Context, userID uuid.UUID) ([]postgres.DeliveryAssignment, error)
	GetCurrentDeliveryAssignment(ctx context.Context, userID uuid.UUID) (*postgres.DeliveryAssignment, error)
	DeliveryUpdateAssignment(ctx context.Context, userID, assignmentID uuid.UUID, toStatus, idempotencyKey string) (*postgres.DeliveryAssignment, error)
	UpdateDeliveryLocation(ctx context.Context, userID uuid.UUID, latitude, longitude float64, accuracyMeters *float64) (map[string]any, error)
	GetAssignmentTracking(ctx context.Context, userID, assignmentID uuid.UUID) (map[string]any, error)
	DeliveryEarnings(ctx context.Context, userID uuid.UUID) (map[string]any, error)
	DeliveryHistory(ctx context.Context, userID uuid.UUID) ([]postgres.DeliveryAssignment, error)
	AdminDashboard(ctx context.Context) (*postgres.AdminDashboard, error)
	AdminPendingRestaurants(ctx context.Context) ([]postgres.PartnerRestaurant, error)
	AdminApproveRestaurant(ctx context.Context, adminID, restaurantID uuid.UUID, approve bool, reason string) error
	AdminSetRestaurantStatus(ctx context.Context, adminID, restaurantID uuid.UUID, status, reason string) error
	AdminPendingDeliveryPartners(ctx context.Context) ([]postgres.DeliveryPartner, error)
	AdminApproveDeliveryPartner(ctx context.Context, adminID, partnerID uuid.UUID, approve bool, reason string) error
	AdminSetDeliveryPartnerStatus(ctx context.Context, adminID, partnerID uuid.UUID, status, reason string) error
	AdminListOrders(ctx context.Context, page postgres.Pagination) ([]postgres.Order, error)
	AdminGetOrder(ctx context.Context, orderID uuid.UUID) (*postgres.Order, error)
	AdminCancelOrder(ctx context.Context, adminID, orderID uuid.UUID, reason string) (*postgres.Order, error)
	AdminRefundOrder(ctx context.Context, adminID, orderID uuid.UUID, reason string, amount float64, idempotencyKey string) (map[string]any, error)
	AdminListCoupons(ctx context.Context) ([]map[string]any, error)
	AdminCreateCoupon(ctx context.Context, adminID uuid.UUID, input map[string]any) (map[string]any, error)
	AdminUpdateCoupon(ctx context.Context, adminID, couponID uuid.UUID, input map[string]any) (map[string]any, error)
	AdminListServiceAreas(ctx context.Context) ([]map[string]any, error)
	AdminCreateServiceArea(ctx context.Context, adminID uuid.UUID, input map[string]any) (map[string]any, error)
	AdminUpdateServiceArea(ctx context.Context, adminID, areaID uuid.UUID, input map[string]any) (map[string]any, error)
	AdminListRestaurantSettlements(ctx context.Context, page postgres.Pagination) ([]map[string]any, error)
	AdminMarkRestaurantSettlementPaid(ctx context.Context, adminID, settlementID uuid.UUID, reference string) (map[string]any, error)
	AdminListDeliverySettlements(ctx context.Context, page postgres.Pagination) ([]map[string]any, error)
	AdminMarkDeliverySettlementPaid(ctx context.Context, adminID, settlementID uuid.UUID, reference string) (map[string]any, error)
	AdminGenerateSettlements(ctx context.Context, adminID uuid.UUID, in postgres.SettlementGenerateInput) (map[string]any, error)
	AdminAuditLogs(ctx context.Context, page postgres.Pagination) ([]map[string]any, error)
	AdminOrderReport(ctx context.Context) (map[string]any, error)
	AdminRevenueReport(ctx context.Context) (map[string]any, error)
}

type Service struct {
	store           Store
	monetizationURL string
	paymentsURL     string
	internalKey     string
	httpClient      *http.Client
}

func New(store Store) *Service {
	return &Service{
		store:           store,
		monetizationURL: os.Getenv("MONETIZATION_SERVICE_URL"),
		paymentsURL:     os.Getenv("PAYMENTS_SERVICE_URL"),
		internalKey:     os.Getenv("INTERNAL_SERVICE_KEY"),
		httpClient:      &http.Client{Timeout: 8 * time.Second},
	}
}

type Home struct {
	Cuisines          []postgres.Cuisine           `json:"cuisines"`
	NearbyRestaurants []postgres.RestaurantSummary `json:"nearby_restaurants"`
	TopRated          []postgres.RestaurantSummary `json:"top_rated"`
	FastDelivery      []postgres.RestaurantSummary `json:"fast_delivery"`
	Offers            []Offer                      `json:"offers"`
}

type Offer struct {
	Code        string `json:"code"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (s *Service) Home(ctx context.Context, city string) (*Home, error) {
	cuisines, err := s.store.ListCuisines(ctx)
	if err != nil {
		return nil, err
	}
	restaurants, err := s.store.ListRestaurants(ctx, postgres.RestaurantFilter{City: city, Limit: 12})
	if err != nil {
		return nil, err
	}

	home := &Home{
		Cuisines:          cuisines,
		NearbyRestaurants: restaurants,
		TopRated:          take(restaurants, 6),
		FastDelivery:      take(restaurants, 6),
		Offers: []Offer{
			{
				Code:        "FIGO50",
				Title:       "FiGo launch offer",
				Description: "Get Rs 50 off on orders above Rs 199.",
			},
		},
	}
	return home, nil
}

func (s *Service) ListRestaurants(ctx context.Context, filter postgres.RestaurantFilter) ([]postgres.RestaurantSummary, error) {
	return s.store.ListRestaurants(ctx, filter)
}

func (s *Service) ListCuisines(ctx context.Context) ([]postgres.Cuisine, error) {
	return s.store.ListCuisines(ctx)
}

func (s *Service) GetRestaurant(ctx context.Context, id uuid.UUID) (*postgres.RestaurantDetail, error) {
	return s.store.GetRestaurant(ctx, id)
}

func (s *Service) GetMenu(ctx context.Context, restaurantID uuid.UUID) ([]postgres.MenuCategory, error) {
	return s.store.GetMenu(ctx, restaurantID)
}

func (s *Service) GetCart(ctx context.Context, userID uuid.UUID) (*postgres.Cart, error) {
	return s.store.GetCart(ctx, userID)
}

func (s *Service) AddCartItem(ctx context.Context, userID uuid.UUID, in postgres.AddCartItemInput) (*postgres.Cart, error) {
	return s.store.AddCartItem(ctx, userID, in)
}

func (s *Service) UpdateCartItem(ctx context.Context, userID, itemID uuid.UUID, in postgres.UpdateCartItemInput) (*postgres.Cart, error) {
	return s.store.UpdateCartItem(ctx, userID, itemID, in)
}

func (s *Service) RemoveCartItem(ctx context.Context, userID, itemID uuid.UUID) error {
	return s.store.RemoveCartItem(ctx, userID, itemID)
}

func (s *Service) ClearCart(ctx context.Context, userID uuid.UUID) error {
	return s.store.ClearCart(ctx, userID)
}

func (s *Service) ApplyCoupon(ctx context.Context, userID uuid.UUID, code string) (*postgres.Cart, error) {
	return s.store.ApplyCoupon(ctx, userID, code)
}

func (s *Service) ListAddresses(ctx context.Context, userID uuid.UUID) ([]postgres.Address, error) {
	return s.store.ListAddresses(ctx, userID)
}

func (s *Service) CreateAddress(ctx context.Context, userID uuid.UUID, in postgres.AddressInput) (*postgres.Address, error) {
	return s.store.CreateAddress(ctx, userID, in)
}

func (s *Service) UpdateAddress(ctx context.Context, userID, addressID uuid.UUID, in postgres.AddressInput) (*postgres.Address, error) {
	return s.store.UpdateAddress(ctx, userID, addressID, in)
}

func (s *Service) DeleteAddress(ctx context.Context, userID, addressID uuid.UUID) error {
	return s.store.DeleteAddress(ctx, userID, addressID)
}

func (s *Service) PlaceOrder(ctx context.Context, userID uuid.UUID, in postgres.PlaceOrderInput, idempotencyKey string) (*postgres.Order, error) {
	return s.store.PlaceOrder(ctx, userID, in, idempotencyKey)
}

func (s *Service) ListOrders(ctx context.Context, userID uuid.UUID) ([]postgres.Order, error) {
	return s.store.ListOrders(ctx, userID)
}

func (s *Service) GetOrder(ctx context.Context, userID, orderID uuid.UUID) (*postgres.Order, error) {
	return s.store.GetOrder(ctx, userID, orderID)
}

func (s *Service) GetOrderTracking(ctx context.Context, userID, orderID uuid.UUID) (map[string]any, error) {
	return s.store.GetOrderTracking(ctx, userID, orderID)
}

func (s *Service) CreatePaymentIntent(ctx context.Context, userID, orderID uuid.UUID, method, idempotencyKey string) (map[string]any, error) {
	details, err := s.store.WalletPaymentChargeDetails(ctx, userID, orderID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(method) == "" {
		method = details.PaymentMethod
	}
	intent, err := s.store.CreatePaymentIntent(ctx, userID, orderID, method, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(method, "ONLINE") {
		upstream, err := s.createPaymentsServiceIntent(ctx, details, idempotencyKey)
		if err != nil {
			return nil, err
		}
		providerPaymentID := stringFromMap(upstream, "id")
		providerOrderID := stringFromMap(upstream, "provider_ref")
		if providerOrderID == "" {
			providerOrderID = stringFromMap(upstream, "provider_order_id")
		}
		if err := s.store.AttachPaymentProviderReference(ctx, userID, orderID, providerPaymentID, providerOrderID, upstream); err != nil {
			return nil, err
		}
		intent["payment_intent"] = upstream
		intent["provider_payment_id"] = providerPaymentID
		intent["provider_order_id"] = providerOrderID
	}
	return intent, nil
}

func (s *Service) ConfirmPayment(ctx context.Context, userID, orderID uuid.UUID, providerPaymentID, providerReference string) (*postgres.Order, error) {
	details, err := s.store.WalletPaymentChargeDetails(ctx, userID, orderID)
	if err != nil {
		return nil, err
	}
	if details.PaymentMethod == "WALLET" && details.PaymentStatus != "CAPTURED" {
		if err := s.chargeWalletForFoodOrder(ctx, details); err != nil {
			return nil, err
		}
		if providerReference == "" {
			providerReference = "monetization-wallet"
		}
	}
	storeProviderPaymentID := providerPaymentID
	if details.PaymentMethod == "ONLINE" && details.PaymentStatus != "CAPTURED" {
		paymentDetails, err := s.store.PaymentIntegrationDetails(ctx, orderID)
		if err != nil {
			return nil, err
		}
		if err := s.markPaymentsServiceIntentSucceeded(ctx, paymentDetails, userID, providerReference); err != nil {
			return nil, err
		}
		storeProviderPaymentID = paymentDetails.ProviderPaymentID
	}
	return s.store.ConfirmPayment(ctx, userID, orderID, storeProviderPaymentID, providerReference)
}

func (s *Service) chargeWalletForFoodOrder(ctx context.Context, details *postgres.WalletPaymentChargeDetails) error {
	if s.monetizationURL == "" {
		return fmt.Errorf("MONETIZATION_SERVICE_URL is required for wallet payments")
	}
	amountPaise := int64(math.Round(details.Amount * 100))
	body, _ := json.Marshal(map[string]any{
		"from_user_id":   details.UserID.String(),
		"to_user_id":     details.RestaurantOwnerID.String(),
		"amount_paise":   amountPaise,
		"description":    "FiGo food order " + details.OrderNumber,
		"reference_type": "food_order",
		"reference_id":   details.OrderID.String(),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.monetizationURL, "/")+"/v1/monetization/internal/charge-and-credit", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalKey)
	}
	req.Header.Set("X-User-Id", details.UserID.String())
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("wallet charge failed with status %d", resp.StatusCode)
	}
	return nil
}

func (s *Service) markPaymentsServiceIntentSucceeded(ctx context.Context, details *postgres.PaymentIntegrationDetails, actorID uuid.UUID, providerRef string) error {
	if s.paymentsURL == "" {
		return fmt.Errorf("PAYMENTS_SERVICE_URL is required for online payments")
	}
	if details.ProviderPaymentID == "" {
		return fmt.Errorf("payments-service intent reference is missing")
	}
	body, _ := json.Marshal(map[string]string{
		"old_status":   "pending",
		"new_status":   "succeeded",
		"provider_ref": providerRef,
	})
	url := strings.TrimRight(s.paymentsURL, "/") + "/v1/payments/intents/" + details.ProviderPaymentID + "/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalKey)
	}
	req.Header.Set("X-User-Id", actorID.String())
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		return nil
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.paymentsURL, "/")+"/v1/payments/intents/"+details.ProviderPaymentID, nil)
	if err != nil {
		return err
	}
	if s.internalKey != "" {
		getReq.Header.Set("X-Internal-Service-Key", s.internalKey)
	}
	getReq.Header.Set("X-User-Id", actorID.String())
	getResp, err := s.httpClient.Do(getReq)
	if err != nil {
		return err
	}
	defer getResp.Body.Close()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if getResp.StatusCode < 400 && json.NewDecoder(getResp.Body).Decode(&envelope) == nil && strings.EqualFold(stringFromMap(envelope.Data, "status"), "succeeded") {
		return nil
	}
	return fmt.Errorf("payments status update failed with status %d", resp.StatusCode)
}

func (s *Service) createPaymentsServiceIntent(ctx context.Context, details *postgres.WalletPaymentChargeDetails, idempotencyKey string) (map[string]any, error) {
	if s.paymentsURL == "" {
		return nil, fmt.Errorf("PAYMENTS_SERVICE_URL is required for online payments")
	}
	body, _ := json.Marshal(map[string]any{
		"payee_id":        details.RestaurantOwnerID.String(),
		"reference_type":  "food_order",
		"reference_id":    details.OrderID.String(),
		"amount":          details.Amount,
		"currency":        "INR",
		"method":          "upi",
		"idempotency_key": idempotencyKey,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.paymentsURL, "/")+"/v1/payments/intents", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalKey)
	}
	req.Header.Set("X-User-Id", details.UserID.String())
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var envelope struct {
		Data  map[string]any `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		if envelope.Error != nil && envelope.Error.Message != "" {
			return nil, errors.New(envelope.Error.Message)
		}
		return nil, fmt.Errorf("payments intent failed with status %d", resp.StatusCode)
	}
	return envelope.Data, nil
}

func (s *Service) refundPaymentsServiceIntent(ctx context.Context, details *postgres.PaymentIntegrationDetails, actorID uuid.UUID, reason string) error {
	if s.paymentsURL == "" {
		return fmt.Errorf("PAYMENTS_SERVICE_URL is required for online refunds")
	}
	body, _ := json.Marshal(map[string]string{"reason": reason})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.paymentsURL, "/")+"/v1/payments/intents/"+details.ProviderPaymentID+"/refund", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalKey)
	}
	req.Header.Set("X-User-Id", actorID.String())
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("payments refund failed with status %d", resp.StatusCode)
	}
	return nil
}

func (s *Service) reverseWalletForFoodRefund(ctx context.Context, details *postgres.PaymentIntegrationDetails, amount float64) error {
	if s.monetizationURL == "" {
		return fmt.Errorf("MONETIZATION_SERVICE_URL is required for wallet refunds")
	}
	amountPaise := int64(math.Round(amount * 100))
	body, _ := json.Marshal(map[string]any{
		"from_user_id":   details.RestaurantOwnerID.String(),
		"to_user_id":     details.UserID.String(),
		"amount_paise":   amountPaise,
		"description":    "FiGo refund " + details.OrderNumber,
		"reference_type": "food_order_refund",
		"reference_id":   details.OrderID.String(),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.monetizationURL, "/")+"/v1/monetization/internal/charge-and-credit", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalKey)
	}
	req.Header.Set("X-User-Id", details.UserID.String())
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("wallet refund failed with status %d", resp.StatusCode)
	}
	return nil
}

func stringFromMap(input map[string]any, key string) string {
	if value, ok := input[key]; ok && value != nil {
		return fmt.Sprint(value)
	}
	return ""
}

func (s *Service) CancelOrder(ctx context.Context, userID, orderID uuid.UUID, reason string) (*postgres.Order, error) {
	return s.store.CancelOrder(ctx, userID, orderID, reason)
}

func (s *Service) RateRestaurant(ctx context.Context, userID, orderID uuid.UUID, rating int, review string) (map[string]any, error) {
	return s.store.RateRestaurant(ctx, userID, orderID, rating, review)
}

func (s *Service) RateDelivery(ctx context.Context, userID, orderID uuid.UUID, rating int, review string) (map[string]any, error) {
	return s.store.RateDelivery(ctx, userID, orderID, rating, review)
}

func (s *Service) CreatePartnerRestaurant(ctx context.Context, ownerID uuid.UUID, in postgres.PartnerRestaurantInput) (*postgres.PartnerRestaurant, error) {
	return s.store.CreatePartnerRestaurant(ctx, ownerID, in)
}

func (s *Service) ListPartnerRestaurants(ctx context.Context, ownerID uuid.UUID) ([]postgres.PartnerRestaurant, error) {
	return s.store.ListPartnerRestaurants(ctx, ownerID)
}

func (s *Service) GetPartnerRestaurant(ctx context.Context, ownerID, restaurantID uuid.UUID) (*postgres.PartnerRestaurant, error) {
	return s.store.GetPartnerRestaurant(ctx, ownerID, restaurantID)
}

func (s *Service) UpdatePartnerRestaurant(ctx context.Context, ownerID, restaurantID uuid.UUID, in postgres.PartnerRestaurantInput) (*postgres.PartnerRestaurant, error) {
	return s.store.UpdatePartnerRestaurant(ctx, ownerID, restaurantID, in)
}

func (s *Service) AddRestaurantDocument(ctx context.Context, ownerID, restaurantID uuid.UUID, input map[string]any) (map[string]any, error) {
	return s.store.AddRestaurantDocument(ctx, ownerID, restaurantID, input)
}

func (s *Service) AddRestaurantImage(ctx context.Context, ownerID, restaurantID uuid.UUID, input map[string]any) (map[string]any, error) {
	return s.store.AddRestaurantImage(ctx, ownerID, restaurantID, input)
}

func (s *Service) CreateMenuCategory(ctx context.Context, ownerID, restaurantID uuid.UUID, in postgres.MenuCategoryInput) (*postgres.MenuCategory, error) {
	return s.store.CreateMenuCategory(ctx, ownerID, restaurantID, in)
}

func (s *Service) ListMenuCategories(ctx context.Context, ownerID, restaurantID uuid.UUID) ([]postgres.MenuCategory, error) {
	return s.store.ListMenuCategories(ctx, ownerID, restaurantID)
}

func (s *Service) UpdateMenuCategory(ctx context.Context, ownerID, categoryID uuid.UUID, in postgres.MenuCategoryInput) (*postgres.MenuCategory, error) {
	return s.store.UpdateMenuCategory(ctx, ownerID, categoryID, in)
}

func (s *Service) DeleteMenuCategory(ctx context.Context, ownerID, categoryID uuid.UUID) error {
	return s.store.DeleteMenuCategory(ctx, ownerID, categoryID)
}

func (s *Service) CreateMenuItem(ctx context.Context, ownerID, restaurantID, categoryID uuid.UUID, in postgres.MenuItemInput) (*postgres.MenuItem, error) {
	return s.store.CreateMenuItem(ctx, ownerID, restaurantID, categoryID, in)
}

func (s *Service) UpdateMenuItem(ctx context.Context, ownerID, itemID uuid.UUID, in postgres.MenuItemInput) (*postgres.MenuItem, error) {
	return s.store.UpdateMenuItem(ctx, ownerID, itemID, in)
}

func (s *Service) DeleteMenuItem(ctx context.Context, ownerID, itemID uuid.UUID) error {
	return s.store.DeleteMenuItem(ctx, ownerID, itemID)
}

func (s *Service) SetMenuItemAvailability(ctx context.Context, ownerID, itemID uuid.UUID, available bool) error {
	return s.store.SetMenuItemAvailability(ctx, ownerID, itemID, available)
}

func (s *Service) ListPartnerOrders(ctx context.Context, ownerID, restaurantID uuid.UUID) ([]postgres.Order, error) {
	return s.store.ListPartnerOrders(ctx, ownerID, restaurantID)
}

func (s *Service) PartnerUpdateOrderStatus(ctx context.Context, ownerID, orderID uuid.UUID, toStatus, reason, idempotencyKey string) (*postgres.Order, error) {
	return s.store.PartnerUpdateOrderStatus(ctx, ownerID, orderID, toStatus, reason, idempotencyKey)
}

func (s *Service) PartnerRestaurantSettlements(ctx context.Context, ownerID, restaurantID uuid.UUID) ([]map[string]any, error) {
	return s.store.PartnerRestaurantSettlements(ctx, ownerID, restaurantID)
}

func (s *Service) PartnerRestaurantSummary(ctx context.Context, ownerID, restaurantID uuid.UUID) (map[string]any, error) {
	return s.store.PartnerRestaurantSummary(ctx, ownerID, restaurantID)
}

func (s *Service) UpsertDeliveryPartner(ctx context.Context, userID uuid.UUID, in postgres.DeliveryPartnerInput) (*postgres.DeliveryPartner, error) {
	return s.store.UpsertDeliveryPartner(ctx, userID, in)
}

func (s *Service) GetDeliveryPartner(ctx context.Context, userID uuid.UUID) (*postgres.DeliveryPartner, error) {
	return s.store.GetDeliveryPartner(ctx, userID)
}

func (s *Service) AddDeliveryDocument(ctx context.Context, userID uuid.UUID, input map[string]any) (map[string]any, error) {
	return s.store.AddDeliveryDocument(ctx, userID, input)
}

func (s *Service) SetDeliveryAvailability(ctx context.Context, userID uuid.UUID, online bool) (*postgres.DeliveryPartner, error) {
	return s.store.SetDeliveryAvailability(ctx, userID, online)
}

func (s *Service) ListDeliveryAssignments(ctx context.Context, userID uuid.UUID) ([]postgres.DeliveryAssignment, error) {
	return s.store.ListDeliveryAssignments(ctx, userID)
}

func (s *Service) GetCurrentDeliveryAssignment(ctx context.Context, userID uuid.UUID) (*postgres.DeliveryAssignment, error) {
	return s.store.GetCurrentDeliveryAssignment(ctx, userID)
}

func (s *Service) DeliveryUpdateAssignment(ctx context.Context, userID, assignmentID uuid.UUID, toStatus, idempotencyKey string) (*postgres.DeliveryAssignment, error) {
	return s.store.DeliveryUpdateAssignment(ctx, userID, assignmentID, toStatus, idempotencyKey)
}

func (s *Service) UpdateDeliveryLocation(ctx context.Context, userID uuid.UUID, latitude, longitude float64, accuracyMeters *float64) (map[string]any, error) {
	return s.store.UpdateDeliveryLocation(ctx, userID, latitude, longitude, accuracyMeters)
}

func (s *Service) GetAssignmentTracking(ctx context.Context, userID, assignmentID uuid.UUID) (map[string]any, error) {
	return s.store.GetAssignmentTracking(ctx, userID, assignmentID)
}

func (s *Service) DeliveryEarnings(ctx context.Context, userID uuid.UUID) (map[string]any, error) {
	return s.store.DeliveryEarnings(ctx, userID)
}

func (s *Service) DeliveryHistory(ctx context.Context, userID uuid.UUID) ([]postgres.DeliveryAssignment, error) {
	return s.store.DeliveryHistory(ctx, userID)
}

func (s *Service) AdminDashboard(ctx context.Context) (*postgres.AdminDashboard, error) {
	return s.store.AdminDashboard(ctx)
}

func (s *Service) AdminPendingRestaurants(ctx context.Context) ([]postgres.PartnerRestaurant, error) {
	return s.store.AdminPendingRestaurants(ctx)
}

func (s *Service) AdminApproveRestaurant(ctx context.Context, adminID, restaurantID uuid.UUID, approve bool, reason string) error {
	return s.store.AdminApproveRestaurant(ctx, adminID, restaurantID, approve, reason)
}

func (s *Service) AdminSetRestaurantStatus(ctx context.Context, adminID, restaurantID uuid.UUID, status, reason string) error {
	return s.store.AdminSetRestaurantStatus(ctx, adminID, restaurantID, status, reason)
}

func (s *Service) AdminPendingDeliveryPartners(ctx context.Context) ([]postgres.DeliveryPartner, error) {
	return s.store.AdminPendingDeliveryPartners(ctx)
}

func (s *Service) AdminApproveDeliveryPartner(ctx context.Context, adminID, partnerID uuid.UUID, approve bool, reason string) error {
	return s.store.AdminApproveDeliveryPartner(ctx, adminID, partnerID, approve, reason)
}

func (s *Service) AdminSetDeliveryPartnerStatus(ctx context.Context, adminID, partnerID uuid.UUID, status, reason string) error {
	return s.store.AdminSetDeliveryPartnerStatus(ctx, adminID, partnerID, status, reason)
}

func (s *Service) AdminListOrders(ctx context.Context, page postgres.Pagination) ([]postgres.Order, error) {
	return s.store.AdminListOrders(ctx, page)
}

func (s *Service) AdminGetOrder(ctx context.Context, orderID uuid.UUID) (*postgres.Order, error) {
	return s.store.AdminGetOrder(ctx, orderID)
}

func (s *Service) AdminCancelOrder(ctx context.Context, adminID, orderID uuid.UUID, reason string) (*postgres.Order, error) {
	return s.store.AdminCancelOrder(ctx, adminID, orderID, reason)
}

func (s *Service) AdminRefundOrder(ctx context.Context, adminID, orderID uuid.UUID, reason string, amount float64, idempotencyKey string) (map[string]any, error) {
	details, err := s.store.PaymentIntegrationDetails(ctx, orderID)
	if err != nil {
		return nil, err
	}
	refundAmount := amount
	if refundAmount <= 0 || refundAmount > details.Amount {
		refundAmount = details.Amount
	}
	if details.PaymentStatus != "REFUNDED" {
		switch details.PaymentMethod {
		case "ONLINE":
			if details.ProviderPaymentID == "" {
				return nil, fmt.Errorf("online payment provider reference missing")
			}
			if err := s.refundPaymentsServiceIntent(ctx, details, adminID, reason); err != nil {
				return nil, err
			}
		case "WALLET":
			if err := s.reverseWalletForFoodRefund(ctx, details, refundAmount); err != nil {
				return nil, err
			}
		}
	}
	return s.store.AdminRefundOrder(ctx, adminID, orderID, reason, amount, idempotencyKey)
}

func (s *Service) AdminListCoupons(ctx context.Context) ([]map[string]any, error) {
	return s.store.AdminListCoupons(ctx)
}

func (s *Service) AdminCreateCoupon(ctx context.Context, adminID uuid.UUID, input map[string]any) (map[string]any, error) {
	return s.store.AdminCreateCoupon(ctx, adminID, input)
}

func (s *Service) AdminUpdateCoupon(ctx context.Context, adminID, couponID uuid.UUID, input map[string]any) (map[string]any, error) {
	return s.store.AdminUpdateCoupon(ctx, adminID, couponID, input)
}

func (s *Service) AdminListServiceAreas(ctx context.Context) ([]map[string]any, error) {
	return s.store.AdminListServiceAreas(ctx)
}

func (s *Service) AdminCreateServiceArea(ctx context.Context, adminID uuid.UUID, input map[string]any) (map[string]any, error) {
	return s.store.AdminCreateServiceArea(ctx, adminID, input)
}

func (s *Service) AdminUpdateServiceArea(ctx context.Context, adminID, areaID uuid.UUID, input map[string]any) (map[string]any, error) {
	return s.store.AdminUpdateServiceArea(ctx, adminID, areaID, input)
}

func (s *Service) AdminListRestaurantSettlements(ctx context.Context, page postgres.Pagination) ([]map[string]any, error) {
	return s.store.AdminListRestaurantSettlements(ctx, page)
}

func (s *Service) AdminMarkRestaurantSettlementPaid(ctx context.Context, adminID, settlementID uuid.UUID, reference string) (map[string]any, error) {
	return s.store.AdminMarkRestaurantSettlementPaid(ctx, adminID, settlementID, reference)
}

func (s *Service) AdminListDeliverySettlements(ctx context.Context, page postgres.Pagination) ([]map[string]any, error) {
	return s.store.AdminListDeliverySettlements(ctx, page)
}

func (s *Service) AdminMarkDeliverySettlementPaid(ctx context.Context, adminID, settlementID uuid.UUID, reference string) (map[string]any, error) {
	return s.store.AdminMarkDeliverySettlementPaid(ctx, adminID, settlementID, reference)
}

func (s *Service) AdminGenerateSettlements(ctx context.Context, adminID uuid.UUID, in postgres.SettlementGenerateInput) (map[string]any, error) {
	return s.store.AdminGenerateSettlements(ctx, adminID, in)
}

func (s *Service) AdminAuditLogs(ctx context.Context, page postgres.Pagination) ([]map[string]any, error) {
	return s.store.AdminAuditLogs(ctx, page)
}

func (s *Service) AdminOrderReport(ctx context.Context) (map[string]any, error) {
	return s.store.AdminOrderReport(ctx)
}

func (s *Service) AdminRevenueReport(ctx context.Context) (map[string]any, error) {
	return s.store.AdminRevenueReport(ctx)
}

func take[T any](items []T, limit int) []T {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}
