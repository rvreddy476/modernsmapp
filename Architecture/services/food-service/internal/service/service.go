package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/outbox"
	"github.com/atpost/shared/realtime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
	ListKitchenQueue(ctx context.Context, ownerID, restaurantID uuid.UUID) ([]postgres.KitchenOrder, error)
	AutoRejectExpiredOrders(ctx context.Context, batch int) ([]uuid.UUID, error)
	ProposeSubstitution(ctx context.Context, ownerID uuid.UUID, in postgres.ProposeSubstitutionInput) (*postgres.Substitution, error)
	RespondToSubstitution(ctx context.Context, customerID, subID uuid.UUID, newStatus string) (*postgres.Substitution, error)
	ListSubstitutions(ctx context.Context, userID, orderID uuid.UUID) ([]postgres.Substitution, error)
	ReportMenuItem(ctx context.Context, reporterID, itemID uuid.UUID, category, detail string) (*postgres.MenuItemReport, error)
	ListPendingModeration(ctx context.Context, limit int) ([]postgres.PendingModerationItem, error)
	ModerateMenuItem(ctx context.Context, adminID, itemID uuid.UUID, status, reason string) error
	ListUnassignedReadyOrders(ctx context.Context, batch int) ([]uuid.UUID, error)
	ListEligibleDeliveryPartners(ctx context.Context, restaurantCity string, limit int) ([]uuid.UUID, error)
	CreateDeliveryOffer(ctx context.Context, orderID, partnerID uuid.UUID, expiresAt time.Time) (*postgres.DeliveryOffer, error)
	ListMyPendingDeliveryOffers(ctx context.Context, userID uuid.UUID) ([]postgres.DeliveryOffer, error)
	AcceptDeliveryOfferTx(ctx context.Context, userID, offerID uuid.UUID) (*postgres.DeliveryOffer, error)
	RejectDeliveryOffer(ctx context.Context, userID, offerID uuid.UUID, reason string) error
	ExpireDeliveryOffers(ctx context.Context) (int, error)
	EnsureDeliveryCodes(ctx context.Context, orderID uuid.UUID) (string, string, error)
	VerifyPickupCode(ctx context.Context, ownerID, orderID uuid.UUID, code string) error
	VerifyDeliveryCode(ctx context.Context, customerID, orderID uuid.UUID, code string) error
	AttachProofURL(ctx context.Context, userID, orderID uuid.UUID, which, url string) error
	CreateTicket(ctx context.Context, in postgres.CreateTicketInput) (*postgres.Ticket, error)
	ListMyTickets(ctx context.Context, customerID uuid.UUID) ([]postgres.Ticket, error)
	AppendTicketMessage(ctx context.Context, ticketID, authorID uuid.UUID, isAdmin bool, body string) (*postgres.TicketMessage, error)
	GetTicketWithMessages(ctx context.Context, ticketID uuid.UUID) (*postgres.Ticket, []postgres.TicketMessage, error)
	SetTicketStatus(ctx context.Context, ticketID uuid.UUID, status string) error
	ListTicketsForAdmin(ctx context.Context, status string, limit int) ([]postgres.Ticket, error)
	ListRefundsForAdmin(ctx context.Context, status string, limit int) ([]postgres.RefundRequest, error)
	CreateRefundRequest(ctx context.Context, customerID, orderID uuid.UUID, ticketID *uuid.UUID, amount float64, reason string) (*postgres.RefundRequest, error)
	DecideRefund(ctx context.Context, adminID, refundID uuid.UUID, status, reason string) error
	CreateItemReview(ctx context.Context, in postgres.CreateItemReviewInput) (*postgres.ItemReview, error)
	ListItemReviews(ctx context.Context, menuItemID uuid.UUID, limit int) ([]postgres.ItemReview, error)
	HideItemReview(ctx context.Context, reviewID uuid.UUID) error
	ReportRestaurantSLA(ctx context.Context, w postgres.ReportWindow) ([]postgres.RestaurantSLAReport, error)
	ReportDeliverySLA(ctx context.Context, w postgres.ReportWindow) ([]postgres.DeliverySLAReport, error)
	ReportPaymentRecon(ctx context.Context, w postgres.ReportWindow) ([]postgres.PaymentReconRow, error)
	ReportRefundsCancellations(ctx context.Context, w postgres.ReportWindow) ([]postgres.RefundCancelRow, error)
	ReportCouponAbuse(ctx context.Context, w postgres.ReportWindow, threshold int) ([]postgres.CouponAbuseRow, error)
	ReportCompliance(ctx context.Context) ([]postgres.ComplianceReportRow, error)
	RecordFraudScore(ctx context.Context, userID uuid.UUID, signal string, score float64, detail map[string]any) error
	TopFraudUsers(ctx context.Context, windowHours, limit int) ([]postgres.TopFraudUsersRow, error)
	RecentRefundsByUser(ctx context.Context, windowHours int) ([]postgres.RecentRefundsByUserRow, error)
	RecentCustomerCancellations(ctx context.Context, windowHours int) ([]postgres.CustomerCancellationsRow, error)
	AppendOrderMessage(ctx context.Context, orderID, authorID uuid.UUID, authorRole, body string) (*postgres.OrderMessage, error)
	ListOrderMessages(ctx context.Context, orderID uuid.UUID) ([]postgres.OrderMessage, error)
	MarkMessageRead(ctx context.Context, messageID, userID uuid.UUID, role string) error
	OrderPartyMembership(ctx context.Context, orderID, userID uuid.UUID) (*postgres.OrderPartyMembership, error)
	GenerateRestaurantSettlementFile(ctx context.Context, adminID uuid.UUID, from, to time.Time) (*postgres.SettlementFile, error)
	GenerateDeliverySettlementFile(ctx context.Context, adminID uuid.UUID, from, to time.Time) (*postgres.SettlementFile, error)
	ListSettlementFiles(ctx context.Context, limit int) ([]postgres.SettlementFile, error)
	GetSettlementFileBody(ctx context.Context, fileID uuid.UUID) ([]byte, string, error)
	GetInvoiceData(ctx context.Context, userID, orderID uuid.UUID) (*postgres.InvoiceData, error)
	AllocateInvoiceNumber(ctx context.Context, orderID uuid.UUID, financialYear string) (string, error)
	PredictPrepTime(ctx context.Context, restaurantID uuid.UUID) (*postgres.PrepTimePrediction, error)
	GetLoyaltyBalance(ctx context.Context, userID uuid.UUID) (*postgres.LoyaltyBalance, error)
	EarnPoints(ctx context.Context, userID, orderID uuid.UUID, delta int, reason string) (*postgres.LoyaltyBalance, error)
	RedeemPoints(ctx context.Context, userID uuid.UUID, orderID *uuid.UUID, delta int, reason string) (*postgres.LoyaltyBalance, error)
	ListLoyaltyLedger(ctx context.Context, userID uuid.UUID, limit int) ([]postgres.LoyaltyLedgerRow, error)
	EnsureReferralCode(ctx context.Context, userID uuid.UUID) (string, error)
	RecordReferral(ctx context.Context, refereeID uuid.UUID, code string) (*postgres.Referral, error)
	MarkReferralRewarded(ctx context.Context, refereeID uuid.UUID, rewardPoints int) error
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
	rtPublisher     *realtime.Publisher
	rtSigner        *realtime.TokenSigner
	outboxQ         *outbox.Queuer
	dbPool          *pgxpool.Pool
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

