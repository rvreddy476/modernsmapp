// Package service implements the core business logic for commerce-service.
package service

import (
	"context"
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
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/counters"
	"github.com/atpost/shared/events"
	tracepkg "github.com/atpost/shared/o11y/trace"
	"github.com/google/uuid"
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
	// ErrInvalidSearchDocCursor means the reindex walk was handed a cursor
	// it did not mint. Handlers map it to 400 — see searchdoc.go for why a
	// bad cursor must not silently restart the walk.
	ErrInvalidSearchDocCursor = fmt.Errorf("invalid search-doc cursor")
)

// Service is the main commerce service.
type Service struct {
	store                *postgres.Store
	deliveryWork         deliveryWorkCoordinator
	localDeliveryEnabled bool
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
	// kycCutover is the SEPARATE cutover for seller KYC identifiers
	// (migration 035). Zero value ModeDual, for the same reason.
	kycCutover pii.Mode
	kyc        kyc.Validator
	// media verifies that a media id a client supplies actually belongs to
	// that client. Nil-safe: nil means unconfigured, which cmd/server only
	// permits in a local environment and says so loudly.
	media     *media.Client
	payoutCfg PayoutConfig
	// productAutoApprove — see WithProductAutoApprove.
	productAutoApprove bool
	// kycRequireDocumentKind — see WithKYCRequireDocumentKind.
	kycRequireDocumentKind bool
	// kycOnFile reads the media ids on a seller's KYC rows; nil means the
	// store (SellerDocumentMediaIDs). A seam for the unit tests.
	kycOnFile func(ctx context.Context, sellerID uuid.UUID) (map[uuid.UUID]bool, error)

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

	// now is the clock the delivery estimate reads "today" from, and the
	// home page's best-seller window is measured back from. nil means
	// time.Now; tests and the golden fixtures pin it (WithClock). Nothing
	// that touches money reads it.
	now func() time.Time
	// eta caches carrier serviceability answers for the delivery estimate
	// (engagement.go). Never consulted by the quote, which prices money.
	eta etaCache

	// platformCoupons is COMMERCE_PLATFORM_COUPONS_ENABLED (coupons_offers.go).
	platformCoupons bool
	// offers reads payments-service's bank-offer registry; offerCache holds
	// its answer for paymentOffersTTL. Set by WithPayments.
	offers     offersReader
	offerCache offerCache

	// scope is the sell-scope view of store (sellscope.go). nil means the
	// store itself; the unit tests set a fake through WithSellScopeStore.
	scope SellScopeStore
}

// WithClock pins the clock the delivery estimate computes "today" from.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
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

// WithProductAutoApprove makes POST /products/:id/submit approve the listing
// immediately when the seller is `approved` and the completeness gate
// passed, instead of queueing it for a moderator. Wired from
// COMMERCE_PRODUCT_AUTO_APPROVE in cmd/server; the dev compose sets it, no
// values file does. The moderation log still gets a row, with
// postgres.AutoApproveReason as the actor, so an auto-approved listing is
// distinguishable from a reviewed one afterwards.
func (s *Service) WithProductAutoApprove(on bool) *Service {
	s.productAutoApprove = on
	return s
}

// WithKYCRequireDocumentKind decides what a NEW KYC media id may be
// (COMMERCE_KYC_REQUIRE_DOCUMENT_KIND, onboarding.go kycMediaKinds). Off (the
// default): media-service's private `document` kind or an image, so the
// Android app that uploads KYC as images keeps working. On: a new id must be
// a `document`; ids already on the seller's rows may still be images. A video
// is refused either way.
// KYCRequireDocumentKind reads COMMERCE_KYC_REQUIRE_DOCUMENT_KIND: only the
// exact value "true" turns the strict rule on; absent, empty, "false" or
// anything else leaves it off, so a typo never refuses the current app.
func KYCRequireDocumentKind(value string) bool {
	return value == "true"
}

func (s *Service) WithKYCRequireDocumentKind(on bool) *Service {
	s.kycRequireDocumentKind = on
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
