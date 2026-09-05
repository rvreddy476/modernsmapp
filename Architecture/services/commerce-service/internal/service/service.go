// Package service implements the core business logic for commerce-service.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/identity"
	"github.com/atpost/commerce-service/internal/kyc"
	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/counters"
	"github.com/atpost/shared/events"
	tracepkg "github.com/atpost/shared/o11y/trace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	kafka "github.com/segmentio/kafka-go"
)

// Phase 0.1 — surfaceable errors so the HTTP handler can map them to
// specific status codes rather than the generic 500 the old code emitted.
var (
	ErrOrderNotFound          = fmt.Errorf("order not found")
	ErrNotOrderOwner          = fmt.Errorf("not authorized for this order")
	ErrOrderNotPaymentPending = fmt.Errorf("order is not awaiting payment")
	// ErrOrderNotPayable is returned when a verified payment arrives for an
	// order the transition guard refuses (cancelled, refunded, …). Distinct
	// from ErrOrderNotPaymentPending so the consumer can treat it as a
	// permanent, alert-worthy condition rather than retrying.
	ErrOrderNotPayable       = fmt.Errorf("order cannot accept payment in its current state")
	ErrPaymentVerifyFailed   = fmt.Errorf("payment verification failed")
	ErrPaymentAmountMismatch = fmt.Errorf("payment amount does not match order")
	ErrStubGatewayInProd     = fmt.Errorf("stub gateway not permitted; set PAYMENTS_ALLOW_STUB=true for dev/test")
	ErrPaymentsClientMissing = fmt.Errorf("payments client not configured")
	// ErrCourierUnavailable means the carrier could not be asked — network
	// failure, or credentials it rejected. The buyer did nothing wrong and
	// retrying may work, so it is a 503, not a 500. Distinct from a
	// serviceable=false answer, which is a real answer about a real pincode.
	ErrCourierUnavailable     = fmt.Errorf("commerce: the delivery partner could not be reached")
	ErrNotReturnSeller        = fmt.Errorf("actor is not the seller for this return")
	ErrReviewOrderItemInvalid = fmt.Errorf("order item does not belong to reviewer or does not match product/seller")
	ErrReviewItemNotDelivered = fmt.Errorf("order item must be delivered before review")
	ErrReturnNotFound         = fmt.Errorf("return not found")
	ErrNotReturnParty         = fmt.Errorf("actor is not the customer or seller for this return")
	ErrReviewNotFound         = fmt.Errorf("review not found")
	ErrNotProductOwner        = fmt.Errorf("actor is not the seller for this product")
	ErrNotReviewSeller        = fmt.Errorf("actor is not the seller for this review")
	// ErrNoSellerProfile means the caller has no seller row. Handlers map it
	// to 403 NO_SELLER, the same shape every other seller surface uses.
	ErrNoSellerProfile = fmt.Errorf("caller has no seller profile")
	// ErrInvalidStockReason means the reason code is outside the seller-facing
	// allow-list. Handlers map it to 400 rather than letting a CHECK
	// violation surface as a 500.
	ErrInvalidStockReason = fmt.Errorf("unsupported stock adjustment reason")
	// ErrApplicationIncomplete means a seller pressed Submit with something
	// a reviewer needs still missing. The message names everything that is
	// absent, not the first thing.
	ErrApplicationIncomplete = fmt.Errorf("the seller application is incomplete")
)

// ConfirmPaymentInput is the request body the customer-facing confirm
// endpoint accepts. The signature triple is what Razorpay returns on
// successful checkout; commerce-service does not trust any of these
// fields — they are forwarded to payments-service for HMAC verification
// against the stored intent.
type ConfirmPaymentInput struct {
	PaymentIntentID   uuid.UUID
	RazorpayOrderID   string
	RazorpayPaymentID string
	RazorpaySignature string
	AmountMinor       int64
	Gateway           string
}

// Service is the main commerce service.
type Service struct {
	store *postgres.Store
	// orders is the money-path view of store (see payment_ports.go). Same
	// object in production; a fake in the payment unit tests.
	orders   orderPaymentStore
	rdb      *redis.Client
	writer   *kafka.Writer
	courier  courier.Provider
	blob     BlobStore
	identity *identity.Client
	// payments is the P0 client behind an interface (payment_ports.go) so
	// the money path is unit-testable; production wires *payments.Client.
	payments paymentsClient
	// allowStubGateway — see WithAllowStubGateway.
	allowStubGateway bool
	pii              *pii.Cipher
	// piiCutover selects dual-write or ciphertext-only behaviour (B4/B5).
	// The zero value is ModeDual, which is the safe default against an
	// unmigrated database.
	piiCutover pii.Mode
	kyc        kyc.Validator
	// media verifies that a media id a client supplies actually belongs to
	// that client. Nil-safe: nil means unconfigured, which cmd/server only
	// permits in a local environment and says so loudly.
	media     *media.Client
	payoutCfg PayoutConfig

	// productViewCounter shards products.view_count across Redis so a
	// trending product taking 100k+ views/hour doesn't bottleneck on a
	// single PG UPDATE row. Nil-safe — falls back to the legacy
	// IncrProductViewCount when Redis is absent (dev loop).
	productViewCounter *counters.Counter

	// ─── cross-service plumbing for affiliate redirects ────────────
	// commerce-service hosts the public /v1/commerce/affiliate/:linkId
	// redirect, which needs the link's metadata. Reaches monetization
	// via internal-key. Fields are nil-safe — set via the With…
	// setters from cmd/server/main.go.
	monetizationServiceURL string
	internalServiceKey     string
	httpClient             *http.Client
}

// WithMonetizationServiceURL sets the base URL for monetization-service
// (used by the affiliate-redirect resolver). Empty = redirect endpoint
// returns 503 since it can't reach the link metadata.
func (s *Service) WithMonetizationServiceURL(u string) *Service {
	s.monetizationServiceURL = u
	return s
}

// WithInternalServiceKey configures the X-Internal-Service-Key header
// that commerce-service sends on outbound calls to monetization. The
// inbound gate (commerce-side) is configured on the handler.
func (s *Service) WithInternalServiceKey(k string) *Service {
	s.internalServiceKey = k
	return s
}

// WithHTTPClient lets callers inject a circuit-breaker-wrapped client
// (Architecture/shared/httpclient.NewWithBreaker). Tests inject a
// 2-second-timeout client. Defaults to a plain 5-second client.
func (s *Service) WithHTTPClient(c *http.Client) *Service {
	s.httpClient = c
	return s
}

func New(store *postgres.Store, rdb *redis.Client, kafkaBrokers string) *Service {
	return NewWithDialer(store, rdb, kafkaBrokers, nil)
}

func NewWithDialer(store *postgres.Store, rdb *redis.Client, kafkaBrokers string, dialer *kafka.Dialer) *Service {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  strings.Split(kafkaBrokers, ","),
		Topic:    "social.events.v1",
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	svc := &Service{store: store, rdb: rdb, writer: w}
	if store != nil {
		svc.orders = store
	}
	if rdb != nil {
		svc.productViewCounter = counters.New(rdb, counters.Config{EntityKind: "product_view_count", Shards: 32})
	}
	return svc
}

// ProductViewCounter exposes the sharded counter so cmd/server can
// attach a flush worker.
func (s *Service) ProductViewCounter() *counters.Counter { return s.productViewCounter }

// adjustProductViewCount routes a +1 view increment through the
// sharded counter. Falls back to a direct PG UPDATE when Redis is nil.
func (s *Service) adjustProductViewCount(ctx context.Context, productID uuid.UUID) {
	if s.productViewCounter != nil {
		if err := s.productViewCounter.Inc(ctx, productID.String(), 1); err == nil {
			return
		}
	}
	s.store.IncrProductViewCount(ctx, productID)
}

func (s *Service) Close() {
	if s.writer != nil {
		_ = s.writer.Close()
	}
}

// publish emits a Kafka event. Failures are logged but not fatal (best-effort).
//
// HP1 — request goroutine no longer blocks on kafka.Writer.WriteMessages.
// Events land in outbox_events inside an existing pool connection; the
// shared/outbox.Publisher polls and fans out to Kafka with retries +
// at-least-once semantics. The legacy synchronous path (s.writer) is
// kept as a fallback only when the Postgres-backed outbox is unavailable
// (e.g. unit tests that wire a *Service without a Store-with-outbox).
//
// Phase F3.3 — W3C trace context is no longer injected on the request
// goroutine; the publisher injects fresh headers at fan-out time (one
// trace per event publish rather than per request, but every consumer
// already roots its own span). Keeping the event_type header is
// redundant with what the publisher writes; we now stamp it server-side
// in the outbox row via the partition_key so consumers continue to
// route without re-parsing the envelope.
func (s *Service) publish(ctx context.Context, eventType string, payload any) {
	s.publishWithIdempotency(ctx, eventType, "", payload)
}

// publishWithIdempotency is the dedup-aware variant. When idempotencyKey
// is non-empty a UNIQUE partial index in outbox_events blocks a second
// insert with the same key (HP5). Use this on retry-prone code paths
// where the natural key (order id, payment id, …) makes "fired twice"
// observable. Pass an empty key for emits that are already idempotent
// upstream (e.g. webhooks that ack the same shipment_id).
func (s *Service) publishWithIdempotency(ctx context.Context, eventType, idempotencyKey string, payload any) {
	data, _ := json.Marshal(payload)
	env := events.EventEnvelope{
		EventID:    uuid.New().String(),
		EventType:  eventType,
		Payload:    data,
		OccurredAt: time.Now(),
	}
	b, _ := json.Marshal(env)
	// Partition key — empty-by-default keeps the existing balancer
	// (LeastBytes) behaviour. Topic is implicit (publisher's
	// DefaultTopic).
	if s.store != nil {
		if err := s.store.EnqueueOutboxEventPool(ctx, eventType, eventType, idempotencyKey, b); err == nil {
			return
		} else {
			slog.Warn("outbox enqueue failed; falling back to direct kafka write",
				"event", eventType, "error", err)
		}
	}
	// Fallback — used in unit tests that construct Service without a
	// migrated Postgres or when the outbox insert itself failed. Stays
	// in place so a Postgres blip can't drop events; the trace-context
	// injection moves here so the legacy path keeps its observability.
	if s.writer == nil {
		return
	}
	headers := []kafka.Header{
		{Key: "event_type", Value: []byte(eventType)},
	}
	tracepkg.InjectKafkaHeaders(ctx, &headers)
	if err := s.writer.WriteMessages(ctx, kafka.Message{Value: b, Headers: headers}); err != nil {
		slog.Warn("kafka publish failed", "event", eventType, "error", err)
	}
}

// ─── Seller Onboarding ───────────────────────────────────────

type OnboardSellerInput struct {
	UserID      uuid.UUID
	SellerType  string
	StoreName   string
	BrandName   *string
	Slug        string
	Description *string
	Email       string
	Phone       *string
	GSTNumber   *string
	State       *string
	City        *string
	PostalCode  *string
}

func (s *Service) OnboardSeller(ctx context.Context, in OnboardSellerInput) (*postgres.Seller, error) {
	if in.StoreName == "" {
		return nil, fmt.Errorf("store_name is required")
	}
	if in.Slug == "" {
		in.Slug = slugify(in.StoreName)
	}
	sel := &postgres.Seller{
		UserID:             in.UserID,
		SellerType:         in.SellerType,
		StoreName:          in.StoreName,
		BrandName:          in.BrandName,
		Slug:               in.Slug,
		Description:        in.Description,
		Email:              in.Email,
		Phone:              in.Phone,
		GSTNumber:          in.GSTNumber,
		State:              in.State,
		City:               in.City,
		PostalCode:         in.PostalCode,
		VerificationStatus: "pending",
		StoreStatus:        "active",
	}
	if err := s.store.CreateSeller(ctx, sel); err != nil {
		return nil, fmt.Errorf("create seller: %w", err)
	}
	s.publish(ctx, "commerce.seller.registered", map[string]any{
		"seller_id": sel.ID, "user_id": sel.UserID, "store_name": sel.StoreName,
	})
	return sel, nil
}

func (s *Service) GetSellerProfile(ctx context.Context, userID uuid.UUID) (*postgres.Seller, error) {
	return s.store.GetSellerByUserID(ctx, userID)
}

// ─── Catalog ─────────────────────────────────────────────────

type CreateProductInput struct {
	SellerID uuid.UUID
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
}

func (s *Service) CreateProduct(ctx context.Context, in CreateProductInput) (*postgres.Product, error) {
	if in.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if len(in.Variants) == 0 {
		return nil, fmt.Errorf("at least one variant is required")
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
	}

	if err := s.store.CreateProduct(ctx, p); err != nil {
		return nil, fmt.Errorf("create product: %w", err)
	}

	for _, vi := range in.Variants {
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
		if err := s.store.CreateVariant(ctx, v); err != nil {
			return nil, fmt.Errorf("create variant %s: %w", vi.SKU, err)
		}
		// Initialize inventory
		if err := s.store.UpsertInventory(ctx, v.ID, in.SellerID, vi.StockQty); err != nil {
			slog.Warn("failed to init inventory", "variant_id", v.ID, "error", err)
		}
	}

	s.publish(ctx, "commerce.product.created", map[string]any{
		"product_id": p.ID, "seller_id": p.SellerID, "title": p.Title,
	})
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
	s.hydrateProductImages(ctx, products)
	return products, total, err
}

// ListProducts returns the customer-facing product catalog: published +
// approved only. Optional category filter and title query. Returns total so
// the UI can paginate. Legacy offset/limit shape — prefer ListProductsFiltered
// at scale.
func (s *Service) ListProducts(ctx context.Context, categoryID *uuid.UUID, query string, limit, offset int) ([]*postgres.Product, int, error) {
	products, total, err := s.store.ListProducts(ctx, categoryID, query, limit, offset)
	s.hydrateProductImages(ctx, products)
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
	return &ListProductsFilteredResult{Items: items, NextCursor: next}, nil
}

