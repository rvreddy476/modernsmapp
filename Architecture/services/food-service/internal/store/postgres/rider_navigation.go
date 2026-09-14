package postgres

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/food-service/internal/geo"
	"github.com/google/uuid"
)

// Rider navigation and money. Every change is additive: no existing key is
// renamed, retyped or removed. What a Feast rider sees, and when:
//
//   - an offer names the restaurant, a drop area snapped to a 0.005 degree
//     (~550 m) grid with its city, the distances and the payout; never the
//     address, a name or a phone;
//   - an assignment names the restaurant (with its business phone when stored)
//     and, only while DropVisible, the exact drop-off with the receiver's first
//     name and the delivery instructions; a closed job keeps the city only;
//   - money carries integer paise computed from the NUMERIC columns in SQL,
//     never through a float.
//
// No masked or relay number exists for calling the customer, so none is sent.

// RiderCurrency is the currency of every rider amount.
const RiderCurrency = "INR"

// dropAreaCellsPerDegree snaps a drop pin to a 0.005 degree grid (~550 m of
// latitude): an offer reaches several nearby riders before anyone accepts, so
// its drop area must not point at a building in a dense city.
const dropAreaCellsPerDegree = 200

// RoundDropCoordinate is a drop pin coordinate as an offer may show it: the
// nearest multiple of 0.005 degrees. The cell index times 5 is an exact
// integer, so dividing by 1000 gives a float whose JSON form has at most 3
// decimals (12.98, 12.975, 77.64). The one rounding every offer payload uses.
func RoundDropCoordinate(v float64) float64 {
	return math.Round(v*dropAreaCellsPerDegree) * 5 / 1000
}

// MapsDirectionsURL is a Google Maps two-wheeler directions deep link.
func MapsDirectionsURL(lat, lng float64) string {
	return "https://www.google.com/maps/dir/?api=1&destination=" +
		strconv.FormatFloat(lat, 'f', -1, 64) + "," + strconv.FormatFloat(lng, 'f', -1, 64) +
		"&travelmode=two-wheeler"
}

// validPin: both coordinates present, in range, and not the 0,0 an address
// saved without a map location snapshots to.
func validPin(lat, lng *float64) bool {
	if lat == nil || lng == nil || (*lat == 0 && *lng == 0) {
		return false
	}
	return *lat >= -90 && *lat <= 90 && *lng >= -180 && *lng <= 180
}

// metersBetween is the straight-line (haversine) distance in whole metres.
func metersBetween(lat1, lng1, lat2, lng2 float64) int64 {
	return int64(math.Round(geo.HaversineKM(lat1, lng1, lat2, lng2) * 1000))
}

// ─── Offers ─────────────────────────────────────────────────────────────────

// OfferRestaurant is the pickup point of an offer.
type OfferRestaurant struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Latitude     *float64  `json:"latitude,omitempty"`
	Longitude    *float64  `json:"longitude,omitempty"`
	AddressLine1 string    `json:"address_line1"`
	City         string    `json:"city"`
}

// DropArea is where an offer goes, rounded; locality is the city (addresses
// have no finer locality field).
type DropArea struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Locality  string  `json:"locality"`
}

// DeliveryOfferView is an offer as a rider sees it (inbox, realtime frame and
// food.delivery.offered): the offer row's keys unchanged, plus the job detail.
type DeliveryOfferView struct {
	*DeliveryOffer
	Restaurant *OfferRestaurant `json:"restaurant,omitempty"`
	DropArea   *DropArea        `json:"drop_area,omitempty"`
	// DistanceToRestaurantMeters is from the rider's last ping, only if fresh.
	DistanceToRestaurantMeters *int64 `json:"distance_to_restaurant_meters,omitempty"`
	// TripDistanceMeters is restaurant to drop, straight line.
	TripDistanceMeters *int64 `json:"trip_distance_meters,omitempty"`
	// PayoutPaise is what the rider earns for this delivery (a batch: all of it).
	PayoutPaise *int64 `json:"payout_paise,omitempty"`
	Currency    string `json:"currency,omitempty"`
}

