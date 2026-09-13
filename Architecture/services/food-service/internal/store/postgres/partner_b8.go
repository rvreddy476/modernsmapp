package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/routing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Lane B8: the read-backs, readiness, partial PATCH, partner single reads and
// paise siblings the Feast Kitchen app needs. Every change here is additive:
// no existing field is renamed, retyped or removed.

// ─── Paise siblings ─────────────────────────────────────────────────────────

// PaiseOf is the integer-paise sibling of a float rupee value a response
// already carries. Every NUMERIC(12,2) amount is exact in paise, so rounding
// the float8 form recovers it; this is the one place that rounding happens.
func PaiseOf(rupees float64) int64 {
	return int64(math.Round(rupees * 100))
}

// FillPaise sets the paise siblings of the item's float prices.
func (m *MenuItem) FillPaise() {
	m.BasePricePaise = PaiseOf(m.BasePrice)
	m.DiscountPricePaise = PaiseOf(m.DiscountPrice)
}

// PartnerTotals is an order's float totals with a *_paise sibling for each.
type PartnerTotals struct {
	PriceBreakdown
	ItemSubtotalPaise       int64 `json:"item_subtotal_paise"`
	AddonTotalPaise         int64 `json:"addon_total_paise"`
	PackagingFeePaise       int64 `json:"packaging_fee_paise"`
	TaxTotalPaise           int64 `json:"tax_total_paise"`
	DeliveryFeePaise        int64 `json:"delivery_fee_paise"`
	PlatformFeePaise        int64 `json:"platform_fee_paise"`
	RestaurantDiscountPaise int64 `json:"restaurant_discount_paise"`
	CouponDiscountPaise     int64 `json:"coupon_discount_paise"`
	FinalAmountPaise        int64 `json:"final_amount_paise"`
}

func PartnerTotalsOf(b PriceBreakdown) PartnerTotals {
	return PartnerTotals{
		PriceBreakdown:          b,
		ItemSubtotalPaise:       PaiseOf(b.ItemSubtotal),
		AddonTotalPaise:         PaiseOf(b.AddonTotal),
		PackagingFeePaise:       PaiseOf(b.PackagingFee),
		TaxTotalPaise:           PaiseOf(b.TaxTotal),
		DeliveryFeePaise:        PaiseOf(b.DeliveryFee),
		PlatformFeePaise:        PaiseOf(b.PlatformFee),
		RestaurantDiscountPaise: PaiseOf(b.RestaurantDiscount),
		CouponDiscountPaise:     PaiseOf(b.CouponDiscount),
		FinalAmountPaise:        PaiseOf(b.FinalAmount),
	}
}

// PartnerOrder is an order as the restaurant partner routes return it: the
// customer shape with paise siblings in totals (the shallower Totals field
// shadows Order.Totals in JSON) and never the customer's drop-off code.
type PartnerOrder struct {
	*Order
	Totals PartnerTotals `json:"totals"`
}

func PartnerOrderOf(o *Order) *PartnerOrder {
	if o == nil {
		return nil
	}
	c := *o
	c.DeliveryCode = ""
	return &PartnerOrder{Order: &c, Totals: PartnerTotalsOf(c.Totals)}
}

// PartnerOrdersOf keeps a nil list nil, so an empty list serialises as before.
func PartnerOrdersOf(orders []Order) []*PartnerOrder {
	if orders == nil {
		return nil
	}
	out := make([]*PartnerOrder, 0, len(orders))
	for i := range orders {
		out = append(out, PartnerOrderOf(&orders[i]))
	}
	return out
}

// FillDerived sets the kitchen row's paise sibling and the RFC 3339 form of
// the accept deadline (accept_deadline_at stays Postgres text).
func (k *KitchenOrder) FillDerived(deadline *time.Time) {
	k.FinalAmountPaise = PaiseOf(k.FinalAmount)
	k.AcceptDeadlineAtRFC3339 = optionalRFC3339(deadline)
}

// PartnerSummaryMap is GET .../reports/summary.
func PartnerSummaryMap(restaurantID uuid.UUID, orders, delivered, refunded int, gross, commission, refunds float64) map[string]any {
	payout := roundMoney(gross - commission - refunds)
	return map[string]any{
		"restaurant_id": restaurantID,
		"orders":        orders,
		"delivered":     delivered,
		"refunded":      refunded,
		"gross_amount":  gross,
		"commission":    commission,
		"refunds":       refunds,
		"payout_amount": payout,

		"gross_amount_paise":  PaiseOf(gross),
		"commission_paise":    PaiseOf(commission),
		"refunds_paise":       PaiseOf(refunds),
		"payout_amount_paise": PaiseOf(payout),
	}
}

