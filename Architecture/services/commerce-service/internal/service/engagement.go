package service

// MStore engagement (pinned 1 Oct 2026): the delivery date on a product
// page, product like / dislike, review helpful votes, and share counts.
//
// Visibility is the caller's job for the product routes (the handler asks
// RequireProductReadable first, exactly as every other product read does);
// the review vote asks it here, because the route names a review and the
// product behind it has to be looked up first.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	// ErrInvalidPincode is a pincode that is not six digits starting 1-9.
	ErrInvalidPincode = errors.New("commerce: a pincode is six digits and does not start with 0")
	// ErrCannotVoteOwnReview: a reviewer may not mark their own review
	// helpful (or not helpful).
	ErrCannotVoteOwnReview = errors.New("commerce: you cannot vote on your own review")
	// ErrInvalidVote is a vote outside helpful / not_helpful.
	ErrInvalidVote = errors.New("commerce: a vote is helpful or not_helpful")
)

// Review vote wire values.
const (
	VoteHelpful    = "helpful"
	VoteNotHelpful = "not_helpful"
)

// ─── Delivery estimate ──────────────────────────────────────────────────

// istZone is India Standard Time. A fixed offset rather than
// time.LoadLocation("Asia/Kolkata"): India has no daylight saving, and a
// fixed zone cannot fail on an image without tzdata.
var istZone = time.FixedZone("IST", 5*3600+30*60)

// defaultDispatchDays applies when a seller never saved fulfilment settings
// (or saved a non-positive SLA). It is the 48-hour column default, in days.
const defaultDispatchDays = 2

// ValidPincode reports whether s has the shape of an Indian pincode: six
// digits, the first 1-9 (no postal zone is numbered 0).
func ValidPincode(s string) bool {
	if len(s) != 6 || s[0] < '1' || s[0] > '9' {
		return false
	}
	for i := 1; i < 6; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// dispatchDaysFromSLA rounds a seller's dispatch SLA in hours UP to whole
// days: 48 h is 2 days, 49 h is 3. A seller who promises 25 hours has not
// promised next-day dispatch.
func dispatchDaysFromSLA(hours int) int {
	if hours <= 0 {
		return defaultDispatchDays
	}
	return (hours + 23) / 24
}

// addDispatchDays moves n working days forward from d, Sundays not counted:
// no seller hands a parcel over on a Sunday. n == 0 is "today", or Monday if
// today is a Sunday.
func addDispatchDays(d time.Time, n int) time.Time {
	if n <= 0 {
		if d.Weekday() == time.Sunday {
			return d.AddDate(0, 0, 1)
		}
		return d
	}
	for i := 0; i < n; i++ {
		d = d.AddDate(0, 0, 1)
		if d.Weekday() == time.Sunday {
			d = d.AddDate(0, 0, 1)
		}
	}
	return d
}

// DeliveryWindow is the computed promise.
type DeliveryWindow struct {
	DeliverBy    time.Time // a date, midnight IST
	MinDays      int       // calendar days from today to the earliest arrival
	MaxDays      int       // calendar days from today to DeliverBy
	DispatchDays int       // the seller's SLA in whole working days
}

// ComputeDeliveryWindow is the arithmetic behind deliver_by:
//
//	today       = now's calendar date in Asia/Kolkata
//	dispatch    = ceil(slaHours / 24) working days (default 2), Sundays skipped
//	deliver_by  = today + dispatch + courierDays (calendar days in transit)
//	max_days    = deliver_by - today
//	min_days    = the same with the seller shipping one working day early
//
// ok=false when the carrier gave no transit time: a date we would have to
// invent is not a date.
func ComputeDeliveryWindow(now time.Time, slaHours, courierDays int) (DeliveryWindow, bool) {
	if courierDays <= 0 {
		return DeliveryWindow{}, false
	}
	local := now.In(istZone)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, istZone)
	dispatch := dispatchDaysFromSLA(slaHours)

	latest := addDispatchDays(today, dispatch).AddDate(0, 0, courierDays)
	earliest := addDispatchDays(today, dispatch-1).AddDate(0, 0, courierDays)
	days := func(t time.Time) int { return int(t.Sub(today).Hours()/24 + 0.5) }
	return DeliveryWindow{
		DeliverBy:    latest,
		MinDays:      days(earliest),
		MaxDays:      days(latest),
		DispatchDays: dispatch,
	}, true
}