// WithRealtime wires the realtime publisher + topic-token signer.
// Both are optional; nil-checked at every callsite so misconfiguration
// degrades to "no live push, polling still works."
func (s *Service) WithRealtime(p *realtime.Publisher, signer *realtime.TokenSigner) *Service {
	s.rtPublisher = p
	s.rtSigner = signer
	return s
}

// WithOutbox wires the durable outbox so domain events flow to Kafka
// and downstream consumers (notification-service for FCM, analytics,
// admin live boards). The pool is the same one the store uses; we
// keep a separate handle so the service layer can enqueue without
// going through every store method.
func (s *Service) WithOutbox(q *outbox.Queuer, db *pgxpool.Pool) *Service {
	s.outboxQ = q
	s.dbPool = db
	return s
}

// emit publishes a Kafka event via the outbox AND a Pub/Sub realtime
// frame in one call. Both are best-effort; failures are logged at
// WARN so a Redis or Postgres hiccup does not break the user request.
func (s *Service) emit(ctx context.Context, topic, eventType string, data any) {
	s.publishRealtime(ctx, topic, eventType, data)
	if s.outboxQ == nil || s.dbPool == nil {
		return
	}
	body, err := json.Marshal(data)
	if err != nil {
		slog.Warn("food-service: outbox marshal failed", "event", eventType, "error", err)
		return
	}
	// Partition by topic so consumers can use it as the Kafka key for
	// order-preserving fan-out (e.g. one partition per order_id).
	if err := s.outboxQ.EnqueuePool(ctx, s.dbPool, eventType, topic, body); err != nil {
		slog.Warn("food-service: outbox enqueue failed", "event", eventType, "error", err)
	}
}