// PartnerSettlementRow is one food.restaurant_settlements row as the partner
// settlements list reads it.
type PartnerSettlementRow struct {
	ID, PeriodStart, PeriodEnd, Status, PaidReference, PaidAt, CreatedAt string
	Gross, Commission, RefundAdjustment, Penalty, Payout                  float64
}

// PartnerSettlementMap is one item of GET .../settlements.
func PartnerSettlementMap(restaurantID uuid.UUID, r PartnerSettlementRow) map[string]any {
	return map[string]any{
		"id": r.ID, "restaurant_id": restaurantID, "period_start": r.PeriodStart, "period_end": r.PeriodEnd,
		"gross_amount": r.Gross, "commission": r.Commission, "refund_adjustment": r.RefundAdjustment,
		"penalty_amount": r.Penalty, "payout_amount": r.Payout, "status": r.Status,
		"paid_reference": r.PaidReference, "paid_at": r.PaidAt, "created_at": r.CreatedAt,

		"gross_amount_paise":      PaiseOf(r.Gross),
		"commission_paise":        PaiseOf(r.Commission),
		"refund_adjustment_paise": PaiseOf(r.RefundAdjustment),
		"penalty_amount_paise":    PaiseOf(r.Penalty),
		"payout_amount_paise":     PaiseOf(r.Payout),
	}
}

// ─── Onboarding read-backs ──────────────────────────────────────────────────

// StepNotSavedError is a read-back of an onboarding step the owner has not
// saved yet: HTTP 404 FOOD_ONBOARDING_STEP_NOT_SAVED. A restaurant the caller
// does not own is pgx.ErrNoRows (404 FOOD_NOT_FOUND) before this is checked.
type StepNotSavedError struct {
	Step string
}

func (e *StepNotSavedError) Error() string {
	return "onboarding step " + e.Step + " has not been saved"
}

// RestaurantReadiness is GET .../readiness. Missing uses the submit 422's
// vocabulary and comes from the same restaurantMissingSteps call.
type RestaurantReadiness struct {
	RestaurantID uuid.UUID `json:"restaurant_id"`
	Ready        bool      `json:"ready"`
	Missing      []string  `json:"missing"`
	Status       string    `json:"status"`
	// CanSubmit: ready, and in a status submit accepts (DRAFT or REJECTED).
	CanSubmit bool `json:"can_submit"`
}