// DeliveryOfferContext is what DeliveryOfferContexts reads for one offer. It
// holds no address line, name or phone. The drop pin is exact so the trip
// distance is honest; BuildDeliveryOfferView rounds it before anything leaves.
type DeliveryOfferContext struct {
	OfferID                uuid.UUID
	RestaurantID           uuid.UUID
	RestaurantName         string
	RestaurantLat          *float64
	RestaurantLng          *float64
	RestaurantAddressLine1 string
	RestaurantCity         string
	DropLat                *float64 `json:"-"`
	DropLng                *float64 `json:"-"`
	DropCity               string
	// PayoutPaise is ROUND(delivery_partner_payout * 100), the figure delivery
	// settlement pays; for a batch offer, the sum over its members.
	PayoutPaise int64
	// The rider's latest location ping, if any.
	RiderLat        *float64
	RiderLng        *float64
	RiderRecordedAt *time.Time
}

// BuildDeliveryOfferView adds the job detail to an offer. A nil context gives
// exactly the old offer. The rider's distance to the restaurant is included
// only when their last ping is no older than maxLocationAge at now.
func BuildDeliveryOfferView(o DeliveryOffer, c *DeliveryOfferContext, now time.Time, maxLocationAge time.Duration) DeliveryOfferView {
	offer := o
	v := DeliveryOfferView{DeliveryOffer: &offer}
	if c == nil {
		return v
	}
	v.Restaurant = &OfferRestaurant{ID: c.RestaurantID, Name: c.RestaurantName,
		AddressLine1: c.RestaurantAddressLine1, City: c.RestaurantCity}
	restaurantPinned := validPin(c.RestaurantLat, c.RestaurantLng)
	if restaurantPinned {
		lat, lng := *c.RestaurantLat, *c.RestaurantLng
		v.Restaurant.Latitude, v.Restaurant.Longitude = &lat, &lng
	}
	if validPin(c.DropLat, c.DropLng) {
		v.DropArea = &DropArea{Latitude: RoundDropCoordinate(*c.DropLat), Longitude: RoundDropCoordinate(*c.DropLng), Locality: c.DropCity}
		if restaurantPinned {
			trip := metersBetween(*c.RestaurantLat, *c.RestaurantLng, *c.DropLat, *c.DropLng)
			v.TripDistanceMeters = &trip
		}
	}
	if restaurantPinned && validPin(c.RiderLat, c.RiderLng) && c.RiderRecordedAt != nil && now.Sub(*c.RiderRecordedAt) <= maxLocationAge {
		d := metersBetween(*c.RiderLat, *c.RiderLng, *c.RestaurantLat, *c.RestaurantLng)
		v.DistanceToRestaurantMeters = &d
	}
	payout := c.PayoutPaise
	v.PayoutPaise, v.Currency = &payout, RiderCurrency
	return v
}