// ─── Variant CRUD (commerce TODO H#5) ─────────────────────────────────

// ListProductVariants returns every variant for a product including
// archived ones — public endpoint, no ownership check needed for read.
func (s *Service) ListProductVariants(ctx context.Context, productID uuid.UUID) ([]*postgres.ProductVariant, error) {
	return s.store.GetVariantsByProduct(ctx, productID)
}

// AddProductVariant creates a new variant on an existing product.
// Authorization: actor must be the owning seller of the product. The
// SKU must be unique per seller (the import path enforces this; here
// the DB UNIQUE constraint on (sku, seller_id) is the final guard).
func (s *Service) AddProductVariant(ctx context.Context, actorID, productID uuid.UUID, v *postgres.ProductVariant) (*postgres.ProductVariant, error) {
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
	v.ProductID = productID
	if v.Status == "" {
		v.Status = "active"
	}
	if v.CurrencyCode == "" {
		v.CurrencyCode = "INR"
	}
	if err := s.store.CreateVariant(ctx, v); err != nil {
		return nil, err
	}
	// Give the variant a stock row, at zero.
	//
	// POST /products does this for the variants it creates; this route did
	// not, so a variant added after launch had NO inventory_items row at
	// all. With `available_qty` now on the wire that difference is visible:
	// the variant reported no stock figure rather than "0 in stock", and the
	// seller's only way to create the row was PATCH …/stock, which needs a
	// row to adjust. Zero is the honest starting quantity — the seller sets
	// the real one next.
	if err := s.store.UpsertInventory(ctx, v.ID, product.SellerID, 0); err != nil {
		slog.Warn("failed to initialise inventory for a new variant",
			"variant_id", v.ID, "error", err)
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
func (s *Service) UpdateProductVariant(ctx context.Context, actorID, variantID uuid.UUID, updates map[string]any) (*postgres.ProductVariant, error) {
	variant, err := s.store.GetVariantByID(ctx, variantID)
	if err != nil {
		return nil, err
	}
	if variant == nil {
		return nil, postgres.ErrProductNotFound
	}
	product, err := s.store.GetProductByID(ctx, variant.ProductID)
	if err != nil {
		return nil, err
	}
	seller, err := s.GetSellerProfile(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if seller == nil || seller.ID != product.SellerID {
		return nil, ErrNotProductOwner
	}
	if err := s.store.UpdateVariant(ctx, variantID, updates); err != nil {
		return nil, err
	}
	return s.store.GetVariantByID(ctx, variantID)
}

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

func (s *Service) ListOrders(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*postgres.Order, error) {
	// HP3: pagination clamped (default 20, max 200).
	limit, offset = clampListPagination(limit, offset)
	orders, _, err := s.store.GetOrdersByCustomer(ctx, userID, limit, offset)
	return orders, err
}

// ListOrderCardsResult is the customer order-list response — Phase 2.1.
// NextCursor is empty on the last page.
type ListOrderCardsResult struct {
	Items      []postgres.OrderCard
	NextCursor string
}

// ListOrderCards returns one page of order cards for the customer using
// keyset pagination over (created_at, id). The richer shape (item count,
// seller count, first item) replaces the old anemic list the customer
// couldn't tell orders apart from. Replaces the offset/COUNT(*) path —
// no more table-scan on every page.
func (s *Service) ListOrderCards(ctx context.Context, userID uuid.UUID, limit int, cursor string) (*ListOrderCardsResult, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if cursor != "" {
		t, id, err := decodeOrderCursor(cursor)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor")
		}
		cursorTime = &t
		cursorID = &id
	}
	cards, hasMore, err := s.store.ListOrderCardsByCustomer(ctx, userID, cursorTime, cursorID, limit)
	if err != nil {
		return nil, err
	}
	res := &ListOrderCardsResult{Items: cards}
	if hasMore && len(cards) > 0 {
		last := cards[len(cards)-1]
		res.NextCursor = encodeOrderCursor(last.CreatedAt, last.ID)
	}
	return res, nil
}

// encodeOrderCursor / decodeOrderCursor are deliberately opaque to the
// client. Format is rfc3339nano|uuid wrapped in URL-safe base64; bumping
// the format only requires a server-side change because clients never
// crack it open.
func encodeOrderCursor(t time.Time, id uuid.UUID) string {
	raw := t.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

func decodeOrderCursor(s string) (time.Time, uuid.UUID, error) {
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, fmt.Errorf("bad cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return t, id, nil
}

// GetOrderWithItems returns the order and its line items, verifying ownership.
func (s *Service) GetOrderWithItems(ctx context.Context, orderID, userID uuid.UUID) (*postgres.Order, []*postgres.OrderItem, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, nil, err
	}
	if order.CustomerUserID != userID {
		return nil, nil, fmt.Errorf("order not found")
	}
	items, err := s.store.GetOrderItems(ctx, orderID)
	if err != nil {
		return nil, nil, err
	}
	return order, items, nil
}

// ListSellerOrders returns orders for a seller — used by the seller fulfillment dashboard.
// Authorization: caller must own the seller account (verified in handler).
// HP3: pagination clamped (default 20, max 200).
func (s *Service) ListSellerOrders(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*postgres.Order, error) {
	limit, offset = clampListPagination(limit, offset)
	orders, _, err := s.store.GetOrdersBySeller(ctx, sellerID, limit, offset)
	return orders, err
}

// SellerOrderCard is the rich DTO the fulfillment dashboard renders for one
// order containing the seller's items. The customer's other-seller items in
// a multi-seller order are intentionally excluded — sellers only see what
// they're responsible for shipping.
type SellerOrderCard struct {
	Order    *postgres.Order       `json:"order"`
	Items    []*postgres.OrderItem `json:"items"`
	Shipment *postgres.Shipment    `json:"shipment,omitempty"`

	// SellerSubtotalMinor is what this seller is owed for THIS order, in
	// paise, summed over their own lines.
	//
	// It reported 0 on a ₹929 order because it was summed from
	// `order_items.final_price` — the NUMERIC column migration 007 stopped
	// maintaining. Nothing was wrong with the arithmetic; it was adding up
	// a column nobody writes. The sum is now over `final_price_minor`
	// (with the pre-007 rupee fallback inside OrderItem.LineTotalMinor).
	SellerSubtotalMinor int64 `json:"seller_subtotal_minor"`
	// SellerSubtotal is the same figure in rupees, kept because the
	// existing dashboard reads this name. Derived from the paise, never
	// summed independently, so the two cannot disagree.
	SellerSubtotal float64 `json:"seller_subtotal"` // money-exempt: rupee mirror of seller_subtotal_minor, derived not computed

	DeliveryAddress []byte `json:"delivery_address,omitempty"`
}

// sellerLines splits an order's items into the ones belonging to sellerID
// and totals them in paise. One definition, so the fulfillment list and the
// order detail can never disagree about what a seller is owed.
func sellerLines(items []*postgres.OrderItem, sellerID uuid.UUID) ([]*postgres.OrderItem, int64) {
	mine := make([]*postgres.OrderItem, 0, len(items))
	var subtotalMinor int64
	for _, it := range items {
		if it.SellerID == sellerID {
			mine = append(mine, it)
			subtotalMinor += it.LineTotalMinor()
		}
	}
	return mine, subtotalMinor
}

// ListSellerFulfillment returns the seller's fulfillment queue, optionally
// filtered by stage:
//
//	stage="" / "all"     — every order with the seller's items
//	stage="unshipped"    — paid but no shipment booked for this seller yet
//	stage="in_transit"   — shipment booked, not yet delivered
//	stage="delivered"    — shipment delivered
//	stage="cancelled"    — order cancelled
//
// The DTO carries only this seller's items even in multi-seller orders.
// Phase 4.2 — replaces the bare ListSellerOrders surface so the dashboard
// can render item-level state in one round trip.
func (s *Service) ListSellerFulfillment(ctx context.Context, sellerID uuid.UUID, stage string, limit, offset int) ([]*SellerOrderCard, error) {
	// HP3: clamp pagination so a malicious / sloppy client can't ask for
	// 100k rows. Same defaults as other seller surfaces (20/200).
	limit, offset = clampListPagination(limit, offset)
	orders, _, err := s.store.GetOrdersBySeller(ctx, sellerID, limit, offset)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return []*SellerOrderCard{}, nil
	}

	// HP2: was 2 queries per order (items + shipment); now 2 queries
	// total for the whole page. 20-order page drops from 41 to 3 round
	// trips (orders + items batch + shipments batch).
	orderIDs := make([]uuid.UUID, 0, len(orders))
	for _, o := range orders {
		orderIDs = append(orderIDs, o.ID)
	}
	itemsByOrder, err := s.store.GetOrderItemsByOrderIDs(ctx, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("batch order items: %w", err)
	}
	shipmentsByOrder, err := s.store.GetShipmentsByOrderAndSeller(ctx, orderIDs, sellerID)
	if err != nil {
		return nil, fmt.Errorf("batch shipments: %w", err)
	}

	out := make([]*SellerOrderCard, 0, len(orders))
	for _, o := range orders {
		mine, subtotalMinor := sellerLines(itemsByOrder[o.ID], sellerID)
		if len(mine) == 0 {
			continue
		}
		card := &SellerOrderCard{
			Order:               o,
			Items:               mine,
			Shipment:            shipmentsByOrder[o.ID],
			SellerSubtotalMinor: subtotalMinor,
			SellerSubtotal:      round2(float64(subtotalMinor) / 100.0),
			DeliveryAddress:     o.DeliveryAddressSnapshot,
		}
		if !fulfillmentMatchesStage(card, stage) {
			continue
		}
		out = append(out, card)
	}
	return out, nil
}

// clampListPagination applies the standard commerce list-endpoint
// defaults: limit 20, max 200, non-negative offset. Centralised so the
// HP3 pagination caps are consistent across service-layer entry points
// regardless of which HTTP handler parsed the query string.
func clampListPagination(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// GetSellerOrderDetail returns a single order from the seller's perspective —
// their items + their shipment + the buyer's delivery snapshot — used by
// the seller order-detail page. Fails if the caller has no items in the
// order (authorization).
func (s *Service) GetSellerOrderDetail(ctx context.Context, sellerID, orderID uuid.UUID) (*SellerOrderCard, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil || order == nil {
		return nil, ErrOrderNotFound
	}
	items, _ := s.store.GetOrderItems(ctx, orderID)
	mine, subtotalMinor := sellerLines(items, sellerID)
	if len(mine) == 0 {
		return nil, ErrNotOrderOwner
	}
	shipment, _ := s.store.GetShipmentByOrderAndSeller(ctx, orderID, sellerID)
	return &SellerOrderCard{
		Order:               order,
		Items:               mine,
		Shipment:            shipment,
		SellerSubtotalMinor: subtotalMinor,
		SellerSubtotal:      round2(float64(subtotalMinor) / 100.0),
		DeliveryAddress:     order.DeliveryAddressSnapshot,
	}, nil
}

// fulfillmentMatchesStage routes a card into the dashboard tab buckets so
// callers can filter server-side without each tab issuing its own query.
func fulfillmentMatchesStage(card *SellerOrderCard, stage string) bool {
	switch stage {
	case "", "all":
		return true
	case "unshipped":
		return card.Order.Status != "cancelled" &&
			card.Order.PaymentStatus == "paid" &&
			(card.Shipment == nil || card.Shipment.Status == "pending")
	case "in_transit":
		return card.Shipment != nil &&
			card.Shipment.Status != "delivered" &&
			card.Shipment.Status != "pending" &&
			card.Order.Status != "cancelled"
	case "delivered":
		return card.Shipment != nil && card.Shipment.Status == "delivered"
	case "cancelled":
		return card.Order.Status == "cancelled"
	}
	return true
}

func (s *Service) GetOrder(ctx context.Context, orderID, userID uuid.UUID) (*postgres.Order, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order == nil || order.CustomerUserID != userID {
		return nil, ErrOrderNotFound
	}
	return order, nil
}

// ─── Order detail (the buyer's order screen) ─────────────────────────

// OrderDetail is one order as the buyer's order screen renders it.
//
// ─── WHY THIS TYPE EXISTS AT ALL ────────────────────────────────────────
//
// GET /v1/commerce/orders/:orderId used to return the raw `postgres.Order`
// row. Three things followed from that, all of them visible on the screen:
//
//	₹0.00 everywhere   the row's money fields are the NUMERIC columns
//	                   migration 007 stopped maintaining. The LIST endpoint
//	                   had already moved to `*_minor` (see OrderCard); detail
//	                   had not, so the same order showed ₹929 in the list and
//	                   ₹0.00 when you opened it.
//	no lines           the row is the order header. Items lived behind a
//	                   second call the app was not making.
//	no address         `delivery_address_snapshot` went out as a base64 blob
//	                   of a JSON object. After the PII cutover that blob is
//	                   the ROUTING fields only — no name, no phone, no
//	                   street — so it is not merely inconvenient to render,
//	                   it is not the address any more. The real one is in
//	                   `delivery_address_snapshot_enc` and has to be opened
//	                   through the cipher.
//
// And `can_cancel`, which the screen needs to decide whether to draw the
// cancel button at all, existed nowhere: the app was guessing from `status`
// against its own copy of the rules.
//
// The money field names match OrderCard exactly, so the list and the detail
// deserialise into the same client model.
type OrderDetail struct {
	ID          uuid.UUID `json:"id"`
	OrderNumber string    `json:"order_number"`

	SubtotalMinor money.Paise `json:"subtotal_minor"`
	DiscountMinor money.Paise `json:"discount_minor"`
	ShippingMinor money.Paise `json:"shipping_minor"`
	TaxMinor      money.Paise `json:"tax_minor"`
	TotalMinor    money.Paise `json:"total_minor"`
	Currency      string      `json:"currency"`

	PaymentMethod *string `json:"payment_method,omitempty"`
	PaymentStatus string  `json:"payment_status"`
	Status        string  `json:"status"`

	Items []OrderDetailItem `json:"items"`

	// DeliveryAddress is the decrypted order snapshot — the address as it
	// was when the order was placed, not the address the customer has now.
	// Nil when the order predates the snapshot, or when the cipher is not
	// configured; the rest of the order still renders in that case, because
	// a missing address is not a reason to fail an order screen.
	DeliveryAddress *pii.Address `json:"delivery_address,omitempty"`

	CanCancel   bool    `json:"can_cancel"`
	TrackingURL *string `json:"tracking_url,omitempty"`

	CreatedAt      time.Time `json:"created_at"`
	CreatedAtEpoch int64     `json:"created_at_epoch"`
}

// OrderDetailItem is one line of the order, in paise.
type OrderDetailItem struct {
	ID             uuid.UUID   `json:"id"`
	ProductID      uuid.UUID   `json:"product_id"`
	VariantID      uuid.UUID   `json:"variant_id"`
	SellerID       uuid.UUID   `json:"seller_id"`
	ProductTitle   string      `json:"product_title"`
	SKU            string      `json:"sku"`
	Quantity       int         `json:"quantity"`
	UnitMRPMinor   money.Paise `json:"unit_mrp_minor"`
	UnitPriceMinor money.Paise `json:"unit_price_minor"`
	TaxMinor       money.Paise `json:"tax_minor"`
	LineTotalMinor money.Paise `json:"line_total_minor"`
	Status         string      `json:"status"`
	TrackingNumber *string     `json:"tracking_number,omitempty"`
}

// GetOrderDetail assembles the buyer's order screen: totals in paise, the
// line items, the decrypted delivery address, whether the cancel button
// applies, and a tracking URL once a shipment carries one.
//
// Ownership is checked the same way GetOrder checks it — a stranger gets
// "not found" rather than a 403, so order ids cannot be probed.
func (s *Service) GetOrderDetail(ctx context.Context, orderID, userID uuid.UUID) (*OrderDetail, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, postgres.ErrOrderNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	if order == nil || order.CustomerUserID != userID {
		return nil, ErrOrderNotFound
	}

	items, err := s.store.GetOrderItems(ctx, orderID)
	if err != nil {
		return nil, err
	}
	lines := make([]OrderDetailItem, 0, len(items))
	for _, it := range items {
		lines = append(lines, OrderDetailItem{
			ID:             it.ID,
			ProductID:      it.ProductID,
			VariantID:      it.VariantID,
			SellerID:       it.SellerID,
			ProductTitle:   it.ProductTitle,
			SKU:            it.SKU,
			Quantity:       it.Quantity,
			UnitMRPMinor:   money.Paise(it.UnitMRPMinor),
			UnitPriceMinor: money.Paise(it.UnitPriceMinor),
			TaxMinor:       money.Paise(it.TaxAmountMinor),
			LineTotalMinor: money.Paise(it.LineTotalMinor()),
			Status:         it.Status,
			TrackingNumber: it.TrackingNumber,
		})
	}

	out := &OrderDetail{
		ID:             order.ID,
		OrderNumber:    order.OrderNumber,
		SubtotalMinor:  money.Paise(order.SubtotalMinorValue()),
		DiscountMinor:  money.Paise(order.DiscountMinorValue()),
		ShippingMinor:  money.Paise(order.ShippingMinorValue()),
		TaxMinor:       money.Paise(order.TaxMinorValue()),
		TotalMinor:     money.Paise(order.TotalMinor()),
		Currency:       coalesceStr(order.CurrencyCode, "INR"),
		PaymentMethod:  order.PaymentMethod,
		PaymentStatus:  order.PaymentStatus,
		Status:         order.Status,
		Items:          lines,
		CanCancel:      postgres.CustomerCanCancel(order.Status),
		CreatedAt:      order.CreatedAt,
		CreatedAtEpoch: order.CreatedAt.Unix(),
	}

	out.DeliveryAddress = s.openOrderAddress(ctx, order)

	// The first shipment that actually has a tracking URL. Multi-seller
	// orders can hold several; the buyer's screen shows one link and
	// GET /orders/:id/shipments carries the full set.
	if shipments, err := s.store.ListShipmentsByOrder(ctx, orderID); err == nil {
		for _, sh := range shipments {
			if sh != nil && sh.TrackingURL != nil && *sh.TrackingURL != "" {
				out.TrackingURL = sh.TrackingURL
				break
			}
		}
	} else {
		slog.Warn("order detail: could not load shipments", "order_id", orderID, "error", err)
	}

	return out, nil
}

// openOrderAddress decrypts the order's delivery-address snapshot.
//
// The sealed blob is the authority. The plaintext column is read only as
// the dual-write-window fallback the cutover mode permits — and after
// cutover that column holds routing fields alone, so serving it as "the
// address" would be quietly wrong rather than merely incomplete.
//
// Failures are logged and swallowed: an order screen that will not load
// because one field could not be decrypted is worse than one that renders
// the order without the address.
func (s *Service) openOrderAddress(ctx context.Context, order *postgres.Order) *pii.Address {
	if len(order.DeliveryAddressSnapshotEnc) > 0 && s.pii != nil {
		plain, err := s.pii.Open(ctx, pii.ScopeOrderSnapshot, order.DeliveryAddressSnapshotEnc)
		if err != nil {
			slog.Error("order detail: could not open the sealed address snapshot",
				"order_id", order.ID, "error", err)
			return nil
		}
		var addr pii.Address
		if err := json.Unmarshal([]byte(plain), &addr); err != nil {
			slog.Error("order detail: sealed address snapshot is not an address",
				"order_id", order.ID, "error", err)
			return nil
		}
		return &addr
	}
	if len(order.DeliveryAddressSnapshot) > 0 && s.piiCutover.AllowsPlaintextRead() {
		var addr pii.Address
		if err := json.Unmarshal(order.DeliveryAddressSnapshot, &addr); err != nil {
			slog.Error("order detail: plaintext address snapshot is not an address",
				"order_id", order.ID, "error", err)
			return nil
		}
		return &addr
	}
	return nil
}

// ─── Cart ────────────────────────────────────────────────────

func (s *Service) AddToCart(ctx context.Context, userID, variantID uuid.UUID, qty int) error {
	variant, err := s.store.GetVariantByID(ctx, variantID)
	if err != nil {
		return fmt.Errorf("variant not found: %w", err)
	}
	// LB-17 / v1 §5.6 — moderation is no longer advisory.
	//
	// This used to check ONLY `variant.Status != "active"`. Neither it nor
	// pricing looked at the product's `status` or `approval_status`, so a
	// seller could create a product, share a direct link and sell it while
	// `approval_status = 'pending'` — or keep selling after an admin had
	// rejected it. The entire moderation queue was decorative.
	//
	// This is the cheap early guard, so the customer gets a clear answer at
	// add-to-cart time. The AUTHORITATIVE check runs inside the checkout
	// transaction under the row lock, because a product can be rejected
	// between being added to a cart and being paid for.
	sellerID, eligible, err := s.store.ProductSaleEligibility(ctx, variantID)
	if err != nil {
		return err
	}
	if !eligible || variant.Status != "active" {
		return postgres.ErrProductUnavailable
	}

	// D2 — one seller per cart for P0.
	//
	// The schema supports multi-seller orders, but every cancellation,
	// refund and fulfilment rule in this module assumes one order is one
	// shipment from one seller. A mixed cart would mean partial cancels,
	// partial refunds and split shipments on day one, in precisely the area
	// that was already least correct.
	current, err := s.store.CartSellerID(ctx, userID)
	if err != nil {
		return err
	}
	if current != uuid.Nil && current != sellerID {
		return postgres.ErrMultipleSellers
	}

	inv, err := s.store.GetInventory(ctx, variantID)
	if err != nil {
		return fmt.Errorf("inventory not found: %w", err)
	}
	if inv.AvailableQty() < qty {
		return fmt.Errorf("only %d units available", inv.AvailableQty())
	}

	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return fmt.Errorf("get cart: %w", err)
	}

	// B5/LB-19: snapshot the price in PAISE, read the same way checkout
	// reads it. The float `variant.SellingPrice` that used to be passed here
	// never reached `price_snapshot_minor` at all, which silently disabled
	// checkout's price-change detection for every line added through this
	// route.
	priceMinor, err := s.store.VariantSellingPriceMinor(ctx, variantID)
	if err != nil {
		return fmt.Errorf("read variant price: %w", err)
	}
	return s.store.UpsertCartItem(ctx, cart.ID, variantID, variant.ProductID, qty, priceMinor)
}

func (s *Service) RemoveFromCart(ctx context.Context, userID, variantID uuid.UUID) error {
	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return err
	}
	return s.store.RemoveCartItem(ctx, cart.ID, variantID)
}

// UpdateCartItem sets the absolute quantity for a variant in the user's
// cart. Quantity 0 removes the line. Replaces the mobile delete+add
// roundtrip from commerce_repository.dart (Phase 1.2). UpsertCartItem at
// the SQL layer is INSERT ... ON CONFLICT DO UPDATE, so this is atomic at
// the row level — concurrent calls converge on a single final quantity.
func (s *Service) UpdateCartItem(ctx context.Context, userID, variantID uuid.UUID, qty int) error {
	if qty < 0 {
		return fmt.Errorf("quantity must be >= 0")
	}
	if qty == 0 {
		return s.RemoveFromCart(ctx, userID, variantID)
	}
	// AddToCart's stock check + upsert semantics are exactly what an
	// atomic set-to-N needs; reuse rather than duplicate.
	return s.AddToCart(ctx, userID, variantID, qty)
}

type CartSummary struct {
	CartID    uuid.UUID
	Items     []*CartItemDetail
	Subtotal  float64 // money-exempt: legacy float pricing behind the fenced+unregistered POST /v1/commerce/orders/checkout (B5); the P0 path is internal/store/postgres/checkout.go in paise
	ItemCount int
}

type CartItemDetail struct {
	Item    *postgres.CartItem
	Product *postgres.Product
	Variant *postgres.ProductVariant
}

func (s *Service) GetCart(ctx context.Context, userID uuid.UUID) (*CartSummary, error) {
	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	items, err := s.store.GetCartItems(ctx, cart.ID)
	if err != nil {
		return nil, err
	}

	// HP2: was 2N queries (one product + one variant per cart row); now
	// 2 queries total regardless of cart size. Typical 10-item cart drops
	// from ~21 to 3 round trips (cart-items + products + variants).
	productIDs := make([]uuid.UUID, 0, len(items))
	variantIDs := make([]uuid.UUID, 0, len(items))
	for _, ci := range items {
		productIDs = append(productIDs, ci.ProductID)
		variantIDs = append(variantIDs, ci.VariantID)
	}
	products, err := s.store.GetProductsByIDs(ctx, productIDs)
	if err != nil {
		return nil, fmt.Errorf("batch products: %w", err)
	}
	variants, err := s.store.GetVariantsByIDs(ctx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("batch variants: %w", err)
	}

	// The cart carries the whole Product, so hydrating it here gives every
	// cart line a renderable image for free — the same one the catalogue
	// showed. Without it the cart is a column of grey boxes next to the
	// product the buyer just looked at.
	hydrate := make([]*postgres.Product, 0, len(products))
	for _, p := range products {
		hydrate = append(hydrate, p)
	}
	s.hydrateProductImages(ctx, hydrate)

	summary := &CartSummary{CartID: cart.ID}
	for _, ci := range items {
		summary.Items = append(summary.Items, &CartItemDetail{
			Item:    ci,
			Product: products[ci.ProductID],
			Variant: variants[ci.VariantID],
		})
		summary.Subtotal += ci.PriceSnapshot * float64(ci.Quantity)
		summary.ItemCount += ci.Quantity
	}
	return summary, nil
}

// ─── Tax Calculation ─────────────────────────────────────────

type TaxBreakdown struct {
	TaxableAmount float64
	CGSTPct       float64
	SGSTPct       float64
	IGSTPct       float64
	CGSTAmount    float64
	SGSTAmount    float64
	IGSTAmount    float64
	TotalTax      float64
	IsInterstate  bool
}

// CalcTax returns GST breakdown for a given amount.
// If sellerState == customerState → intrastate (CGST+SGST); else interstate (IGST).
func CalcTax(amount, cgstPct, sgstPct, igstPct float64, sellerState, customerState string) TaxBreakdown {
	interstate := sellerState != customerState
	tb := TaxBreakdown{TaxableAmount: amount, CGSTPct: cgstPct, SGSTPct: sgstPct, IGSTPct: igstPct, IsInterstate: interstate}
	if interstate {
		tb.IGSTAmount = round2(amount * igstPct / 100)
		tb.TotalTax = tb.IGSTAmount
	} else {
		tb.CGSTAmount = round2(amount * cgstPct / 100)
		tb.SGSTAmount = round2(amount * sgstPct / 100)
		tb.TotalTax = tb.CGSTAmount + tb.SGSTAmount
	}
	return tb
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// couponWithinCaps returns true iff the coupon still has global +
// per-user redemptions available. Audit O10: GetCouponByCode already
// filters by is_active and expiry at the SQL layer, but the prior
// checkout never enforced max_uses or max_uses_per_user — a permissive
// public code could be redeemed unboundedly by anyone. max_uses == 0
// is treated as "unlimited"; same for max_uses_per_user.
func couponWithinCaps(ctx context.Context, s *Service, c *postgres.Coupon, userID uuid.UUID) bool {
	if c.MaxUses != nil && *c.MaxUses > 0 && c.UsesCount >= *c.MaxUses {
		return false
	}
	if c.MaxUsesPerUser > 0 {
		n, err := s.store.CountCouponUsagesByUser(ctx, c.ID, userID)
		if err != nil {
			// Fail closed on Postgres errors — reject the coupon rather
			// than risk over-redemption under a transient blip.
			return false
		}
		if n >= c.MaxUsesPerUser {
			return false
		}
	}
	return true
}

// ─── Checkout & Orders ───────────────────────────────────────

// QuoteInput is the request body for the server-authoritative checkout
// quote endpoint (POST /v1/commerce/checkout/quote — Phase 1.1).
type QuoteInput struct {
	UserID        uuid.UUID
	AddressID     uuid.UUID
	PaymentMethod string
	CouponCode    string
}

// QuoteItem is one line item in a checkout quote response.
type QuoteItem struct {
	VariantID    uuid.UUID `json:"variant_id"`
	ProductID    uuid.UUID `json:"product_id"`
	SellerID     uuid.UUID `json:"seller_id"`
	ProductTitle string    `json:"product_title"`
	SKU          string    `json:"sku,omitempty"`
	Quantity     int       `json:"quantity"`
	UnitPrice    float64   `json:"unit_price"`
	LineSubtotal float64   `json:"line_subtotal"` // money-exempt: legacy float pricing behind the fenced+unregistered POST /v1/commerce/orders/checkout (B5); the P0 path is internal/store/postgres/checkout.go in paise
}

// UnavailableQuoteItem is one cart row whose requested quantity exceeds
// available stock — surfaced on the Quote response so the UI can warn
// before "Place order". Strict checkout still fails on these.
type UnavailableQuoteItem struct {
	VariantID    uuid.UUID `json:"variant_id"`
	ProductID    uuid.UUID `json:"product_id"`
	ProductTitle string    `json:"product_title"`
	Available    int       `json:"available"`
	Requested    int       `json:"requested"`
}

// Quote is the server-authoritative pricing the client must render before
// placing an order. The same priceCart helper backs both Quote and
// Checkout, so what the customer sees in the quote is what they get on
// the order — there is no client-side recomputation.
type Quote struct {
	Subtotal         float64                `json:"subtotal"` // money-exempt: legacy float pricing behind the fenced+unregistered POST /v1/commerce/orders/checkout (B5); the P0 path is internal/store/postgres/checkout.go in paise
	CouponDiscount   float64                `json:"coupon_discount"`
	CouponCode       string                 `json:"coupon_code,omitempty"`
	Shipping         float64                `json:"shipping"`
	Tax              float64                `json:"tax"`
	GrandTotal       float64                `json:"grand_total"`
	Currency         string                 `json:"currency"`
	Items            []QuoteItem            `json:"items"`
	UnavailableItems []UnavailableQuoteItem `json:"unavailable_items"`
	CODEligible      bool                   `json:"cod_eligible"`
	Serviceable      bool                   `json:"serviceable"`
	SellerIDs        []uuid.UUID            `json:"seller_ids"`
}

// pricingResult is the shared, untransformed output of the cart-pricing
// pipeline. Quote shapes it into a customer-facing DTO; Checkout consumes
// the OrderItems directly to persist the order.
type pricingResult struct {
	Cart             *postgres.Cart
	CartItems        []*postgres.CartItem
	OrderItems       []*postgres.OrderItem
	Subtotal         float64 // money-exempt: legacy float pricing behind the fenced+unregistered POST /v1/commerce/orders/checkout (B5); the P0 path is internal/store/postgres/checkout.go in paise
	CouponDiscount   float64
	CouponCodePtr    *string
	Shipping         float64
	Tax              float64
	FinalAmount      float64
	UnavailableItems []UnavailableQuoteItem
}

// priceCart resolves the user's current cart into a fully-priced result
// using server-side product/variant/inventory data. strict=true (Checkout)
// rejects the call on any out-of-stock line; strict=false (Quote) returns
// the result with UnavailableItems populated and prices reflecting only
// the available rows. Coupon, shipping, and tax rules match Checkout 1:1.
//
// Caller must validate the user owns the cart; this helper trusts userID.
func (s *Service) priceCart(ctx context.Context, userID uuid.UUID, paymentMethod, couponCode string, strict bool) (*pricingResult, error) {
	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	cartItems, err := s.store.GetCartItems(ctx, cart.ID)
	if err != nil {
		return nil, err
	}
	if len(cartItems) == 0 {
		return nil, fmt.Errorf("cart is empty")
	}

	// Phase F2.1 — preload tiered prices for every variant in the cart
	// in one round trip. The fast-path `resolveLinePrice` below uses the
	// resulting map; falls back to variant.selling_price when a variant
	// has no tier ladder.
	variantIDs := make([]uuid.UUID, 0, len(cartItems))
	for _, ci := range cartItems {
		variantIDs = append(variantIDs, ci.VariantID)
	}
	tiersByVariant, err := s.store.ListPriceTiersForVariants(ctx, variantIDs)
	if err != nil {
		// Tier lookup failure is non-fatal — we fall back to the
		// variant's catalog price so checkout still completes.
		slog.Warn("priceCart: tier lookup failed; using catalog price", "error", err)
		tiersByVariant = map[uuid.UUID][]*postgres.PriceTier{}
	}

	// HP2: batch variant/product/inventory fetches for the whole cart.
	// Was 3N round trips (per cart row); now 3 fixed regardless of cart
	// size. A 10-item cart drops from ~31 to 4 round trips at checkout.
	productIDs := make([]uuid.UUID, 0, len(cartItems))
	for _, ci := range cartItems {
		productIDs = append(productIDs, ci.ProductID)
	}
	variantsByID, err := s.store.GetVariantsByIDs(ctx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("batch variants: %w", err)
	}
	productsByID, err := s.store.GetProductsByIDs(ctx, productIDs)
	if err != nil {
		return nil, fmt.Errorf("batch products: %w", err)
	}
	inventoryByVariant, err := s.store.GetInventoryByVariantIDs(ctx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("batch inventory: %w", err)
	}

	var orderItems []*postgres.OrderItem
	var unavailable []UnavailableQuoteItem
	var subtotal float64 // money-exempt: legacy float pricing behind the fenced+unregistered POST /v1/commerce/orders/checkout (B5); the P0 path is internal/store/postgres/checkout.go in paise
	totalTax := 0.0      // Phase 3+ will compute per-HSN GST; today the schema stores 0.

	for _, ci := range cartItems {
		variant, ok := variantsByID[ci.VariantID]
		if !ok {
			return nil, fmt.Errorf("variant %s not found", ci.VariantID)
		}
		product, ok := productsByID[ci.ProductID]
		if !ok {
			return nil, fmt.Errorf("product %s not found", ci.ProductID)
		}
		inv, ok := inventoryByVariant[ci.VariantID]
		if !ok {
			return nil, fmt.Errorf("inventory for %s not found", ci.VariantID)
		}
		if inv.AvailableQty() < ci.Quantity {
			if strict {
				return nil, fmt.Errorf("insufficient stock for %s: available %d", product.Title, inv.AvailableQty())
			}
			unavailable = append(unavailable, UnavailableQuoteItem{
				VariantID:    ci.VariantID,
				ProductID:    ci.ProductID,
				ProductTitle: product.Title,
				Available:    inv.AvailableQty(),
				Requested:    ci.Quantity,
			})
			continue
		}

		// Phase F2.1 — resolve unit price via tier ladder if one
		// exists. The walk is short (typical seller defines ≤5 tiers
		// per variant) and the slice is already sorted by min_qty.
		unitPrice := resolveTieredPrice(tiersByVariant[ci.VariantID], variant.SellingPrice, ci.Quantity)
		lineTotal := round2(unitPrice * float64(ci.Quantity))
		subtotal += lineTotal

		returnUntil := time.Now().AddDate(0, 0, product.ReturnPolicyDays)
		orderItems = append(orderItems, &postgres.OrderItem{
			ProductID:           ci.ProductID,
			VariantID:           ci.VariantID,
			SellerID:            product.SellerID,
			ProductTitle:        product.Title,
			SKU:                 variant.SKU,
			Quantity:            ci.Quantity,
			UnitMRP:             variant.MRP,
			UnitPrice:           unitPrice,
			DiscountAmount:      0,
			TaxAmount:           0,
			FinalPrice:          lineTotal,
			Status:              "confirmed",
			ReturnEligibleUntil: &returnUntil,
		})
	}

	// Coupon — Audit O10: enforces max_uses + max_uses_per_user caps.
	couponDiscount := 0.0
	var couponCodePtr *string
	if couponCode != "" {
		c, err := s.store.GetCouponByCode(ctx, couponCode)
		if err == nil && subtotal >= c.MinOrderAmount && couponWithinCaps(ctx, s, c, userID) {
			switch c.DiscountType {
			case "percentage":
				d := subtotal * c.DiscountValue / 100
				if c.MaxDiscountAmount != nil && d > *c.MaxDiscountAmount {
					d = *c.MaxDiscountAmount
				}
				couponDiscount = round2(d)
			case "flat":
				couponDiscount = c.DiscountValue
			}
		}
		cc := couponCode
		couponCodePtr = &cc
	}

	// Shipping: flat ₹40, free above ₹499. Phase 1.3 serviceability will
	// per-courier/per-pincode the real number.
	shipping := 40.0
	if subtotal > 499 {
		shipping = 0
	}

	final := subtotal - couponDiscount + shipping + totalTax
	return &pricingResult{
		Cart:             cart,
		CartItems:        cartItems,
		OrderItems:       orderItems,
		Subtotal:         subtotal,
		CouponDiscount:   couponDiscount,
		CouponCodePtr:    couponCodePtr,
		Shipping:         shipping,
		Tax:              totalTax,
		FinalAmount:      final,
		UnavailableItems: unavailable,
	}, nil
}

// ServiceabilityInput is the request for CheckServiceability — Phase 1.3.
// Pincode is the customer's drop pincode; ProductID resolves the seller
// (and pickup pincode) and the package weight unless overridden.
type ServiceabilityInput struct {
	Pincode       string
	ProductID     uuid.UUID
	VariantID     uuid.UUID
	SellerID      uuid.UUID
	PaymentMethod string
}

// CheckServiceability is the server-authoritative serviceability +
// COD-eligibility check that replaces the mobile pincode heuristic.
// Inputs come from the product's seller pickup address and weight; the
// courier adapter returns the authoritative answer (currently stub-ish
// for both adapters — the Shiprocket implementation is a follow-up).
func (s *Service) CheckServiceability(ctx context.Context, in ServiceabilityInput) (*courier.ServiceabilityResult, error) {
	if in.Pincode == "" {
		return &courier.ServiceabilityResult{Serviceable: false, Reason: "pincode required"}, nil
	}
	product, err := s.store.GetProductByID(ctx, in.ProductID)
	if err != nil {
		return nil, fmt.Errorf("product %s not found", in.ProductID)
	}
	sellerID := product.SellerID
	if in.SellerID != uuid.Nil {
		sellerID = in.SellerID
	}
	seller, err := s.store.GetSellerByID(ctx, sellerID)
	if err != nil {
		return nil, fmt.Errorf("seller %s not found", sellerID)
	}
	if seller.PostalCode == nil || *seller.PostalCode == "" {
		return &courier.ServiceabilityResult{
			Serviceable: false,
			Reason:      "seller pickup pincode not configured",
		}, nil
	}
	weightKg := 0.5 // sensible default until catalog enforces a weight
	if product.WeightGrams != nil && *product.WeightGrams > 0 {
		weightKg = float64(*product.WeightGrams) / 1000.0
	}
	pm := in.PaymentMethod
	if pm == "" {
		pm = "prepaid"
	}
	if s.courier == nil {
		// No courier wired (test/dev without provider): assume reachable
		// so the rest of the flow keeps working; production must have
		// COURIER_PROVIDER set.
		return &courier.ServiceabilityResult{
			Serviceable:   true,
			CODSupported:  true,
			EstimatedDays: 4,
			Courier:       "none",
			Reason:        "courier provider not configured",
		}, nil
	}
	return s.courier.CheckServiceability(ctx, courier.ServiceabilityRequest{
		PickupPincode: *seller.PostalCode,
		DropPincode:   in.Pincode,
		WeightKg:      weightKg,
		PaymentMethod: pm,
	})
}

// Quote returns the server-authoritative pricing for the user's current
// cart without persisting an order. The web + mobile checkout flows must
// render this before "Place order" so the customer sees the same numbers
// the server will use on Checkout.
//
// Serviceability and COD eligibility are placeholder true; Phase 1.3
// replaces them with the real courier + pincode check.
func (s *Service) Quote(ctx context.Context, in QuoteInput) (*Quote, error) {
	res, err := s.priceCart(ctx, in.UserID, in.PaymentMethod, in.CouponCode, false)
	if err != nil {
		return nil, err
	}
	items := make([]QuoteItem, 0, len(res.OrderItems))
	sellerSet := map[uuid.UUID]struct{}{}
	for _, oi := range res.OrderItems {
		items = append(items, QuoteItem{
			VariantID:    oi.VariantID,
			ProductID:    oi.ProductID,
			SellerID:     oi.SellerID,
			ProductTitle: oi.ProductTitle,
			SKU:          oi.SKU,
			Quantity:     oi.Quantity,
			UnitPrice:    oi.UnitPrice,
			LineSubtotal: oi.FinalPrice,
		})
		sellerSet[oi.SellerID] = struct{}{}
	}
	sellerIDs := make([]uuid.UUID, 0, len(sellerSet))
	for sid := range sellerSet {
		sellerIDs = append(sellerIDs, sid)
	}
	codeStr := ""
	if res.CouponCodePtr != nil {
		codeStr = *res.CouponCodePtr
	}
	return &Quote{
		Subtotal:         round2(res.Subtotal),
		CouponDiscount:   round2(res.CouponDiscount),
		CouponCode:       codeStr,
		Shipping:         round2(res.Shipping),
		Tax:              round2(res.Tax),
		GrandTotal:       round2(res.FinalAmount),
		Currency:         "INR",
		Items:            items,
		UnavailableItems: res.UnavailableItems,
		CODEligible:      true, // Phase 1.3 replaces with real check
		Serviceable:      true, // Phase 1.3 replaces with real check
		SellerIDs:        sellerIDs,
	}, nil
}

type CheckoutInput struct {
	UserID         uuid.UUID
	AddressID      uuid.UUID
	PaymentMethod  string
	CouponCode     string
	GiftMessage    *string
	IdempotencyKey string

	// ─── Phase 5 — Optional B2B context ────────────────────────
	// When OrganizationID is set, the caller must be an active 'admin'
	// or 'buyer' member. The org's approval_threshold + credit_terms
	// are applied at order create time.
	OrganizationID *uuid.UUID
	PONumber       *string
	CostCenter     *string
	InvoiceEmail   *string
}

// Checkout commits the user's cart as an order. All pricing (line totals,
// coupon, shipping, tax, grand total) is computed by priceCart so a future
// quote API and any client-side display can never drift from the value
// actually persisted on the order.
func (s *Service) Checkout(ctx context.Context, in CheckoutInput) (*postgres.Order, error) {
	// H4 — idempotency dedup. When the caller supplies an Idempotency-Key
	// header and we already have an order for that (user, key) pair,
	// return it verbatim instead of double-creating + double-charging.
	// This must run before priceCart so a double-tap during a slow round
	// trip doesn't waste a pricing query.
	if in.IdempotencyKey != "" {
		if existing, err := s.store.GetOrderByIdempotencyKey(ctx, in.UserID, in.IdempotencyKey); err == nil && existing != nil {
			return existing, nil
		}
	}

	res, err := s.priceCart(ctx, in.UserID, in.PaymentMethod, in.CouponCode, true)
	if err != nil {
		return nil, err
	}

	idempKey := in.IdempotencyKey
	if idempKey == "" {
		idempKey = fmt.Sprintf("%s-%d", in.UserID, time.Now().UnixNano())
	}

	// COD orders skip the gateway: confirmed immediately, payment_status
	// stays "pending" (orders_payment_status_check allows
	// pending|processing|paid|failed|refund_*; there is no cod_pending —
	// downstream code distinguishes COD by reading payment_method='cod').
	addrSnapshot, _ := json.Marshal(map[string]any{"address_id": in.AddressID})
	pm := in.PaymentMethod
	isCOD := strings.EqualFold(pm, "cod")
	orderStatus, paymentStatus := checkoutInitialState(isCOD)

	// Phase 5 — B2B context: validate org membership, apply approval
	// threshold + credit terms. A buyer on an org with approval_threshold
	// set and order >= threshold can't pay yet — the order parks in
	// approval_status=pending and Status="awaiting_approval"; an approver
	// then green-lights it via ApproveOrgOrder.
	var (
		orgID           *uuid.UUID
		approvalStatus  *string
		creditTermsDays int
		paymentDueDate  *time.Time
	)
	if in.OrganizationID != nil {
		member, err := s.requireOrgRole(ctx, *in.OrganizationID, in.UserID, "admin", "buyer")
		if err != nil {
			return nil, fmt.Errorf("org checkout: %w", err)
		}
		_ = member // role already validated
		org, err := s.store.GetOrganizationByID(ctx, *in.OrganizationID)
		if err != nil || org == nil {
			return nil, ErrOrgNotFound
		}
		if org.Status != "active" {
			return nil, fmt.Errorf("organization not active")
		}
		orgID = &org.ID
		// Approval gate
		if org.ApprovalThreshold != nil && res.FinalAmount >= *org.ApprovalThreshold {
			s := "pending"
			approvalStatus = &s
			orderStatus = "awaiting_approval"
			paymentStatus = "pending"
		} else if in.OrganizationID != nil {
			s := "not_required"
			approvalStatus = &s
		}
		// Credit terms: net N days. Only applied when the org has terms
		// configured AND the buyer chose the credit payment method.
		if org.CreditTermsDays > 0 && strings.EqualFold(pm, "credit") {
			creditTermsDays = org.CreditTermsDays
			due := time.Now().AddDate(0, 0, org.CreditTermsDays)
			paymentDueDate = &due
			// Credit orders defer payment but the order is otherwise
			// confirmed once approval clears.
			if approvalStatus == nil || *approvalStatus != "pending" {
				orderStatus = "confirmed"
			}
		}
	}

	order := &postgres.Order{
		CustomerUserID:          in.UserID,
		Subtotal:                res.Subtotal,
		DiscountAmount:          0,
		ShippingCharges:         res.Shipping,
		TaxAmount:               res.Tax,
		CouponCode:              res.CouponCodePtr,
		CouponDiscount:          res.CouponDiscount,
		FinalAmount:             res.FinalAmount,
		CurrencyCode:            "INR",
		PaymentMethod:           &pm,
		PaymentStatus:           paymentStatus,
		DeliveryAddressID:       &in.AddressID,
		DeliveryAddressSnapshot: addrSnapshot,
		GiftMessage:             in.GiftMessage,
		Status:                  orderStatus,
		IdempotencyKey:          &idempKey,
		OrganizationID:          orgID,
		PONumber:                in.PONumber,
		CostCenter:              in.CostCenter,
		InvoiceEmail:            in.InvoiceEmail,
		ApprovalStatus:          approvalStatus,
		CreditTermsDays:         creditTermsDays,
		PaymentDueDate:          paymentDueDate,
	}

	if err := s.store.CreateOrder(ctx, order, res.OrderItems); err != nil {
		if in.IdempotencyKey != "" && isPostgresUniqueViolation(err) {
			if existing, lookupErr := s.store.GetOrderByIdempotencyKey(ctx, in.UserID, in.IdempotencyKey); lookupErr == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Hard-reserve inventory (COD deducts immediately — no gateway step).
	for _, ci := range res.CartItems {
		if isCOD {
			if err := s.store.DeductStock(ctx, ci.VariantID, ci.Quantity, order.ID); err != nil {
				slog.Warn("failed to deduct stock for COD", "variant", ci.VariantID, "error", err)
			}
			continue
		}
		if err := s.store.ReserveStock(ctx, ci.VariantID, in.UserID, ci.Quantity, &order.ID, "order", 30*time.Minute); err != nil {
			slog.Warn("failed to reserve stock", "variant", ci.VariantID, "error", err)
		}
	}

	_ = s.store.ClearCart(ctx, res.Cart.ID)

	if res.CouponCodePtr != nil {
		if c, err := s.store.GetCouponByCode(ctx, *res.CouponCodePtr); err == nil {
			_ = s.store.IncrCouponUsage(ctx, c.ID, in.UserID, order.ID)
		}
	}

	buyerEmail, buyerName := s.resolveBuyer(ctx, in.UserID)
	var sellerEmail, sellerName string
	if len(res.OrderItems) > 0 {
		if seller, err := s.store.GetSellerByID(ctx, res.OrderItems[0].SellerID); err == nil {
			sellerEmail, sellerName = s.resolveSeller(seller)
		}
	}

	s.publish(ctx, "commerce.order.created", map[string]any{
		"order_id": order.ID, "user_id": in.UserID,
		"order_number": order.OrderNumber, "amount": order.FinalAmount,
		"payment_method": pm,
		"buyer_email":    buyerEmail,
		"buyer_name":     buyerName,
	})
	s.publish(ctx, events.EventCommerceSellerNewOrder, map[string]any{
		"order_id":     order.ID,
		"order_number": order.OrderNumber,
		"seller_id":    sellerIDOrNil(res.OrderItems),
		"amount":       order.FinalAmount,
		"seller_email": sellerEmail,
		"seller_name":  sellerName,
	})
	if isCOD {
		s.publish(ctx, events.EventCommerceOrderPaid, map[string]any{
			"order_id":       order.ID,
			"order_number":   order.OrderNumber,
			"user_id":        in.UserID,
			"amount":         order.FinalAmount,
			"payment_method": "cod",
			"buyer_email":    buyerEmail,
			"buyer_name":     buyerName,
		})
		// Phase 6.1 — durable enqueue replaces the fire-and-forget goroutine.
		s.EnqueueFulfillPaidOrder(ctx, order.ID)
	}
	return order, nil
}

func sellerIDOrNil(items []*postgres.OrderItem) *uuid.UUID {
	if len(items) == 0 {
		return nil
	}
	return &items[0].SellerID
}

// ConfirmPayment is the customer-facing confirm path invoked by the HTTP
// handler after Razorpay checkout returns. It is the safety-critical entry
// point: previously the handler trusted any (payment_id, gateway) pair the
// client sent and marked the order paid. Now we:
//
//  1. Require the actor to own the order.
//  2. Require the order to actually be payment_pending.
//  3. Forward the Razorpay signature triple to payments-service for HMAC
//     verification + amount check.
//  4. Refuse gateway=stub unless PAYMENTS_ALLOW_STUB is explicitly set.
//
// Idempotent — an already-paid order returns nil without re-running the
// fulfillment side effects.
func (s *Service) ConfirmPayment(ctx context.Context, orderID, actorID uuid.UUID, in ConfirmPaymentInput) error {
	order, err := s.getOrderForPayment(ctx, orderID)
	if err != nil {
		return err
	}
	if order.CustomerUserID != actorID {
		return ErrNotOrderOwner
	}
	if order.PaymentStatus == "paid" {
		return nil // idempotent: payment already applied
	}
	// v1 §5.3: this comparison read `order.PaymentStatus != "payment_pending"`,
	// testing the PAYMENT status against a value that only ever appears in
	// `orders.status` — and which the payment_status CHECK constraint does
	// not even permit. So every prepaid confirm returned
	// ErrOrderNotPaymentPending and the amount-verified branch below was
	// unreachable. The predicate is now postgres.OrderPayable — the same one
	// MarkOrderPaid enforces in SQL — so a confirm and the guard can never
	// disagree about which states accept money.
	if !postgres.OrderPayable(order.Status, order.PaymentStatus) {
		return fmt.Errorf("%w (status=%s payment_status=%s)", ErrOrderNotPaymentPending, order.Status, order.PaymentStatus)
	}

	gateway := in.Gateway
	if gateway == "" {
		gateway = "razorpay"
	}
	if gateway == "stub" && !s.stubGatewayAllowed() {
		return ErrStubGatewayInProd
	}
	if s.payments == nil {
		return ErrPaymentsClientMissing
	}

	// The intent a callback may be checked against is the one commerce
	// bound to the order at checkout — never one the client names. A client
	// that names a different intent is refused before payments is asked.
	intentID, err := s.orders.OrderPaymentIntentID(ctx, orderID)
	if err != nil || intentID == uuid.Nil {
		return ErrPaymentVerifyFailed
	}
	if in.PaymentIntentID != uuid.Nil && in.PaymentIntentID != intentID {
		slog.Warn("payment verify: client named an intent that is not the order's",
			"order_id", orderID, "bound_intent", intentID, "claimed_intent", in.PaymentIntentID)
		return ErrPaymentVerifyFailed
	}
	// The payable amount comes from `final_amount_minor`, which is what the
	// checkout wrote and what the payment intent was opened for. It used to
	// be ROUND(order.FinalAmount*100) — the NUMERIC mirror migration 007
	// stopped maintaining — so on every P0 order the expected amount was
	// ZERO, and a client that honestly reported what it paid was refused
	// with PAYMENT_AMOUNT_MISMATCH.
	expectedMinor := order.TotalMinor()
	if in.AmountMinor != 0 && in.AmountMinor != expectedMinor {
		return ErrPaymentAmountMismatch
	}

	// A1 / review R-3 — with a real gateway THIS PATH MARKS NOTHING PAID.
	//
	// It used to call VerifyIntent (which itself transitioned the intent to
	// `succeeded`) and then applyPaidStatus. That made a browser callback an
	// approval-capable evaluator: whatever the client handed back became
	// terminal order state as long as the HMAC checked out. A provider can
	// reverse a payment, or never capture it, after that callback — and we
	// would already have committed stock and started fulfilment.
	//
	// The callback is ADVISORY. A positive verdict tells the app the
	// redirect was genuine so it can leave the spinner and begin polling
	// GET /v1/commerce/orders/:orderId/payment/status. The order becomes
	// paid only when a signature-verified provider webhook reaches
	// Store.ApplyPaymentSucceeded, which re-checks amount, currency, payer
	// and intent against the order before committing any stock.
	verdict, err := s.payments.VerifyCallback(ctx, intentID,
		in.RazorpayOrderID, in.RazorpayPaymentID, in.RazorpaySignature,
		money.Paise(expectedMinor))
	if err != nil {
		slog.Warn("commerce: advisory callback verification failed",
			"order_id", orderID, "intent_id", intentID, "error", err)
		return ErrPaymentVerifyFailed
	}
	if verdict == nil || !verdict.Verified {
		return ErrPaymentVerifyFailed
	}
	// Bind the verified intent to this order + actor. payments-service
	// echoes the intent's reference/payer; an intent minted for another
	// order (or by another user) is refused even with a valid signature.
	// These checks can only make the verdict NEGATIVE.
	if verdict.ReferenceID != uuid.Nil && verdict.ReferenceID != orderID {
		slog.Warn("payment verify: intent references a different order",
			"order_id", orderID, "intent_id", intentID, "intent_ref", verdict.ReferenceID)
		return ErrPaymentVerifyFailed
	}
	if verdict.PayerID != uuid.Nil && verdict.PayerID != actorID {
		slog.Warn("payment verify: intent payer is not the order customer",
			"order_id", orderID, "intent_id", intentID)
		return ErrPaymentVerifyFailed
	}
	if verdict.AmountMinor != 0 && verdict.AmountMinor != expectedMinor {
		return ErrPaymentAmountMismatch
	}

	if gateway != "stub" {
		// Deliberately no state change: nil means "your callback looks
		// genuine", never "you have paid".
		return nil
	}

	// The stub gateway is the one case where the callback IS the last word:
	// no PSP exists to send a webhook, so nothing else can ever settle the
	// order.
	//
	// ─── AND THE FLAG ALONE IS NOT ENOUGH TO ESTABLISH THAT ──────────────
	//
	// PAYMENTS_ALLOW_STUB is a COMMERCE-side flag. It says what this service
	// was told, not what payments-service is actually running, and the two
	// can disagree — this dev stack currently does, with the flag on here
	// and real Razorpay test credentials over there. In that configuration a
	// webhook DOES exist, so treating a client callback as terminal would
	// restore exactly the authority A1/LB-3 removed: whoever holds a genuine
	// signature settles the order without the webhook's amount, currency,
	// payer and intent re-checks ever running.
	//
	// So the last question is put to payments-service itself. An intent
	// carries `client_session` if and only if a real Provider adapter is
	// configured there (payments' withClientSession attaches it from
	// h.provider). Its presence means a webhook is coming, and the callback
	// goes back to being advisory.
	//
	// Failing to ask is fatal rather than permissive: an unreachable
	// payments-service is not evidence that no provider exists.
	if err := s.assertNoRealProvider(ctx, intentID); err != nil {
		return err
	}

	// Same guarded transition the webhook consumer would take — one UPDATE,
	// converging with any concurrent caller, refusing a dead order.
	return s.applyPaidStatus(ctx, orderID, in.RazorpayPaymentID, gateway, &actorID, "customer")
}

// assertNoRealProvider refuses stub settlement when payments-service has a
// real PSP adapter configured.
//
// The signal is `client_session` on the intent: payments attaches it only
// when `h.provider != nil`, so it is present exactly when a provider exists
// that can sign — and therefore send — a webhook. Reading the fact off the
// intent rather than off our own environment means the two services cannot
// disagree about which settlement path is live.
func (s *Service) assertNoRealProvider(ctx context.Context, intentID uuid.UUID) error {
	intent, err := s.payments.GetIntent(ctx, intentID)
	if err != nil {
		slog.Error("commerce: refusing stub settlement — payments-service could not be asked "+
			"which provider is configured", "intent_id", intentID, "error", err)
		return ErrPaymentVerifyFailed
	}
	if intent != nil && len(intent.ClientSession) > 0 {
		slog.Warn("commerce: refusing stub settlement — payments-service has a real provider "+
			"configured, so the signature-verified webhook is the settlement path",
			"intent_id", intentID, "provider", intent.ClientSession["provider"])
		return ErrStubGatewayInProd
	}
	return nil
}

// checkoutInitialState is the (status, payment_status) pair a fresh order
// is created in. Prepaid orders park in payment_pending/pending until the
// gateway confirms; COD orders are confirmed immediately with payment
// still pending (there is no cod_pending value — downstream code reads
// payment_method='cod'). The pair MUST satisfy postgres.OrderPayable, or
// ConfirmPayment / the payments consumer can never move the order to
// paid — that was the bug behind the always-409 confirm endpoint, which
// compared payment_status against the *order* status value
// "payment_pending". The unit test pins the two together.
func checkoutInitialState(isCOD bool) (orderStatus, paymentStatus string) {
	if isCOD {
		return "confirmed", "pending"
	}
	return "payment_pending", "pending"
}

// getOrderForPayment loads an order for the money path, mapping the
// store's "no rows" into ErrOrderNotFound (GetOrderByID returns a
// non-nil pointer alongside pgx.ErrNoRows).
func (s *Service) getOrderForPayment(ctx context.Context, orderID uuid.UUID) (*postgres.Order, error) {
	if s.orders == nil {
		return nil, fmt.Errorf("order store not configured")
	}
	order, err := s.orders.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, postgres.ErrOrderNotFound) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	if order == nil {
		return nil, ErrOrderNotFound
	}
	return order, nil
}

// OrderActorRole describes how the supplied actor relates to an order —
// used by the shipment / invoice / return handlers to gate writes to the
// seller of at least one item and to gate reads to the customer or a
// seller. Returns ErrOrderNotFound when the order does not exist.
type OrderActorRole struct {
	IsCustomer bool
	IsSeller   bool
}

// OrderActor inspects the order + its items and reports whether the
// supplied actor is the customer and/or a seller of any item.
//
// ─── TWO KINDS OF ID ────────────────────────────────────────────────────
//
// `actorID` is a USER id — it arrives as X-User-Id. `order_items.seller_id`
// is a SELLER id, the primary key of a row in `sellers`. They are drawn
// from different tables and never collide, so the comparison
//
//	if it.SellerID == actorID
//
// could not be true for any caller, and IsSeller was permanently false.
// Every write it gates — POST /orders/:id/shipment, POST /orders/:id/invoice
// — answered 403 to the seller who actually owned the line, and the reads
// (GET …/shipment, …/shipments, …/invoice) answered 403 too because the
// seller is not the customer either. A seller could not act on their own
// order at all, and the 403 said "actor is not a seller on this order",
// which is exactly what it looked like from the outside.
//
// The caller's seller profile is resolved first and the comparison is
// seller-id to seller-id. A caller with no seller profile is simply not a
// seller — that is not an error, so a plain buyer still gets the customer
// half of the role rather than a 500.
func (s *Service) OrderActor(ctx context.Context, orderID, actorID uuid.UUID) (OrderActorRole, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return OrderActorRole{}, err
	}
	if order == nil {
		return OrderActorRole{}, ErrOrderNotFound
	}
	role := OrderActorRole{IsCustomer: order.CustomerUserID == actorID}

	sellerID, err := s.actorSellerID(ctx, actorID)
	if err != nil {
		return OrderActorRole{}, err
	}
	if sellerID == uuid.Nil {
		return role, nil
	}
	items, err := s.store.GetOrderItems(ctx, orderID)
	if err != nil {
		return OrderActorRole{}, err
	}
	for _, it := range items {
		if it.SellerID == sellerID {
			role.IsSeller = true
			break
		}
	}
	return role, nil
}

// actorSellerID maps a user id to their seller id, or uuid.Nil when the
// caller has no seller profile. "No seller row" is a fact about the caller,
// not a failure, so it is not an error; anything else is.
func (s *Service) actorSellerID(ctx context.Context, actorID uuid.UUID) (uuid.UUID, error) {
	seller, err := s.store.GetSellerByUserID(ctx, actorID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) || errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, err
	}
	if seller == nil {
		return uuid.Nil, nil
	}
	return seller.ID, nil
}

// ApplyVerifiedPaymentEvent is the system entry point the Kafka payments
// consumer uses when payments-service publishes payment.succeeded. The
// webhook has already verified the Razorpay signature upstream, so we
// trust the event and apply the paid status directly. Idempotent.
func (s *Service) ApplyVerifiedPaymentEvent(ctx context.Context, orderID uuid.UUID, paymentID string) error {
	return s.applyPaidStatus(ctx, orderID, paymentID, "razorpay", nil, "system")
}

// applyPaidStatus is the shared "actually mark this order paid + fire the
// downstream side effects" core, called from the stub-gateway
// ConfirmPayment and the service-level ApplyVerifiedPaymentEvent. (The
// production webhook path is Store.ApplyPaymentSucceeded, which applies the
// same transition guard inside its inbox transaction.)
//
// The transition itself is one guarded UPDATE (store.MarkOrderPaid), so
// when two callers race exactly one sees Applied=true and runs the side
// effects; the other observes payment_status=paid and returns nil. An
// order that is not payable any more (cancelled, refunded, …) yields
// ErrOrderNotPayable — money arrived for a dead order and someone has to
// refund it; the caller decides how loudly to report that.
func (s *Service) applyPaidStatus(ctx context.Context, orderID uuid.UUID, paymentID, gateway string, actorID *uuid.UUID, actorType string) error {
	if s.orders == nil {
		return fmt.Errorf("order store not configured")
	}
	t, err := s.orders.MarkOrderPaid(ctx, orderID, paymentID, gateway, actorID, actorType)
	if err != nil {
		if errors.Is(err, postgres.ErrOrderNotFound) {
			return ErrOrderNotFound
		}
		return err
	}
	if !t.Applied {
		if t.PaymentStatus == "paid" {
			return nil // converged: the other caller won the race
		}
		return fmt.Errorf("%w (status=%s payment_status=%s)", ErrOrderNotPayable, t.Status, t.PaymentStatus)
	}

	// Deduct inventory (best-effort).
	items, _ := s.orders.GetOrderItems(ctx, orderID)
	for _, item := range items {
		if err := s.orders.DeductStock(ctx, item.VariantID, item.Quantity, orderID); err != nil {
			slog.Warn("failed to deduct stock", "variant", item.VariantID, "error", err)
		}
	}

	order, _ := s.orders.GetOrderByID(ctx, orderID)
	var buyerEmail, orderNumber string
	var amount float64 // money-exempt: seller earnings, fenced at /v1/commerce/seller/earnings (B5)
	if order != nil {
		buyerEmail, _ = s.resolveBuyer(ctx, order.CustomerUserID)
		orderNumber = order.OrderNumber
		amount = order.FinalAmount
	}
	s.publish(ctx, events.EventCommerceOrderPaid, map[string]any{
		"order_id":     orderID,
		"order_number": orderNumber,
		"amount":       amount,
		"payment_id":   paymentID,
		"buyer_email":  buyerEmail,
	})

	// Phase 6.1 — enqueue a durable fulfillment job rather than firing
	// `go s.fulfillPaidOrder(orderID)`. A service restart between this
	// point and the side effects (invoice + shipment) used to drop the
	// work entirely; now the worker picks it back up.
	s.EnqueueFulfillPaidOrder(ctx, orderID)
	return nil
}

// MarkPaymentFailed flags an order's payment as failed and releases the stock
// reservation made at checkout so other customers can buy the units. The
// order itself stays in payment_pending so the customer can retry — switching
// to a hard "payment_failed" terminal state would force them to rebuild the
// cart. Idempotent: a second call on an already-failed intent is a no-op,
// and a late payment.failed arriving after the order was paid is refused
// by the transition guard (failed is only reachable from pending /
// processing) so it can never clobber a captured payment.
func (s *Service) MarkPaymentFailed(ctx context.Context, orderID uuid.UUID, paymentID string) error {
	if s.orders == nil {
		return fmt.Errorf("order store not configured")
	}
	applied, err := s.orders.TransitionPaymentStatus(ctx, orderID, "failed", paymentID, "razorpay")
	if err != nil {
		return err
	}
	if !applied {
		return nil
	}
	items, _ := s.orders.GetOrderItems(ctx, orderID)
	order, err := s.getOrderForPayment(ctx, orderID)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if err := s.orders.ReleaseReservation(ctx, item.VariantID, order.CustomerUserID, item.Quantity); err != nil {
			slog.Warn("failed to release reservation",
				"variant", item.VariantID, "order", orderID, "error", err)
		}
	}
	return nil
}