// DeliveryEstimate is GET /products/:id/delivery-estimate. The date fields
// are absent when serviceable is false (or the carrier gave no transit
// time).
type DeliveryEstimate struct {
	Pincode       string `json:"pincode"`
	Serviceable   bool   `json:"serviceable"`
	DeliverBy     string `json:"deliver_by,omitempty"`
	MinDays       int    `json:"min_days,omitempty"`
	MaxDays       int    `json:"max_days,omitempty"`
	DispatchDays  int    `json:"dispatch_days,omitempty"`
	Courier       string `json:"courier"`
	PincodeSource string `json:"pincode_source"`
}

// Pincode sources.
const (
	PincodeFromQuery          = "query"
	PincodeFromDefaultAddress = "default_address"
)

// DefaultPincode is the signed-in buyer's default-address pincode.
func (s *Service) DefaultPincode(ctx context.Context, userID uuid.UUID) (string, bool, error) {
	return s.store.DefaultAddressPincode(ctx, userID)
}

// EstimateDelivery answers when productID can reach pincode. The caller has
// already checked the product is readable and the pincode's shape.
func (s *Service) EstimateDelivery(ctx context.Context, productID uuid.UUID, pincode, source string) (*DeliveryEstimate, error) {
	if !ValidPincode(pincode) {
		return nil, ErrInvalidPincode
	}
	product, err := s.store.GetProductByID(ctx, productID)
	if err != nil {
		return nil, err
	}
	if s.courier == nil {
		// Production refuses to boot without a courier; a dev stack with
		// none cannot promise a date and says so as "try later", not as a
		// made-up one.
		return nil, fmt.Errorf("%w: no courier configured", ErrCourierUnavailable)
	}
	out := &DeliveryEstimate{Pincode: pincode, Courier: s.courier.Name(), PincodeSource: source}

	pickup := s.sellerPickupPin(ctx, product.SellerID)
	if pickup == "" {
		// No pickup address: nothing can be shipped from this seller yet.
		return out, nil
	}
	grams := 500
	if product.WeightGrams != nil && *product.WeightGrams > 0 {
		grams = *product.WeightGrams
	}
	res, err := s.serviceabilityForEstimate(ctx, pickup, pincode, grams)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCourierUnavailable, err)
	}
	if res.Courier != "" {
		out.Courier = res.Courier
	}
	if !res.Serviceable {
		return out, nil
	}
	out.Serviceable = true

	sla, err := s.store.SellerDispatchSLAHours(ctx, product.SellerID)
	if err != nil {
		return nil, err
	}
	if w, ok := ComputeDeliveryWindow(s.clock(), sla, res.EstimatedDays); ok {
		out.DeliverBy = w.DeliverBy.Format("2006-01-02")
		out.MinDays, out.MaxDays, out.DispatchDays = w.MinDays, w.MaxDays, w.DispatchDays
	}
	return out, nil
}

// quoteDeliveryWindow is the same computation for the P0 quote's additive
// deliver_by / max_days keys. Best-effort: a quote is about money, and a
// failed SLA read must never fail it, so any error yields no date.
func (s *Service) quoteDeliveryWindow(ctx context.Context, sellerID uuid.UUID, courierDays int) (string, int) {
	sla, err := s.store.SellerDispatchSLAHours(ctx, sellerID)
	if err != nil {
		slog.WarnContext(ctx, "commerce: quote delivery date skipped", "seller_id", sellerID, "error", err)
		return "", 0
	}
	w, ok := ComputeDeliveryWindow(s.clock(), sla, courierDays)
	if !ok {
		return "", 0
	}
	return w.DeliverBy.Format("2006-01-02"), w.MaxDays
}

// weightBandGrams rounds a parcel weight up to the 500 g band a carrier's
// rate card bills in, which is also the cache key's weight.
func weightBandGrams(grams int) int {
	if grams <= 0 {
		return 500
	}
	return ((grams + 499) / 500) * 500
}