// publishRealtime is a best-effort fire-and-forget broadcast. Errors
// are logged at WARN — the durable copy is the Kafka event.
func (s *Service) publishRealtime(ctx context.Context, topic, eventType string, data any) {
	if s.rtPublisher == nil {
		return
	}
	if err := s.rtPublisher.Publish(ctx, topic, eventType, data); err != nil {
		slog.Warn("food-service: realtime publish failed", "topic", topic, "event", eventType, "error", err)
	}
}

// IssueRealtimeToken builds a topic-scoped token granting the user
// access to the order/restaurant/partner topics they own. Caller is
// responsible for X-User-Id auth.
func (s *Service) IssueRealtimeToken(ctx context.Context, userID uuid.UUID) (string, []string, error) {
	if s.rtSigner == nil {
		return "", nil, errors.New("realtime: signer not configured")
	}
	// Topic set:
	//   1. food.order.{order_id}       — for every order the user placed.
	//   2. food.restaurant.{id}.orders — for every restaurant the user owns.
	//   3. food.delivery_partner.{id}.assignments — if they're a partner.
	topics := []string{}
	if orders, err := s.store.ListOrders(ctx, userID); err == nil {
		for _, o := range orders {
			topics = append(topics, "food.order."+o.ID.String())
		}
	}
	if rests, err := s.store.ListPartnerRestaurants(ctx, userID); err == nil {
		for _, r := range rests {
			topics = append(topics, "food.restaurant."+r.ID.String()+".orders")
		}
	}
	// Always grant the delivery-partner self-assignment topic — the
	// user is keyed by their own user_id, so the topic is self-scoped.
	topics = append(topics, "food.delivery_partner."+userID.String()+".assignments")
	if len(topics) == 0 {
		// Token signer rejects empty topic list; give the caller a
		// no-op self-topic so they at least get a connected event.
		topics = []string{"food.user." + userID.String()}
	}
	tok, err := s.rtSigner.Sign(userID.String(), topics)
	if err != nil {
		return "", nil, err
	}
	return tok, topics, nil
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
	o, err := s.store.PlaceOrder(ctx, userID, in, idempotencyKey)
	if err != nil {
		return nil, err
	}
	s.emit(ctx, "food.order."+o.ID.String(), "food.order.placed", o)
	s.publishRealtime(ctx, "food.restaurant."+o.RestaurantID.String()+".orders", "food.order.placed", o)
	s.publishRealtime(ctx, "food.admin.live_orders", "food.order.placed", o)
	return o, nil
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

// ConfirmPaymentInput is the FiGo confirm-payment request body in
// canonical Go form. Online payments require the Razorpay signature
// triple — the backend forwards it to payments-service for HMAC
// verification before any "paid" state is persisted. Wallet payments
// pass through the existing internal monetization charge path; the
// signature fields are ignored for that method.
//
// Idempotency: an already-CAPTURED order short-circuits to a clean
// return (the order row is reloaded so the response is the canonical
// post-confirm state). IdempotencyKey is reserved for cross-request
// dedup on the same {user, order} tuple.
type ConfirmPaymentInput struct {
	UserID            uuid.UUID
	OrderID           uuid.UUID
	ProviderPaymentID string
	ProviderReference string
	RazorpayOrderID   string
	RazorpayPaymentID string
	RazorpaySignature string
	AmountMinor       int64
	IdempotencyKey    string
}

// ConfirmPayment is the FiGo customer-facing confirm path. P0.1 fix:
//
//  1. Reject ONLINE confirms missing the Razorpay signature triple.
//  2. Call payments-service /v1/payments/intents/:id/verify (which
//     re-checks HMAC + amount against the stored intent). The
//     previous direct status-PATCH path is gone — the client can no
//     longer forge a paid state by sending arbitrary provider ids.
//  3. Idempotent: already-CAPTURED orders short-circuit cleanly.
//  4. Cancelled / refunded orders cannot be revived because
//     payments-service refuses to verify against an intent that
//     isn't pending. Late webhook arrival is the canonical
//     reconciliation path.
func (s *Service) ConfirmPayment(ctx context.Context, in ConfirmPaymentInput) (*postgres.Order, error) {
	details, err := s.store.WalletPaymentChargeDetails(ctx, in.UserID, in.OrderID)
	if err != nil {
		return nil, err
	}
	// Idempotent short-circuit: a duplicate confirm on an already-
	// CAPTURED order is the most common race (mobile + webhook both
	// fire). Re-running the wallet charge or signature verify here
	// would double-charge or 400. Reload the order row so the
	// response is canonical post-confirm state — no further work.
	if details.PaymentStatus == "CAPTURED" {
		return s.store.GetOrder(ctx, in.UserID, in.OrderID)
	}
	providerReference := in.ProviderReference
	if details.PaymentMethod == "WALLET" {
		if err := s.chargeWalletForFoodOrder(ctx, details); err != nil {
			return nil, err
		}
		if providerReference == "" {
			providerReference = "monetization-wallet"
		}
	}
	storeProviderPaymentID := in.ProviderPaymentID
	if details.PaymentMethod == "ONLINE" {
		if in.RazorpayOrderID == "" || in.RazorpayPaymentID == "" || in.RazorpaySignature == "" {
			return nil, fmt.Errorf("razorpay signature triple is required for online payments")
		}
		paymentDetails, err := s.store.PaymentIntegrationDetails(ctx, in.OrderID)
		if err != nil {
			return nil, err
		}
		if err := s.verifyPaymentsServiceIntent(ctx, paymentDetails, in); err != nil {
			return nil, err
		}
		storeProviderPaymentID = in.RazorpayPaymentID
		providerReference = in.RazorpayOrderID
	}
	o, err := s.store.ConfirmPayment(ctx, in.UserID, in.OrderID, storeProviderPaymentID, providerReference)
	if err != nil {
		return nil, err
	}
	s.emit(ctx, "food.order."+o.ID.String(), "food.order.payment_succeeded", o)
	s.publishRealtime(ctx, "food.restaurant."+o.RestaurantID.String()+".orders", "food.order.payment_succeeded", o)
	s.publishRealtime(ctx, "food.admin.live_orders", "food.order.payment_succeeded", o)
	return o, nil
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

// verifyPaymentsServiceIntent is the new (P0.1) verification path.
// Calls payments-service `/v1/payments/intents/:id/verify` which
// re-checks the Razorpay HMAC signature against the stored intent +
// validates the amount (when provided). Returns nil only on a 200
// with `verified: true` — anything else fails the confirm so the
// order never moves to paid based on client-supplied data alone.
//
// The legacy direct status-PATCH path is intentionally removed —
// the old code would call PATCH /status with the client's
// provider_ref and trust the payments-service to mark succeeded
// without verifying the signature.
func (s *Service) verifyPaymentsServiceIntent(ctx context.Context, details *postgres.PaymentIntegrationDetails, in ConfirmPaymentInput) error {
	if s.paymentsURL == "" {
		return fmt.Errorf("PAYMENTS_SERVICE_URL is required for online payments")
	}
	if details.ProviderPaymentID == "" {
		return fmt.Errorf("payments-service intent reference is missing")
	}
	body, _ := json.Marshal(map[string]any{
		"razorpay_order_id":   in.RazorpayOrderID,
		"razorpay_payment_id": in.RazorpayPaymentID,
		"razorpay_signature":  in.RazorpaySignature,
		"amount_minor":        in.AmountMinor,
	})
	url := strings.TrimRight(s.paymentsURL, "/") + "/v1/payments/intents/" + details.ProviderPaymentID + "/verify"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalKey)
	}
	req.Header.Set("X-User-Id", in.UserID.String())
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("payments verify rejected confirm (status %d)", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			Verified    bool   `json:"verified"`
			Status      string `json:"status"`
			AmountMinor int64  `json:"amount_minor"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("payments verify response decode: %w", err)
	}
	if !envelope.Data.Verified {
		return fmt.Errorf("payments verify returned not verified (status=%s)", envelope.Data.Status)
	}
	return nil
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
	o, err := s.store.CancelOrder(ctx, userID, orderID, reason)
	if err != nil {
		return nil, err
	}
	s.emit(ctx, "food.order."+o.ID.String(), "food.order.cancelled", o)
	s.publishRealtime(ctx, "food.restaurant."+o.RestaurantID.String()+".orders", "food.order.cancelled", o)
	s.publishRealtime(ctx, "food.admin.live_orders", "food.order.cancelled", o)
	return o, nil
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

// ListKitchenQueue returns CONFIRMED orders awaiting partner accept,
// sorted by SLA-breach deadline (most-urgent first). Used by the
// partner mobile/web kitchen dashboard.
func (s *Service) ListKitchenQueue(ctx context.Context, ownerID, restaurantID uuid.UUID) ([]postgres.KitchenOrder, error) {
	return s.store.ListKitchenQueue(ctx, ownerID, restaurantID)
}

// ProposeSubstitution lets a partner offer a swap when an item is out
// of stock. Emits a realtime event on the order topic so the customer
// app pops a "choose: accept / decline" sheet.
func (s *Service) ProposeSubstitution(ctx context.Context, ownerID uuid.UUID, in postgres.ProposeSubstitutionInput) (*postgres.Substitution, error) {
	sub, err := s.store.ProposeSubstitution(ctx, ownerID, in)
	if err != nil {
		return nil, err
	}
	s.emit(ctx, "food.order."+sub.OrderID.String(), "food.order.substitution_proposed", sub)
	return sub, nil
}

// RespondToSubstitution is the customer-facing approve/decline. After
// a response we emit a follow-up event so the partner kitchen UI
// updates immediately.
func (s *Service) RespondToSubstitution(ctx context.Context, customerID, subID uuid.UUID, response string) (*postgres.Substitution, error) {
	sub, err := s.store.RespondToSubstitution(ctx, customerID, subID, response)
	if err != nil {
		return nil, err
	}
	eventType := "food.order.substitution_" + response // approved / declined / cancelled
	s.emit(ctx, "food.order."+sub.OrderID.String(), eventType, sub)
	return sub, nil
}

// ListSubstitutions returns every substitution for an order; visible
// to either the customer or the owning partner.
func (s *Service) ListSubstitutions(ctx context.Context, userID, orderID uuid.UUID) ([]postgres.Substitution, error) {
	return s.store.ListSubstitutions(ctx, userID, orderID)
}

// ReportMenuItem records a customer complaint against a menu item.
// Once enough unresolved reports accumulate the item auto-flips to
// `flagged` so admin reviews it. Idempotent at the store layer.
func (s *Service) ReportMenuItem(ctx context.Context, reporterID, itemID uuid.UUID, category, detail string) (*postgres.MenuItemReport, error) {
	return s.store.ReportMenuItem(ctx, reporterID, itemID, category, detail)
}

// ListPendingModeration returns flagged / pending_review menu items
// for the admin moderation queue. Admin-only.
func (s *Service) ListPendingModeration(ctx context.Context, limit int) ([]postgres.PendingModerationItem, error) {
	return s.store.ListPendingModeration(ctx, limit)
}

// ModerateMenuItem sets the admin verdict on a flagged item. Approving
// resolves all open reports against the item; rejecting hides it from
// customer-facing listings.
func (s *Service) ModerateMenuItem(ctx context.Context, adminID, itemID uuid.UUID, status, reason string) error {
	return s.store.ModerateMenuItem(ctx, adminID, itemID, status, reason)
}

// CreateItemReview wraps the store call; only DELIVERED orders by the
// customer are accepted (enforced in the store layer).
func (s *Service) CreateItemReview(ctx context.Context, in postgres.CreateItemReviewInput) (*postgres.ItemReview, error) {
	return s.store.CreateItemReview(ctx, in)
}

// ListItemReviews returns reviews for one menu item; public read.
func (s *Service) ListItemReviews(ctx context.Context, menuItemID uuid.UUID, limit int) ([]postgres.ItemReview, error) {
	return s.store.ListItemReviews(ctx, menuItemID, limit)
}

// HideItemReview is the admin moderation knob (hard-delete v1).
func (s *Service) HideItemReview(ctx context.Context, reviewID uuid.UUID) error {
	return s.store.HideItemReview(ctx, reviewID)
}

// AutoRejectSLAExpiredOrders is invoked by the background worker every
// 15s. Transitions CONFIRMED orders past their accept_deadline_at to
// RESTAURANT_REJECTED and fires events so notifications + refunds run
// downstream.
func (s *Service) AutoRejectSLAExpiredOrders(ctx context.Context) (int, error) {
	ids, err := s.store.AutoRejectExpiredOrders(ctx, 50)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		// Push a minimal payload — downstream consumers re-fetch the
		// order from food-service for the full state.
		s.emit(ctx, "food.order."+id.String(), "food.order.restaurant_rejected", map[string]any{
			"id":     id.String(),
			"reason": "sla_breach",
		})
	}
	return len(ids), nil
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