// CancelOrder cancels an order that hasn't shipped yet. For prepaid
// orders we also kick off a refund via payments-service — without this,
// money landed in escrow at confirmation and stayed there. Refund
// happens best-effort: the cancel itself succeeds even when the refund
// call fails (an admin can retry); but on success we stamp the intent
// ID on the order so the refund consumer can flip payment_status when
// the gateway settles.
// CancelOrder delegates to the transactional implementation.
//
// LB-10 / M-2 / LB-8. The body that used to live here had three defects
// stacked on top of each other:
//
//  1. It never compared `actorID` to `order.customer_user_id`, so knowing
//     an order UUID was enough to cancel a stranger's order.
//  2. It never released or restored inventory, so a cancelled order's stock
//     was lost permanently — the seller's catalogue silently drained.
//  3. It had three `slog.Warn` + `return nil` branches on the refund leg
//     (no client, no intent, call failed), each reporting success while no
//     money moved and nothing remembered the debt.
//
// Store.CancelOrder does the ownership check, releases or restocks under the
// same lock as the status change, and writes a DURABLE refund command that a
// worker owns. The permitted (state, actor) pairs live in the
// order_status_transitions table so the rule and the audit trail cannot
// drift apart.
func (s *Service) CancelOrder(ctx context.Context, orderID, actorID uuid.UUID, actorType, reason string) error {
	return s.store.CancelOrder(ctx, orderID, actorID, actorType, reason)
}