// DeliveryOfferContexts reads the job detail of each offer in one query. The
// delivery snapshot contributes its pin and city only.
func (s *Store) DeliveryOfferContexts(ctx context.Context, offerIDs []uuid.UUID) (map[uuid.UUID]DeliveryOfferContext, error) {
	out := make(map[uuid.UUID]DeliveryOfferContext, len(offerIDs))
	if len(offerIDs) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(offerIDs))
	for _, id := range offerIDs {
		ids = append(ids, id.String())
	}
	rows, err := s.db.Query(ctx, `
		SELECT off.id, o.restaurant_id, o.restaurant_name_snapshot,
			r.latitude::float8, r.longitude::float8, COALESCE(r.address_line1, ''), COALESCE(r.city, ''),
			CASE WHEN jsonb_typeof(o.delivery_address_snapshot->'latitude') = 'number'
				THEN (o.delivery_address_snapshot->>'latitude')::float8 END,
			CASE WHEN jsonb_typeof(o.delivery_address_snapshot->'longitude') = 'number'
				THEN (o.delivery_address_snapshot->>'longitude')::float8 END,
			COALESCE(o.delivery_address_snapshot->>'city', ''),
			CASE WHEN off.batch_id IS NULL
				THEN COALESCE(ROUND(da.delivery_partner_payout * 100), 0)::bigint
				ELSE (SELECT COALESCE(SUM(ROUND(m.delivery_partner_payout * 100)), 0)::bigint
					FROM food.delivery_assignments m WHERE m.batch_id = off.batch_id)
			END,
			loc.latitude::float8, loc.longitude::float8, loc.recorded_at
		FROM food.delivery_offers off
		JOIN food.orders o ON o.id = off.order_id
		JOIN food.restaurants r ON r.id = o.restaurant_id
		LEFT JOIN food.delivery_assignments da ON da.order_id = off.order_id
		LEFT JOIN LATERAL (
			SELECT l.latitude, l.longitude, l.recorded_at
			FROM food.delivery_partner_locations l
			WHERE l.delivery_partner_id = off.delivery_partner_id
			ORDER BY l.recorded_at DESC
			LIMIT 1
		) loc ON TRUE
		WHERE off.id = ANY($1::text[]::uuid[])
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c DeliveryOfferContext
		if err := rows.Scan(&c.OfferID, &c.RestaurantID, &c.RestaurantName,
			&c.RestaurantLat, &c.RestaurantLng, &c.RestaurantAddressLine1, &c.RestaurantCity,
			&c.DropLat, &c.DropLng, &c.DropCity, &c.PayoutPaise,
			&c.RiderLat, &c.RiderLng, &c.RiderRecordedAt); err != nil {
			return nil, err
		}
		out[c.OfferID] = c
	}
	return out, rows.Err()
}

// ─── Assignments ────────────────────────────────────────────────────────────

// AssignmentRestaurant is the pickup point of an assignment. Phone is the
// restaurant's business number, only when stored.
type AssignmentRestaurant struct {
	Name         string   `json:"name"`
	Latitude     *float64 `json:"latitude,omitempty"`
	Longitude    *float64 `json:"longitude,omitempty"`
	AddressLine1 string   `json:"address_line1"`
	AddressLine2 string   `json:"address_line2"`
	City         string   `json:"city"`
	Phone        string   `json:"phone,omitempty"`
}

// AssignmentDrop is the exact drop-off, only while DropVisible. The customer
// is named by the receiver's first name only; their phone is never included.
type AssignmentDrop struct {
	Latitude             *float64 `json:"latitude,omitempty"`
	Longitude            *float64 `json:"longitude,omitempty"`
	AddressLine1         string   `json:"address_line1"`
	AddressLine2         string   `json:"address_line2"`
	Landmark             string   `json:"landmark"`
	City                 string   `json:"city"`
	PostalCode           string   `json:"postal_code"`
	CustomerFirstName    string   `json:"customer_first_name"`
	DeliveryInstructions string   `json:"delivery_instructions"`
}

// DropSummary is all a closed job (delivered, failed, cancelled, released)
// keeps of where it went.
type DropSummary struct {
	City     string `json:"city"`
	Locality string `json:"locality"`
}

// AssignmentNavigation holds Google Maps two-wheeler deep links. DropURL only
// when the drop is shown; neither on a closed job.
type AssignmentNavigation struct {
	PickupURL string `json:"pickup_url,omitempty"`
	DropURL   string `json:"drop_url,omitempty"`
}

// AssignmentPlaces is what deliveryAssignmentSelect reads beyond the original
// columns. The raw delivery snapshot stays inside the store; FillRiderView
// copies out only what the visibility rules allow.
type AssignmentPlaces struct {
	RestaurantLat          *float64
	RestaurantLng          *float64
	RestaurantAddressLine1 string
	RestaurantAddressLine2 string
	RestaurantCity         string
	RestaurantPhone        string
	DeliverySnapshot       []byte
	CustomerInstruction    string
	ETAAt                  *time.Time
	ETASource              string
}

// deliveryAssignmentSelect is the one column list scanDeliveryAssignment reads:
// the original twelve columns, the two paise siblings computed from NUMERIC in
// SQL, then AssignmentPlaces.
const deliveryAssignmentSelect = `
	SELECT da.id, da.order_id, o.order_number, o.restaurant_name_snapshot,
		o.restaurant_id, da.delivery_partner_id, da.status::text, o.status::text,
		da.delivery_fee::float8, da.delivery_partner_payout::float8, da.created_at::text,
		COALESCE(da.pickup_code, ''),
		ROUND(da.delivery_fee * 100)::bigint, ROUND(da.delivery_partner_payout * 100)::bigint,
		r.latitude::float8, r.longitude::float8, COALESCE(r.address_line1, ''), COALESCE(r.address_line2, ''),
		COALESCE(r.city, ''), COALESCE(r.phone, ''),
		o.delivery_address_snapshot, COALESCE(o.customer_instruction, ''),
		o.eta_at, COALESCE(o.eta_source, '')
	FROM food.delivery_assignments da
	JOIN food.orders o ON o.id = da.order_id
	JOIN food.restaurants r ON r.id = o.restaurant_id`

// assignmentClosed: the job is over for the rider.
func assignmentClosed(status string) bool {
	switch status {
	case "DELIVERED", "FAILED", "CANCELLED", "REJECTED":
		return true
	}
	return false
}

// dropSnapshot is the part of a delivery_address_snapshot a rider view may
// use. The phone is never read.
type dropSnapshot struct {
	receiverName, addressLine1, addressLine2, landmark, city, postalCode string
	lat, lng                                                             *float64
}

func readDropSnapshot(raw []byte) dropSnapshot {
	var m map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return dropSnapshot{}
	}
	text := func(k string) string {
		v, _ := m[k].(string)
		return strings.TrimSpace(v)
	}
	number := func(k string) *float64 {
		v, ok := jsonNumber(m[k])
		if !ok {
			return nil
		}
		return &v
	}
	return dropSnapshot{
		receiverName: text("receiver_name"), addressLine1: text("address_line1"), addressLine2: text("address_line2"),
		landmark: text("landmark"), city: text("city"), postalCode: text("postal_code"),
		lat: number("latitude"), lng: number("longitude"),
	}
}

func firstName(full string) string {
	if parts := strings.Fields(full); len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// FillRiderView sets the rider-facing detail of an assignment from its places:
// payout, restaurant, the drop (only while DropVisible) or a city-only summary
// once closed, the order's ETA while ETAVisible, and navigation on open jobs.
// The paise siblings must already be set.
func (a *DeliveryAssignment) FillRiderView(p AssignmentPlaces) {
	a.PayoutPaise, a.Currency = a.DeliveryPartnerPayoutPaise, RiderCurrency
	a.Drop, a.DropSummary, a.Navigation, a.ETAAt, a.ETASource = nil, nil, nil, "", ""

	r := &AssignmentRestaurant{Name: a.RestaurantName, AddressLine1: p.RestaurantAddressLine1,
		AddressLine2: p.RestaurantAddressLine2, City: p.RestaurantCity, Phone: strings.TrimSpace(p.RestaurantPhone)}
	if validPin(p.RestaurantLat, p.RestaurantLng) {
		lat, lng := *p.RestaurantLat, *p.RestaurantLng
		r.Latitude, r.Longitude = &lat, &lng
	}
	a.Restaurant = r

	snap := readDropSnapshot(p.DeliverySnapshot)
	switch {
	case DropVisible(a.Status, a.OrderStatus):
		d := &AssignmentDrop{AddressLine1: snap.addressLine1, AddressLine2: snap.addressLine2, Landmark: snap.landmark,
			City: snap.city, PostalCode: snap.postalCode, CustomerFirstName: firstName(snap.receiverName),
			DeliveryInstructions: strings.TrimSpace(p.CustomerInstruction)}
		if validPin(snap.lat, snap.lng) {
			d.Latitude, d.Longitude = snap.lat, snap.lng
		}
		a.Drop = d
	case assignmentClosed(a.Status) && snap.city != "":
		a.DropSummary = &DropSummary{City: snap.city, Locality: snap.city}
	}

	if p.ETAAt != nil && validETASource(p.ETASource) && ETAVisible(a.OrderStatus) {
		a.ETAAt, a.ETASource = FormatETA(*p.ETAAt), p.ETASource
	}

	if assignmentClosed(a.Status) {
		return
	}
	var nav AssignmentNavigation
	if r.Latitude != nil {
		nav.PickupURL = MapsDirectionsURL(*r.Latitude, *r.Longitude)
	}
	if a.Drop != nil && a.Drop.Latitude != nil {
		nav.DropURL = MapsDirectionsURL(*a.Drop.Latitude, *a.Drop.Longitude)
	}
	if nav != (AssignmentNavigation{}) {
		a.Navigation = &nav
	}
}

// ─── Earnings ───────────────────────────────────────────────────────────────

// DeliveryEarningsMap is GET /delivery/earnings: the float rupee keys as
// before, plus paise siblings summed from NUMERIC in SQL.
func DeliveryEarningsMap(todayCount int, todayAmount float64, todayPaise int64, totalCount int, totalAmount float64, totalPaise int64) map[string]any {
	return map[string]any{
		"deliveries_today": todayCount,
		"earnings_today":   todayAmount,
		"total_deliveries": totalCount,
		"total_earnings":   totalAmount,

		"earnings_today_paise": todayPaise,
		"total_earnings_paise": totalPaise,
		"currency":             RiderCurrency,
	}
}