// serviceabilityForEstimate asks the carrier, through a 6-hour cache keyed
// by (pickup pin, drop pin, weight band) for any real carrier — Shiprocket
// rate-limits the serviceability API, and a product page is read far more
// often than a lane's transit time changes. The stub is never cached: it
// costs nothing and a dev stack should see a changed stub at once.
//
// This cache feeds the DATE only. The quote calls the carrier itself and
// never reads it, so no cached answer can ever price an order.
func (s *Service) serviceabilityForEstimate(ctx context.Context, pickup, drop string, grams int) (*courier.ServiceabilityResult, error) {
	band := weightBandGrams(grams)
	req := courier.ServiceabilityRequest{
		PickupPincode: pickup,
		DropPincode:   drop,
		WeightKg:      float64(band) / 1000.0,
		PaymentMethod: "prepaid", // A5
	}
	name := s.courier.Name()
	if name == "stub" {
		return s.courier.CheckServiceability(ctx, req)
	}
	key := fmt.Sprintf("%s|%s|%s|%d", name, pickup, drop, band)
	now := s.clock()
	if res, ok := s.eta.get(key, now); ok {
		return res, nil
	}
	res, err := s.courier.CheckServiceability(ctx, req)
	if err != nil {
		return nil, err
	}
	s.eta.put(key, res, now)
	return res, nil
}

// etaTTL is how long a carrier's serviceability answer is reused for.
const etaTTL = 6 * time.Hour

// etaCacheMax bounds the cache; at the bound, expired entries are swept and,
// if that frees nothing, the cache starts over. Coarse, and enough: the
// working set is (pickup pins x drop pins actually viewed) per 6 hours.
const etaCacheMax = 20000

// etaCache is an in-process TTL cache of serviceability results. The zero
// value is ready to use.
type etaCache struct {
	mu sync.Mutex
	m  map[string]etaEntry
}

type etaEntry struct {
	res courier.ServiceabilityResult
	at  time.Time
}

func (c *etaCache) get(key string, now time.Time) (*courier.ServiceabilityResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || now.Sub(e.at) >= etaTTL || now.Before(e.at) {
		return nil, false
	}
	res := e.res
	return &res, true
}

func (c *etaCache) put(key string, res *courier.ServiceabilityResult, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[string]etaEntry)
	}
	if len(c.m) >= etaCacheMax {
		for k, e := range c.m {
			if now.Sub(e.at) >= etaTTL {
				delete(c.m, k)
			}
		}
		if len(c.m) >= etaCacheMax {
			c.m = make(map[string]etaEntry)
		}
	}
	c.m[key] = etaEntry{res: *res, at: now}
}

// ─── Product reactions ──────────────────────────────────────────────────

// ReactionState is the reply to a reaction write. No dislike count: a
// dislike is private to the shopper who cast it.
type ReactionState struct {
	ViewerReaction *string `json:"viewer_reaction"`
	LikeCount      int64   `json:"like_count"`
}

// ReactToProduct sets userID's reaction. The caller has checked the
// product is live.
func (s *Service) ReactToProduct(ctx context.Context, productID, userID uuid.UUID, kind string) (*ReactionState, error) {
	n, err := s.store.SetProductReaction(ctx, productID, userID, kind)
	if err != nil {
		return nil, err
	}
	k := kind
	return &ReactionState{ViewerReaction: &k, LikeCount: n}, nil
}

// ClearProductReaction removes userID's reaction.
func (s *Service) ClearProductReaction(ctx context.Context, productID, userID uuid.UUID) (*ReactionState, error) {
	n, err := s.store.ClearProductReaction(ctx, productID, userID)
	if err != nil {
		return nil, err
	}
	return &ReactionState{LikeCount: n}, nil
}

// MarkViewerReaction fills p.ViewerReaction for a signed-in viewer (null
// when they have not reacted) and leaves it absent for an anonymous one.
// Soft: a failed read leaves the field absent rather than failing the page.
func (s *Service) MarkViewerReaction(ctx context.Context, viewer uuid.UUID, p *postgres.Product) {
	if p == nil || viewer == uuid.Nil {
		return
	}
	kind, err := s.store.ProductReactionOf(ctx, p.ID, viewer)
	if err != nil {
		slog.WarnContext(ctx, "commerce: viewer reaction read failed", "product_id", p.ID, "error", err)
		return
	}
	p.ViewerReaction = &postgres.ViewerReaction{Kind: kind}
}

// RequireProductLive is the buyer rule for an engagement WRITE (a like, a
// share): the product must be live for shoppers. Unlike a read, the owning
// seller gets no exception — nobody likes or shares a draft.
func (s *Service) RequireProductLive(ctx context.Context, productID uuid.UUID) error {
	return s.RequireProductReadable(ctx, productID, uuid.Nil)
}

// ─── Share ──────────────────────────────────────────────────────────────