// ApplyRefundEvent is the system entry point the Kafka payments consumer
// uses when payments-service publishes payment.refunded. The refund may
// be a per-line return refund (set up by ApproveReturn) or an order-
// level cancel refund (set up by CancelOrder). We try both keyed lookups
// and log a no-op if neither matches — payments-service can refund
// intents that commerce-service didn't initiate, and that's fine.
func (s *Service) ApplyRefundEvent(ctx context.Context, intentID string) error {
	if intentID == "" {
		return nil
	}
	if n, err := s.store.MarkReturnRefundSucceededByIntent(ctx, intentID); err != nil {
		slog.Warn("refund consumer: return update failed", "intent_id", intentID, "error", err)
	} else if n > 0 {
		slog.Info("refund consumer: return marked succeeded", "intent_id", intentID, "rows", n)
		return nil
	}
	if n, err := s.store.MarkOrderRefundedByPayment(ctx, intentID); err != nil {
		slog.Warn("refund consumer: order update failed", "intent_id", intentID, "error", err)
	} else if n > 0 {
		slog.Info("refund consumer: order marked refunded", "intent_id", intentID, "rows", n)
		return nil
	}
	// No match — this was a refund commerce-service didn't initiate
	// (manual gateway action, etc.). Quiet info-level log.
	slog.Info("refund consumer: no matching commerce row", "intent_id", intentID)
	return nil
}