func (s *Store) GetRestaurantReadiness(ctx context.Context, ownerID, restaurantID uuid.UUID) (*RestaurantReadiness, error) {
	var status string
	if err := s.db.QueryRow(ctx, `
		SELECT status::text FROM food.restaurants WHERE id = $1 AND owner_user_id = $2
	`, restaurantID, ownerID).Scan(&status); err != nil {
		return nil, err
	}
	missing, err := restaurantMissingSteps(ctx, s.db, restaurantID)
	if err != nil {
		return nil, err
	}
	ready := len(missing) == 0
	return &RestaurantReadiness{
		RestaurantID: restaurantID, Ready: ready, Missing: missing, Status: status,
		CanSubmit: ready && (status == "DRAFT" || status == "REJECTED"),
	}, nil
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// GetRestaurantCompliance reads back the saved compliance form. It selects
// the masked PAN only; the sealed blob and lookup hash never leave the table.
func (s *Store) GetRestaurantCompliance(ctx context.Context, ownerID, restaurantID uuid.UUID) (*RestaurantCompliance, error) {
	var out RestaurantCompliance
	var taxCategory, legalName, panMasked, holderType *string
	var submitted *time.Time
	if err := s.db.QueryRow(ctx, `
		SELECT id, tax_category, legal_name, gstin, gstin_state_code, pan_masked, pan_holder_type,
			specified_premises_declared_at::text, compliance_submitted_at
		FROM food.restaurants
		WHERE id = $1 AND owner_user_id = $2
	`, restaurantID, ownerID).Scan(&out.RestaurantID, &taxCategory, &legalName, &out.GSTIN, &out.GSTINStateCode,
		&panMasked, &holderType, &out.SpecifiedPremisesDeclaredAt, &submitted); err != nil {
		return nil, err
	}
	if submitted == nil || panMasked == nil {
		return nil, &StepNotSavedError{Step: onboarding.StepCompliance}
	}
	out.TaxCategory, out.LegalName, out.PANMasked, out.PANHolderType = derefString(taxCategory), derefString(legalName), *panMasked, derefString(holderType)
	out.ComplianceSubmittedAt = rfc3339(*submitted)
	return &out, nil
}

// GetRestaurantLocation reads back the pin, the address and the active
// delivery radius. "Saved" is what readiness calls a location: coordinates
// and an active service area.
func (s *Store) GetRestaurantLocation(ctx context.Context, ownerID, restaurantID uuid.UUID) (*RestaurantLocation, error) {
	var out RestaurantLocation
	var lat, lng, radius *float64
	var areaID *uuid.UUID
	if err := s.db.QueryRow(ctx, `
		SELECT r.id, r.latitude::float8, r.longitude::float8, COALESCE(r.address_line1, ''), COALESCE(r.address_line2, ''),
			COALESCE(r.city, ''), COALESCE(r.state, ''), COALESCE(r.postal_code, ''), COALESCE(r.google_place_id, ''),
			a.radius_km::float8, a.id
		FROM food.restaurants r
		LEFT JOIN LATERAL (
			SELECT sa.id, sa.radius_km FROM food.restaurant_service_areas sa
			WHERE sa.restaurant_id = r.id AND sa.is_active
			ORDER BY sa.created_at DESC, sa.id
			LIMIT 1
		) a ON TRUE
		WHERE r.id = $1 AND r.owner_user_id = $2
	`, restaurantID, ownerID).Scan(&out.RestaurantID, &lat, &lng, &out.AddressLine1, &out.AddressLine2,
		&out.City, &out.State, &out.PostalCode, &out.GooglePlaceID, &radius, &areaID); err != nil {
		return nil, err
	}
	if lat == nil || lng == nil || areaID == nil || radius == nil {
		return nil, &StepNotSavedError{Step: onboarding.StepLocation}
	}
	out.Latitude, out.Longitude, out.DeliveryRadiusKM, out.ServiceAreaID = *lat, *lng, *radius, *areaID
	return &out, nil
}

// GetOperatingHours reads back the schedule in the order the PUT returns it.
func (s *Store) GetOperatingHours(ctx context.Context, ownerID, restaurantID uuid.UUID) (*OperatingHours, error) {
	if err := s.requireRestaurantOwner(ctx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	windows, err := loadHours(ctx, s.db, restaurantID)
	if err != nil {
		return nil, err
	}
	if len(windows) == 0 {
		return nil, &StepNotSavedError{Step: onboarding.StepOperatingHours}
	}
	sort.SliceStable(windows, func(i, j int) bool {
		a, b := windows[i], windows[j]
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		if a.Closed != b.Closed {
			return a.Closed
		}
		return a.Opens < b.Opens
	})
	loc := s.ordering.Location
	return &OperatingHours{
		RestaurantID: restaurantID,
		Timezone:     loc.String(),
		IsOpenNow:    openAt(s.ordering.Now().In(loc), windows),
		Windows:      hoursView(windows),
	}, nil
}

// RestaurantFSSAIView is GET .../fssai: the licence mirrored on the
// restaurant and the latest FSSAI document with its review outcome.
type RestaurantFSSAIView struct {
	RestaurantID   uuid.UUID           `json:"restaurant_id"`
	LicenceNumber  string              `json:"fssai_licence_number"`
	ExpiresAt      *string             `json:"fssai_expires_at"`
	Document       *RestaurantDocument `json:"document"`
	DocumentStatus *string             `json:"document_status"`
	ReviewReason   *string             `json:"review_reason"`
}

func (s *Store) GetRestaurantFSSAI(ctx context.Context, ownerID, restaurantID uuid.UUID) (*RestaurantFSSAIView, error) {
	out := RestaurantFSSAIView{RestaurantID: restaurantID}
	var licence *string
	if err := s.db.QueryRow(ctx, `
		SELECT fssai_licence_number, fssai_expires_at::text
		FROM food.restaurants
		WHERE id = $1 AND owner_user_id = $2
	`, restaurantID, ownerID).Scan(&licence, &out.ExpiresAt); err != nil {
		return nil, err
	}
	out.LicenceNumber = derefString(licence)
	var docID uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT id FROM food.restaurant_documents
		WHERE restaurant_id = $1 AND document_type = 'FSSAI'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, restaurantID).Scan(&docID)
	switch {
	case err == nil:
		doc, err := getRestaurantDocument(ctx, s.db, restaurantID, docID)
		if err != nil {
			return nil, err
		}
		status := doc.Status
		out.Document, out.DocumentStatus, out.ReviewReason = doc, &status, doc.RejectionReason
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	if licence == nil && out.Document == nil {
		return nil, &StepNotSavedError{Step: onboarding.StepFSSAI}
	}
	return &out, nil
}

// ─── Partner restaurant ─────────────────────────────────────────────────────

// partnerRestaurantSelect is the one column list scanPartnerRestaurant reads.
const partnerRestaurantSelect = `
	SELECT r.id, r.partner_id, r.owner_user_id, r.name, r.slug, COALESCE(r.description, ''),
		r.status::text, r.is_open, r.is_accepting_orders, r.city, COALESCE(r.state, ''),
		r.min_order_amount::float8, r.packaging_fee::float8, r.created_at::text,
		NULLIF(r.phone, ''), NULLIF(r.email, ''), NULLIF(p.legal_name, ''), NULLIF(p.display_name, '')
	FROM food.restaurants r
	JOIN food.restaurant_partners p ON p.id = r.partner_id`

// patchPartnerRestaurant writes only the keys in.Present names (every key
// when it is nil). name, slug, legal_name and the two amounts cannot be
// cleared; a null slug is re-derived from the name, as an empty one always was.
func (s *Store) patchPartnerRestaurant(ctx context.Context, ownerID, restaurantID uuid.UUID, in PartnerRestaurantInput) (*PartnerRestaurant, error) {
	full := in.Present == nil
	present := func(k string) bool { return full || in.Present[k] }
	isNull := func(k string) bool { return !full && in.Null[k] }
	if present("name") && (isNull("name") || strings.TrimSpace(in.Name) == "") {
		return nil, fmt.Errorf("name is required")
	}
	for _, k := range []string{"min_order_amount", "packaging_fee"} {
		if present(k) && isNull(k) {
			return nil, fmt.Errorf("%s cannot be cleared", k)
		}
	}
	if !full && in.Present["legal_name"] && (in.Null["legal_name"] || strings.TrimSpace(in.LegalName) == "") {
		return nil, fmt.Errorf("legal_name cannot be cleared")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var currentName string
	var partnerID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT name, partner_id FROM food.restaurants
		WHERE id = $1 AND owner_user_id = $2
		FOR UPDATE
	`, restaurantID, ownerID).Scan(&currentName, &partnerID); err != nil {
		return nil, err
	}

	args := []any{restaurantID}
	var sets []string
	set := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	text := func(k, v string) any {
		if isNull(k) {
			return nil
		}
		return v
	}
	name := currentName
	if present("name") {
		name = in.Name
		set("name", in.Name)
	}
	if present("slug") {
		slug := strings.TrimSpace(in.Slug)
		if isNull("slug") || slug == "" {
			slug = strings.ToLower(strings.ReplaceAll(name, " ", "-"))
		}
		set("slug", slug)
	}
	if present("description") {
		set("description", text("description", in.Description))
	}
	if present("phone") {
		set("phone", text("phone", in.Phone))
	}
	if present("email") {
		set("email", text("email", in.Email))
	}
	if present("min_order_amount") {
		set("min_order_amount", in.MinOrderAmount)
	}
	if present("packaging_fee") {
		set("packaging_fee", in.PackagingFee)
	}

	partnerArgs := []any{partnerID}
	var partnerSets []string
	if !full {
		if in.Present["legal_name"] {
			partnerArgs = append(partnerArgs, strings.TrimSpace(in.LegalName))
			partnerSets = append(partnerSets, fmt.Sprintf("legal_name = $%d", len(partnerArgs)))
		}
		if in.Present["display_name"] {
			var v any
			if !in.Null["display_name"] && strings.TrimSpace(in.DisplayName) != "" {
				v = in.DisplayName
			}
			partnerArgs = append(partnerArgs, v)
			partnerSets = append(partnerSets, fmt.Sprintf("display_name = $%d", len(partnerArgs)))
		}
	}
	if len(sets) == 0 && len(partnerSets) == 0 {
		return s.GetPartnerRestaurant(ctx, ownerID, restaurantID)
	}

	// Any profile change on a live restaurant sends it back to review, as the
	// full replace always did.
	sets = append(sets,
		"status = CASE WHEN status = 'ACTIVE' THEN 'PENDING_REVIEW' ELSE status END",
		"is_accepting_orders = CASE WHEN status = 'ACTIVE' THEN FALSE ELSE is_accepting_orders END")
	if _, err := tx.Exec(ctx, "UPDATE food.restaurants SET "+strings.Join(sets, ", ")+" WHERE id = $1", args...); err != nil {
		return nil, err
	}
	if len(partnerSets) > 0 {
		if _, err := tx.Exec(ctx, "UPDATE food.restaurant_partners SET "+strings.Join(partnerSets, ", ")+" WHERE id = $1", partnerArgs...); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetPartnerRestaurant(ctx, ownerID, restaurantID)
}

// ─── Partner single reads ───────────────────────────────────────────────────

// GetPartnerOrder is one order of a restaurant the caller owns, with items,
// history and the money block. Another restaurant's order is pgx.ErrNoRows.
// The customer's drop-off code is never included.
func (s *Store) GetPartnerOrder(ctx context.Context, ownerID, orderID uuid.UUID) (*Order, error) {
	var userID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		SELECT o.user_id
		FROM food.orders o
		JOIN food.restaurants r ON r.id = o.restaurant_id
		WHERE o.id = $1 AND r.owner_user_id = $2
	`, orderID, ownerID).Scan(&userID); err != nil {
		return nil, err
	}
	o, err := s.getOrder(ctx, s.db, userID, orderID, true)
	if err != nil {
		return nil, err
	}
	o.DeliveryCode = ""
	return o, nil
}

// partnerMenuCategories lists every active category of the restaurant with
// its active items. The LEFT JOIN keeps a category that has no items yet.
func (s *Store) partnerMenuCategories(ctx context.Context, restaurantID uuid.UUID) ([]MenuCategory, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.name, COALESCE(c.description, ''), c.sort_order,
			COUNT(i.id) OVER (PARTITION BY c.id)::int,
			i.id, i.restaurant_id, i.name, COALESCE(i.description, ''), i.food_type::text,
			i.base_price::float8, COALESCE(i.discount_price, 0)::float8, COALESCE(i.image_url, ''),
			i.preparation_minutes, i.is_available, i.is_recommended, i.tax_percentage::float8, i.media_id
		FROM food.menu_categories c
		LEFT JOIN food.menu_items i ON i.category_id = c.id AND i.is_active = TRUE
		WHERE c.restaurant_id = $1 AND c.is_active = TRUE
		ORDER BY c.sort_order, c.name, c.id, i.is_recommended DESC, i.name, i.id
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[uuid.UUID]int{}
	var categories []MenuCategory
	for rows.Next() {
		var cat MenuCategory
		var count int
		var itemID, itemRestaurant *uuid.UUID
		var name, foodType *string
		var description, imageURL string
		var basePrice, taxPercentage *float64
		var discountPrice float64
		var prepMinutes *int
		var available, recommended *bool
		var mediaID *uuid.UUID
		if err := rows.Scan(&cat.ID, &cat.Name, &cat.Description, &cat.SortOrder, &count,
			&itemID, &itemRestaurant, &name, &description, &foodType,
			&basePrice, &discountPrice, &imageURL, &prepMinutes, &available, &recommended, &taxPercentage, &mediaID); err != nil {
			return nil, err
		}
		idx, ok := byID[cat.ID]
		if !ok {
			cat.Items = []MenuItem{}
			c := count
			cat.ItemCount = &c
			categories = append(categories, cat)
			idx = len(categories) - 1
			byID[cat.ID] = idx
		}
		if itemID == nil {
			continue
		}
		item := MenuItem{
			ID: *itemID, RestaurantID: *itemRestaurant, CategoryID: cat.ID, Name: derefString(name), Description: description,
			FoodType: derefString(foodType), DiscountPrice: discountPrice, ImageURL: imageURL, ImageMediaID: mediaID,
		}
		if basePrice != nil {
			item.BasePrice = *basePrice
		}
		if taxPercentage != nil {
			item.TaxPercentage = *taxPercentage
		}
		if prepMinutes != nil {
			item.PreparationMinutes = *prepMinutes
		}
		item.IsAvailable = available != nil && *available
		item.IsRecommended = recommended != nil && *recommended
		item.FillPaise()
		categories[idx].Items = append(categories[idx].Items, item)
	}
	return categories, rows.Err()
}

// ─── Pre-accept ETA refresh (B6 follow-up) ──────────────────────────────────

// preAcceptETAStatuses are the order statuses in which no rider ping drives
// the ETA yet (the claim also requires that no rider has accepted).
var preAcceptETAStatuses = []string{"PLACED", "PAYMENT_PENDING", "CONFIRMED", "PREPARING", "READY_FOR_PICKUP", "DELIVERY_ASSIGNING", "DELIVERY_ASSIGNED"}

// claimPreAcceptETA takes the same per-order recompute slot the rider ping
// claims (eta_computed_at, ETARecomputeInterval), for an order of this
// customer that no rider has accepted. Nil when not eligible or the slot is
// taken.
func claimPreAcceptETA(ctx context.Context, q rowQuerier, userID, orderID uuid.UUID) (*ETAJob, error) {
	job := ETAJob{OrderID: orderID}
	err := q.QueryRow(ctx, `
		UPDATE food.orders o
		SET eta_computed_at = NOW()
		WHERE o.id = $1 AND o.user_id = $2
			AND o.status::text = ANY($3::text[])
			AND NOT EXISTS (
				SELECT 1 FROM food.delivery_assignments da
				WHERE da.order_id = o.id
					AND da.status IN ('ACCEPTED', 'ARRIVED_AT_RESTAURANT', 'PICKED_UP', 'ARRIVED_AT_CUSTOMER'))
			AND (o.eta_computed_at IS NULL
				OR o.eta_computed_at <= NOW() - make_interval(secs => $4::float8))
		RETURNING o.eta_computed_at, o.status::text
	`, orderID, userID, preAcceptETAStatuses, ETARecomputeInterval.Seconds()).Scan(&job.ClaimedAt, &job.OrderStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim pre-accept eta: %w", err)
	}
	if err := loadETAInputs(ctx, q, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// preAcceptETA = max(now, food ready) + ride(restaurant -> customer): the
// placement formula moved forward to now, so the estimate never sits in the
// past while the kitchen runs late.
func (s *Store) preAcceptETA(ctx context.Context, job ETAJob) (time.Time, string, bool) {
	if job.Restaurant == nil || job.Customer == nil {
		return time.Time{}, "", false
	}
	pickupAt := s.ordering.Now()
	if job.FoodReadyAt != nil && job.FoodReadyAt.After(pickupAt) {
		pickupAt = *job.FoodReadyAt
	}
	var ride routing.Route
	var err error = fmt.Errorf("no router")
	if s.router != nil {
		ride, err = s.router.Route(ctx, *job.Restaurant, *job.Customer)
	}
	if err != nil || !validETASource(ride.Source) || ride.Duration < 0 {
		ride, err = s.ordering.Haversine().Route(ctx, *job.Restaurant, *job.Customer)
		if err != nil || !validETASource(ride.Source) {
			return time.Time{}, "", false
		}
	}
	return pickupAt.Add(time.Duration(rideSeconds(ride.Duration)) * time.Second), ride.Source, true
}

// refreshPreAcceptETA is best effort: the order detail read never fails on it.
func (s *Store) refreshPreAcceptETA(ctx context.Context, userID, orderID uuid.UUID) {
	job, err := claimPreAcceptETA(ctx, s.db, userID, orderID)
	if err != nil {
		slog.WarnContext(ctx, "food-service: pre-accept eta claim failed", "order_id", orderID, "error", err)
		return
	}
	if job == nil {
		return
	}
	eta, source, ok := s.preAcceptETA(ctx, *job)
	if !ok {
		return
	}
	if _, err := s.RecordOrderETA(ctx, orderID, job.ClaimedAt, eta, source); err != nil {
		slog.WarnContext(ctx, "food-service: pre-accept eta write failed", "order_id", orderID, "error", err)
	}
}