// ShareChannels is the vocabulary POST /products/:id/share accepts.
var ShareChannels = map[string]bool{"native": true, "whatsapp": true, "copy_link": true, "other": true}

// RecordShare counts one share of a live product.
func (s *Service) RecordShare(ctx context.Context, productID uuid.UUID) error {
	return s.store.IncrProductShareCount(ctx, productID)
}

// ─── Review helpful votes ───────────────────────────────────────────────

// ReviewVoteState is the reply to a vote write. Only the helpful count is
// public.
type ReviewVoteState struct {
	ViewerVote   *string `json:"viewer_vote"`
	HelpfulCount int     `json:"helpful_count"`
}

// reviewVoteTarget resolves a review a shopper may vote on: it exists, it
// is listed, and its product is live. Anything else is ErrReviewNotFound —
// a review on a hidden product is not confirmed to exist.
func (s *Service) reviewVoteTarget(ctx context.Context, reviewID uuid.UUID) (*postgres.ReviewVoteTarget, error) {
	t, err := s.store.GetReviewVoteTarget(ctx, reviewID)
	if errors.Is(err, postgres.ErrReviewNotFoundP0) {
		return nil, ErrReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	if !t.Listed {
		return nil, ErrReviewNotFound
	}
	if err := s.RequireProductLive(ctx, t.ProductID); err != nil {
		if isProductNotFound(err) {
			return nil, ErrReviewNotFound
		}
		return nil, err
	}
	return t, nil
}

// VoteOnReview records userID's helpful / not_helpful vote.
func (s *Service) VoteOnReview(ctx context.Context, reviewID, userID uuid.UUID, vote string) (*ReviewVoteState, error) {
	// Visibility first: a hidden review is 404 whatever the body says.
	t, err := s.reviewVoteTarget(ctx, reviewID)
	if err != nil {
		return nil, err
	}
	if vote != VoteHelpful && vote != VoteNotHelpful {
		return nil, ErrInvalidVote
	}
	if t.ReviewerID == userID {
		return nil, ErrCannotVoteOwnReview
	}
	n, err := s.store.SetReviewVote(ctx, reviewID, userID, vote == VoteHelpful)
	if err != nil {
		return nil, err
	}
	v := vote
	return &ReviewVoteState{ViewerVote: &v, HelpfulCount: n}, nil
}

// ClearReviewVote removes userID's vote. The same visibility rule applies;
// the own-review rule does not need to (there is nothing to remove).
func (s *Service) ClearReviewVote(ctx context.Context, reviewID, userID uuid.UUID) (*ReviewVoteState, error) {
	if _, err := s.reviewVoteTarget(ctx, reviewID); err != nil {
		return nil, err
	}
	n, err := s.store.ClearReviewVote(ctx, reviewID, userID)
	if err != nil {
		return nil, err
	}
	return &ReviewVoteState{HelpfulCount: n}, nil
}

// NormaliseReviewSort maps the ?sort= value: "recent" or, for anything else
// (including absent), the default "helpful".
func NormaliseReviewSort(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), postgres.ReviewSortRecent) {
		return postgres.ReviewSortRecent
	}
	return postgres.ReviewSortHelpful
}

// ProductReviewRows is GET /products/:id/reviews: the reviews in the chosen
// order, each with its helpful count and the viewer's own vote.
func (s *Service) ProductReviewRows(ctx context.Context, productID, viewer uuid.UUID, sort string, limit, offset int) ([]*postgres.ReviewRow, int, error) {
	reviews, total, err := s.store.GetProductReviews(ctx, productID, NormaliseReviewSort(sort), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]uuid.UUID, 0, len(reviews))
	for _, r := range reviews {
		ids = append(ids, r.ID)
	}
	votes, err := s.store.ReviewVotesOf(ctx, viewer, ids)
	if err != nil {
		// Soft, like the favourites mark: the list is still right without
		// the viewer's own votes.
		slog.WarnContext(ctx, "commerce: viewer review votes read failed", "product_id", productID, "error", err)
		votes = map[uuid.UUID]bool{}
	}
	rows := make([]*postgres.ReviewRow, 0, len(reviews))
	for _, r := range reviews {
		row := &postgres.ReviewRow{Review: r, HelpfulCount: r.HelpfulCount}
		if helpful, ok := votes[r.ID]; ok {
			v := VoteNotHelpful
			if helpful {
				v = VoteHelpful
			}
			row.ViewerVote = &v
		}
		rows = append(rows, row)
	}
	return rows, total, nil
}