// ─── Returns ─────────────────────────────────────────────────

// GetReturnRequest fetches a return for read by either the customer or
// the seller. Phase 2.2 — adds the missing detail endpoint mobile was
// emulating by listing all returns and filtering client-side.
func (s *Service) GetReturnRequest(ctx context.Context, returnID, actorID uuid.UUID) (*postgres.ReturnRequest, error) {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil || r == nil {
		return nil, ErrReturnNotFound
	}
	if r.CustomerUserID != actorID && r.SellerID != actorID {
		return nil, ErrNotReturnParty
	}
	return r, nil
}

// BulkReturnItemInput is one line in a multi-item return request. Phase 2.3
// replaces the mobile fan-out (N HTTP calls) with a single endpoint.
type BulkReturnItemInput struct {
	OrderItemID       uuid.UUID
	SellerID          uuid.UUID
	ReasonCode        string
	ReasonDescription *string
}

// CreateReturnRequestBulk creates a return row per supplied item. Each
// row goes through the existing single-item validation + publish path,
// so multi-seller orders correctly notify each seller. On partial
// failure (e.g. item 2 of 3 is ineligible) the first N successfully-
// created return rows are returned alongside the error so the caller
// can surface what landed and what didn't.
func (s *Service) CreateReturnRequestBulk(ctx context.Context, orderID, customerUserID uuid.UUID, items []BulkReturnItemInput) ([]*postgres.ReturnRequest, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("at least one item required")
	}
	out := make([]*postgres.ReturnRequest, 0, len(items))
	for _, it := range items {
		r, err := s.CreateReturnRequest(ctx, CreateReturnInput{
			OrderID:           orderID,
			OrderItemID:       it.OrderItemID,
			CustomerUserID:    customerUserID,
			SellerID:          it.SellerID,
			ReasonCode:        it.ReasonCode,
			ReasonDescription: it.ReasonDescription,
		})
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

type CreateReturnInput struct {
	OrderID           uuid.UUID
	OrderItemID       uuid.UUID
	CustomerUserID    uuid.UUID
	SellerID          uuid.UUID
	ReasonCode        string
	ReasonDescription *string
}

func (s *Service) CreateReturnRequest(ctx context.Context, in CreateReturnInput) (*postgres.ReturnRequest, error) {
	r := &postgres.ReturnRequest{
		OrderID:           in.OrderID,
		OrderItemID:       in.OrderItemID,
		CustomerUserID:    in.CustomerUserID,
		SellerID:          in.SellerID,
		ReasonCode:        in.ReasonCode,
		ReasonDescription: in.ReasonDescription,
		Status:            "requested",
	}
	if err := s.store.CreateReturnRequest(ctx, r); err != nil {
		return nil, err
	}
	_ = s.store.UpdateOrderStatus(ctx, in.OrderID, "return_requested", &in.CustomerUserID, "customer", in.ReasonCode)
	s.publish(ctx, "commerce.return.requested", map[string]any{
		"return_id": r.ID, "order_id": r.OrderID, "seller_id": r.SellerID,
	})
	return r, nil
}

// ApproveReturn moves a return from 'requested' to 'approved', books a
// reverse-pickup with the courier (pickup at the customer's address, drop
// at the seller), and kicks off the refund. The refund flow differs by
// payment method:
//   - Prepaid: call payments-service to refund the original gateway intent.
//     Refund status flips to 'pending' here; payments-service publishes a
//     payment.refunded event when the gateway settles, which the
//     commerce-service consumer rolls into 'succeeded'.
//   - COD: there is no gateway transaction to reverse. We mark the refund
//     as 'manual' so the seller's payout is debited and Ops can wire the
//     cash back to the customer outside the system.
//
// All side effects best-effort: courier or payments-service unavailability
// degrades cleanly to "approved with refund pending" rather than rolling
// back the approval.
func (s *Service) ApproveReturn(ctx context.Context, returnID, actorID uuid.UUID) (*postgres.ReturnRequest, error) {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil {
		return nil, fmt.Errorf("get return: %w", err)
	}
	// Phase 0.5: only the seller of the returned item may approve.
	if r.SellerID != actorID {
		return nil, ErrNotReturnSeller
	}
	if r.Status == "approved" {
		return r, nil // idempotent
	}
	if r.Status != "requested" {
		return nil, fmt.Errorf("cannot approve return in status %q", r.Status)
	}

	if err := s.store.UpdateReturnStatus(ctx, returnID, "approved", nil); err != nil {
		return nil, fmt.Errorf("update return status: %w", err)
	}
	_ = s.store.UpdateOrderStatus(ctx, r.OrderID, "return_approved", &actorID, "seller", "return approved")

	// Reverse-pickup label.
	s.bookReturnPickup(ctx, r)

	// Phase 6.1 — refund happens via the durable fulfillment worker so a
	// payments-service blip doesn't leave the buyer unrefunded after a
	// successful approve API call.
	s.EnqueueProcessReturnApproved(ctx, returnID)

	out, _ := s.store.GetReturnRequestByID(ctx, returnID)
	if out == nil {
		out = r
	}
	s.publish(ctx, "commerce.return.approved", map[string]any{
		"return_id": out.ID, "order_id": out.OrderID,
		"seller_id": out.SellerID, "refund_status": out.RefundAmount,
	})
	return out, nil
}

// bookReturnPickup books a reverse-pickup shipment (customer → seller).
// Failures are logged but don't block approval — Ops can re-trigger via
// a retry endpoint later. The courier is the same provider as outbound.
func (s *Service) bookReturnPickup(ctx context.Context, r *postgres.ReturnRequest) {
	if s.courier == nil {
		return
	}
	order, err := s.store.GetOrderByID(ctx, r.OrderID)
	if err != nil || order == nil {
		slog.Warn("return pickup: get order failed", "return_id", r.ID, "error", err)
		return
	}
	// Pickup is the customer's delivery address (where the goods are now).
	if order.DeliveryAddressID == nil {
		slog.Warn("return pickup: order has no delivery address", "order_id", order.ID)
		return
	}
	addr, err := s.store.GetAddressByID(ctx, *order.DeliveryAddressID)
	if err != nil {
		slog.Warn("return pickup: address lookup failed", "error", err)
		return
	}
	pickup := courier.Address{
		Name: addr.ContactName, Phone: addr.Phone,
		Line1: addr.AddressLine1, City: addr.City, State: addr.State,
		Postal: addr.PostalCode, Country: addr.Country,
	}
	if addr.AddressLine2 != nil {
		pickup.Line2 = *addr.AddressLine2
	}

	// Drop is the seller's pickup address.
	seller, err := s.store.GetSellerByID(ctx, r.SellerID)
	if err != nil {
		slog.Warn("return pickup: seller lookup failed", "error", err)
		return
	}
	drop := courier.Address{Name: seller.StoreName, Country: "IN"}
	if seller.City != nil {
		drop.City = *seller.City
	}
	if seller.State != nil {
		drop.State = *seller.State
	}
	if seller.PostalCode != nil {
		drop.Postal = *seller.PostalCode
	}
	if seller.Phone != nil {
		drop.Phone = *seller.Phone
	}

	// Reuse the outbound CreateShipment; courier providers don't model
	// returns separately. PaymentMethod=prepaid because the customer
	// already paid (this is reverse logistics, not a sale).
	resp, err := s.courier.CreateShipment(ctx, courier.ShipmentRequest{
		OrderID:       r.OrderID.String() + "-return",
		OrderNumber:   "RTN-" + r.ID.String()[:8],
		PickupAddress: pickup,
		DropAddress:   drop,
		Weight:        0.5, // Use a default — real weight is the original item's, looked up below.
		PaymentMethod: "prepaid",
	})
	if err != nil {
		slog.Warn("return pickup: courier create failed", "error", err)
		return
	}
	if err := s.store.SetReturnPickupLabel(ctx, r.ID, s.courier.Name(), resp.AWBNumber, resp.LabelURL); err != nil {
		slog.Warn("return pickup: persist label failed", "error", err)
	}
}

// initiateReturnRefund decides between gateway refund and manual COD
// reconciliation, then records the outcome on the return row. Item-level
// refund amount = the original line's final price (one item per return
// today; multi-item returns would prorate here).
func (s *Service) initiateReturnRefund(ctx context.Context, r *postgres.ReturnRequest, actorID uuid.UUID) {
	order, err := s.store.GetOrderByID(ctx, r.OrderID)
	if err != nil || order == nil {
		slog.Warn("refund: get order failed", "return_id", r.ID, "error", err)
		return
	}
	items, _ := s.store.GetOrderItems(ctx, r.OrderID)
	var amount float64 // money-exempt: COD remittance, fenced at /v1/commerce/seller/cod-remittances (B5)
	for _, it := range items {
		if it.ID == r.OrderItemID {
			amount = it.FinalPrice
			break
		}
	}
	if amount == 0 {
		amount = order.FinalAmount // fallback: full-order refund
	}

	isCOD := order.PaymentMethod != nil && strings.EqualFold(*order.PaymentMethod, "cod")
	if isCOD {
		// No gateway leg — Ops settles cash externally.
		if err := s.store.SetReturnRefund(ctx, r.ID, "", "manual", amount); err != nil {
			slog.Warn("refund: persist manual cod refund failed", "error", err)
		}
		return
	}

	// LB-11 / D3 — returns are FENCED in Commerce P0 and this path is
	// unreachable by design.
	//
	// The return loop is not part of the launch scope, and its creation path
	// (`CreateReturnRequest`) persisted caller-supplied order_id,
	// order_item_id, customer_user_id and seller_id with no relational
	// check, so a caller could attach a return to a stranger's order and
	// move that order to `return_requested` (review M-3). Repairing that
	// loop is post-launch work; fencing it is cheaper and safer.
	//
	// The routes are unregistered, the worker entry points are disabled, and
	// migration 012 puts a trigger on `return_requests` that refuses the
	// INSERT — so a replayed legacy job cannot resurrect this either. This
	// function remains only so that a code path which somehow reaches it
	// fails loudly instead of half-refunding.
	slog.Error("commerce: return refund path invoked while returns are fenced (LB-11)",
		"return_id", r.ID, "order_id", r.OrderID)
	_ = s.store.SetReturnRefund(ctx, r.ID, "", "pending", amount)
}

// RejectReturn closes a return with status='rejected' and records the
// seller's reason. Order falls back to the previous fulfillment state
// (delivered) so the customer's UI shows the return is closed.
func (s *Service) RejectReturn(ctx context.Context, returnID, actorID uuid.UUID, reason string) (*postgres.ReturnRequest, error) {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil {
		return nil, fmt.Errorf("get return: %w", err)
	}
	// Phase 0.5: only the seller of the returned item may reject.
	if r.SellerID != actorID {
		return nil, ErrNotReturnSeller
	}
	if r.Status == "rejected" {
		return r, nil
	}
	if r.Status != "requested" {
		return nil, fmt.Errorf("cannot reject return in status %q", r.Status)
	}
	rsn := reason
	if err := s.store.UpdateReturnStatus(ctx, returnID, "rejected", &rsn); err != nil {
		return nil, fmt.Errorf("update return status: %w", err)
	}
	_ = s.store.UpdateOrderStatus(ctx, r.OrderID, "return_rejected", &actorID, "seller", reason)
	s.publish(ctx, "commerce.return.rejected", map[string]any{
		"return_id": r.ID, "order_id": r.OrderID, "reason": reason,
	})
	out, _ := s.store.GetReturnRequestByID(ctx, returnID)
	if out == nil {
		out = r
	}
	return out, nil
}

// ─── Payout Calculation ──────────────────────────────────────

// PayoutConfig holds the platform's commission / platform-fee / TDS rates.
// Phase 4.1 — previously these were hard-coded constants, making them
// impossible to change without redeploying. Now they're loaded from env at
// boot and applied as defaults when the per-call values aren't supplied.
//
// All values are percent (5.0 = 5%, not 0.05). Negative or out-of-bound
// values fall back to compiled defaults so a misconfigured env can't push
// a seller payout into negative territory.
type PayoutConfig struct {
	CommissionPct  float64
	PlatformFeePct float64
	TDSPct         float64
}

// fallbackPayoutConfig is the value the service falls back to when env vars
// are missing or out of bounds. Matches the historical hard-coded constants
// (5% commission, 2% platform fee, 1% TDS) so behaviour is unchanged for
// deployments that don't configure overrides.
var fallbackPayoutConfig = PayoutConfig{CommissionPct: 5.0, PlatformFeePct: 2.0, TDSPct: 1.0}

// WithPayoutConfig overrides the default fee schedule. Values <=0 or >100
// are rejected and replaced with the fallback so a misconfigured env can't
// produce nonsense payouts.
func (s *Service) WithPayoutConfig(cfg PayoutConfig) *Service {
	if cfg.CommissionPct <= 0 || cfg.CommissionPct > 100 {
		cfg.CommissionPct = fallbackPayoutConfig.CommissionPct
	}
	if cfg.PlatformFeePct <= 0 || cfg.PlatformFeePct > 100 {
		cfg.PlatformFeePct = fallbackPayoutConfig.PlatformFeePct
	}
	if cfg.TDSPct < 0 || cfg.TDSPct > 100 {
		cfg.TDSPct = fallbackPayoutConfig.TDSPct
	}
	s.payoutCfg = cfg
	return s
}

// payoutConfig returns the configured schedule, falling back to defaults if
// WithPayoutConfig was never called.
func (s *Service) payoutConfig() PayoutConfig {
	if s.payoutCfg.CommissionPct == 0 && s.payoutCfg.PlatformFeePct == 0 && s.payoutCfg.TDSPct == 0 {
		return fallbackPayoutConfig
	}
	return s.payoutCfg
}

// CalculateSellerPayout breaks a gross amount into commission, platform
// fee, TDS, and the seller's net payout. Per-call overrides (e.g. for a
// seller with a negotiated rate) win; if 0 is passed for a value the
// service's configured default is used.
func (s *Service) CalculateSellerPayout(grossAmount float64, commissionPct, platformFeePct float64) (net float64, commission float64, fee float64, tds float64) { // money-exempt: payout preview, fenced at /v1/commerce/payout (B5)
	cfg := s.payoutConfig()
	if commissionPct == 0 {
		commissionPct = cfg.CommissionPct
	}
	if platformFeePct == 0 {
		platformFeePct = cfg.PlatformFeePct
	}
	commission = round2(grossAmount * commissionPct / 100)
	fee = round2(grossAmount * platformFeePct / 100)
	tds = round2((grossAmount - commission - fee) * cfg.TDSPct / 100)
	net = round2(grossAmount - commission - fee - tds)
	return
}

// ─── Reviews ─────────────────────────────────────────────────

// CreateReview validates the supplied review against the reviewer's actual
// order history before persisting. Phase 0.6 — previously the handler set
// IsVerifiedPurchase=true blindly off the request fields, so any
// authenticated user could post a "verified" review for any product by
// supplying a fabricated order_item_id. Now we look up the order item and
// require:
//
//   - The order item belongs to an order owned by the reviewer.
//   - The item's product_id matches the reviewed product.
//   - The item's seller_id matches the supplied seller.
//   - The item is delivered (status=='delivered' or delivered_at set).
//
// IsVerifiedPurchase is derived from the validation; callers are no longer
// trusted to set it.
func (s *Service) CreateReview(ctx context.Context, r *postgres.Review) error {
	item, err := s.store.GetOrderItemByID(ctx, r.OrderItemID)
	if err != nil || item == nil {
		return ErrReviewOrderItemInvalid
	}
	if item.ProductID != r.ProductID || item.SellerID != r.SellerID {
		return ErrReviewOrderItemInvalid
	}
	order, err := s.store.GetOrderByID(ctx, item.OrderID)
	if err != nil || order == nil {
		return ErrReviewOrderItemInvalid
	}
	if order.CustomerUserID != r.ReviewerID {
		return ErrReviewOrderItemInvalid
	}
	if item.Status != "delivered" && item.DeliveredAt == nil {
		return ErrReviewItemNotDelivered
	}

	r.IsVerifiedPurchase = true // derived, not trusted from input

	if err := s.store.CreateReview(ctx, r); err != nil {
		return err
	}
	s.publish(ctx, "commerce.review.created", map[string]any{
		"product_id": r.ProductID, "seller_id": r.SellerID, "rating": r.Rating,
	})
	return nil
}

func (s *Service) GetProductReviews(ctx context.Context, productID uuid.UUID, limit, offset int) ([]*postgres.Review, int, error) {
	return s.store.GetProductReviews(ctx, productID, limit, offset)
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
func (s *Service) AddProductMedia(ctx context.Context, productID, actorUserID, mediaID uuid.UUID, mediaType string, sortOrder int) ([]postgres.ProductMedia, error) {
	if err := s.assertProductSeller(ctx, productID, actorUserID); err != nil {
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
	if err := s.store.AddProductMedia(ctx, productID, mediaID, mediaType, sortOrder); err != nil {
		return nil, err
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

// AddSellerResponseToReview lets the seller of a reviewed product attach
// a public response. Phase 2.4. Only the seller may respond; the response
// timestamp is set on first write and overwritten on a subsequent edit.
func (s *Service) AddSellerResponseToReview(ctx context.Context, reviewID, actorID uuid.UUID, response string) (*postgres.Review, error) {
	r, err := s.store.GetReviewByID(ctx, reviewID)
	if err != nil || r == nil {
		return nil, ErrReviewNotFound
	}
	if r.SellerID != actorID {
		return nil, ErrNotReviewSeller
	}
	if err := s.store.SetSellerResponse(ctx, reviewID, response); err != nil {
		return nil, err
	}
	return s.store.GetReviewByID(ctx, reviewID)
}

// ─── Addresses ───────────────────────────────────────────────

// sealAddressForWrite encrypts an address's identifying fields.
//
// B4. Every client-reachable address write goes through here, and it FAILS the
// whole write when KMS, the key ring or the cipher is unavailable. That is
// deliberate: the alternative is storing the customer's name, phone and street
// in plaintext because the key service had a bad minute, and a row written
// that way is indistinguishable afterwards from one written before the
// cutover.
func (s *Service) sealAddressForWrite(ctx context.Context, addr *postgres.CustomerAddress) (postgres.SealedAddressWrite, error) {
	if s.pii == nil {
		return postgres.SealedAddressWrite{}, fmt.Errorf(
			"commerce: the PII cipher is not configured; refusing to store an address in plaintext")
	}
	sealed, err := s.pii.SealAddress(ctx, pii.ScopeProfile, pii.Address{
		ContactName:  addr.ContactName,
		Phone:        addr.Phone,
		AddressLine1: addr.AddressLine1,
		AddressLine2: derefOrEmpty(addr.AddressLine2),
		Landmark:     derefOrEmpty(addr.Landmark),
		City:         addr.City,
		State:        addr.State,
		PostalCode:   addr.PostalCode,
		Country:      addr.Country,
	})
	if err != nil {
		return postgres.SealedAddressWrite{}, fmt.Errorf("commerce: sealing the address: %w", err)
	}
	return postgres.SealedAddressWrite{
		ContactName:    sealed.ContactName,
		Phone:          sealed.Phone,
		AddressLine1:   sealed.AddressLine1,
		AddressLine2:   sealed.AddressLine2,
		Landmark:       sealed.Landmark,
		KeyVersion:     sealed.KeyVersion,
		LookupHash:     sealed.LookupHash,
		WritePlaintext: s.piiCutover.WritesPlaintext(),
	}, nil
}

func (s *Service) AddAddress(ctx context.Context, addr *postgres.CustomerAddress) error {
	sealed, err := s.sealAddressForWrite(ctx, addr)
	if err != nil {
		return err
	}
	return s.store.CreateAddress(ctx, addr, sealed)
}

func (s *Service) GetAddresses(ctx context.Context, userID uuid.UUID) ([]*postgres.CustomerAddress, error) {
	return s.store.GetAddressesByUser(ctx, userID)
}

func (s *Service) UpdateAddress(ctx context.Context, id, userID uuid.UUID, addr *postgres.CustomerAddress) error {
	sealed, err := s.sealAddressForWrite(ctx, addr)
	if err != nil {
		return err
	}
	return s.store.UpdateAddress(ctx, id, userID, addr, sealed)
}

func (s *Service) DeleteAddress(ctx context.Context, id, userID uuid.UUID) error {
	return s.store.DeleteAddress(ctx, id, userID)
}

func (s *Service) SetDefaultAddress(ctx context.Context, id, userID uuid.UUID) error {
	return s.store.SetDefaultAddress(ctx, id, userID)
}

// ListMyReturns returns the calling customer's return history.
// HP3: pagination clamped (default 20, max 200).
func (s *Service) ListMyReturns(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*postgres.ReturnRequest, error) {
	limit, offset = clampListPagination(limit, offset)
	return s.store.ListReturnsByCustomer(ctx, userID, limit, offset)
}

// SellerReturnCard joins a return request with the order item it concerns
// + the order header. Lets the inbox render reason, item, refund amount,
// and the buyer's order without N+1 round trips on the UI side.
type SellerReturnCard struct {
	Return    *postgres.ReturnRequest `json:"return"`
	OrderItem *postgres.OrderItem     `json:"order_item,omitempty"`
	Order     *postgres.Order         `json:"order,omitempty"`
}

// ListSellerReturns returns the seller's returns inbox, optionally filtered
// by status (requested / approved / rejected / refunded / closed). Phase 4.3.
func (s *Service) ListSellerReturns(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]*SellerReturnCard, error) {
	// HP3: clamp pagination.
	limit, offset = clampListPagination(limit, offset)
	returns, err := s.store.ListReturnsBySeller(ctx, sellerID, status, limit, offset)
	if err != nil {
		return nil, err
	}
	if len(returns) == 0 {
		return []*SellerReturnCard{}, nil
	}

	// HP2: was 2 queries per return (item + order); now 2 queries total
	// per page. 20-return page drops from 41 to 3 round trips.
	itemIDs := make([]uuid.UUID, 0, len(returns))
	orderIDs := make([]uuid.UUID, 0, len(returns))
	seenOrder := map[uuid.UUID]struct{}{}
	for _, r := range returns {
		itemIDs = append(itemIDs, r.OrderItemID)
		if _, ok := seenOrder[r.OrderID]; !ok {
			seenOrder[r.OrderID] = struct{}{}
			orderIDs = append(orderIDs, r.OrderID)
		}
	}
	itemsByID, err := s.store.GetOrderItemsByIDs(ctx, itemIDs)
	if err != nil {
		return nil, fmt.Errorf("batch return items: %w", err)
	}
	ordersByID, err := s.store.GetOrdersByIDs(ctx, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("batch return orders: %w", err)
	}

	out := make([]*SellerReturnCard, 0, len(returns))
	for _, r := range returns {
		out = append(out, &SellerReturnCard{
			Return:    r,
			OrderItem: itemsByID[r.OrderItemID],
			Order:     ordersByID[r.OrderID],
		})
	}
	return out, nil
}

// SellerEarning is one delivered prepaid order item with the gross/commission
// /fee/tds/net breakdown computed via CalculateSellerPayout. Phase 4.4 — the
// prepaid analogue of CODRemittance. We don't have a persisted ledger table
// yet (deferred to Phase 6's outbox/saga work); this is a read-time derivation
// from order_items so the seller UI can show earnings without a write path.
type SellerEarning struct {
	OrderItemID      uuid.UUID  `json:"order_item_id"`
	OrderID          uuid.UUID  `json:"order_id"`
	OrderNumber      string     `json:"order_number"`
	ProductTitle     string     `json:"product_title"`
	SKU              string     `json:"sku"`
	Quantity         int        `json:"quantity"`
	GrossAmount      float64    `json:"gross_amount"`
	CommissionAmount float64    `json:"commission_amount"`
	PlatformFee      float64    `json:"platform_fee"`
	TDSAmount        float64    `json:"tds_amount"`
	NetAmount        float64    `json:"net_amount"`
	PaymentMethod    *string    `json:"payment_method,omitempty"`
	Status           string     `json:"status"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
}

// ListSellerEarnings returns delivered prepaid (non-COD) order items for a
// seller. COD earnings live in their own remittance ledger so they're not
// duplicated here. Phase 4.4.
// HP3: pagination clamped (default 20, max 200).
func (s *Service) ListSellerEarnings(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*SellerEarning, error) {
	limit, offset = clampListPagination(limit, offset)
	items, err := s.store.ListDeliveredItemsForSeller(ctx, sellerID, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]*SellerEarning, 0, len(items))
	for _, row := range items {
		// Skip COD items — they go through the COD remittance ledger.
		if row.PaymentMethod != nil && *row.PaymentMethod == "cod" {
			continue
		}
		net, commission, fee, tds := s.CalculateSellerPayout(row.Item.FinalPrice, 0, 0)
		out = append(out, &SellerEarning{
			OrderItemID:      row.Item.ID,
			OrderID:          row.Item.OrderID,
			OrderNumber:      row.OrderNumber,
			ProductTitle:     row.Item.ProductTitle,
			SKU:              row.Item.SKU,
			Quantity:         row.Item.Quantity,
			GrossAmount:      round2(row.Item.FinalPrice),
			CommissionAmount: commission,
			PlatformFee:      fee,
			TDSAmount:        tds,
			NetAmount:        net,
			PaymentMethod:    row.PaymentMethod,
			Status:           row.Item.Status,
			DeliveredAt:      row.Item.DeliveredAt,
		})
	}
	return out, nil
}

// PreviewReturnRefund returns the refund amount the seller would be debited
// if they approve the return. Reads the order item's FinalPrice; when the
// return already has an explicit RefundAmount, that wins. Phase 4.3 — keeps
// the inbox from having to recompute.
func (s *Service) PreviewReturnRefund(ctx context.Context, returnID, actorID uuid.UUID) (float64, error) {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil || r == nil {
		return 0, ErrReturnNotFound
	}
	if r.SellerID != actorID && r.CustomerUserID != actorID {
		return 0, ErrNotReturnParty
	}
	if r.RefundAmount != nil && *r.RefundAmount > 0 {
		return *r.RefundAmount, nil
	}
	item, err := s.store.GetOrderItemByID(ctx, r.OrderItemID)
	if err != nil || item == nil {
		return 0, ErrReturnNotFound
	}
	return round2(item.FinalPrice), nil
}

// ─── Helpers ─────────────────────────────────────────────────

func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func uniqueSlug(base string) string {
	return fmt.Sprintf("%s-%d", base, time.Now().UnixMilli()%100000)
}

func coalesceStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func coalesceInt(n, def int) int {
	if n == 0 {
		return def
	}
	return n
}

func isPostgresUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// derefOrEmpty flattens a nullable text column for sealing.
//
// The cipher seals strings; NULL and "" are the same absence as far as an
// address line is concerned, and collapsing them here keeps the sealed shape
// stable so a row whose landmark was NULL and one whose landmark was ""
// produce the same ciphertext structure.
func derefOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// WithMedia attaches the media-service client used to verify that a media id
// a client supplies actually belongs to that client.
//
// Nil-safe by construction, because a developer machine with no media-service
// must still be able to run the product flows. cmd/server refuses to START
// without it in a deployed environment — the decision about whether an
// unverified media reference is acceptable belongs there, not scattered
// across the call sites.
func (s *Service) WithMedia(c *media.Client) *Service {
	s.media = c
	return s
}

// verifyMedia checks every supplied media id belongs to actorUserID.
//
// A nil client means media verification is not configured. That is only
// reachable in a local environment (cmd/server exits otherwise), and it logs
// once per call rather than silently passing, because "the check was skipped"
// and "the check passed" must never look the same in a log.
func (s *Service) verifyMedia(ctx context.Context, actorUserID uuid.UUID, kind media.Kind, ids ...*uuid.UUID) error {
	if s.media == nil {
		for _, id := range ids {
			if id != nil && *id != uuid.Nil {
				slog.Warn("commerce: media ownership NOT verified — no media-service client configured",
					"media_id", *id, "actor", actorUserID)
			}
		}
		return nil
	}
	return s.media.VerifyAllOwned(ctx, actorUserID, kind, ids...)
}

// rupeeMirror renders an optional paise amount as the NUMERIC column's value.
//
// money-exempt: this writes the deprecated rupee mirror of a minor column, for
// the analytics readers that still scan it. Nothing computes from the result.
func rupeeMirror(minor *int64) *float64 {
	if minor == nil {
		return nil
	}
	r := float64(*minor) / 100.0
	return &r
}
