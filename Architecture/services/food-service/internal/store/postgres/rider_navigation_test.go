package postgres

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/geo"
	"github.com/atpost/food-service/internal/orderstate"
	"github.com/google/uuid"
)

// rnDropSnapshot is a delivery_address_snapshot exactly as PlaceOrder writes
// it, including the receiver's full name and phone that no rider may see.
const rnDropSnapshot = `{"id":"0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0099","label":"Home","receiver_name":"Asha Rao","phone":"+919812345678",` +
	`"address_line1":"42 Lake View Road","address_line2":"Flat 3B","landmark":"Opp. City Park","city":"Bengaluru",` +
	`"state":"Karnataka","country":"India","postal_code":"560038","latitude":12.978449,"longitude":77.640812}`

// rnDropOnly is every piece of the drop-off that only an accepted job shows.
var rnDropOnly = []string{"42 Lake View Road", "Flat 3B", "Opp. City Park", "560038", "Asha", "Ring the bell twice", "12.978449", "77.640812"}

// rnNever is what no rider response or offer carries at any stage.
var rnNever = []string{"Rao", "9812345678", "receiver_name"}

var rnOpenAssignment = map[string]bool{"ACCEPTED": true, "ARRIVED_AT_RESTAURANT": true, "PICKED_UP": true, "ARRIVED_AT_CUSTOMER": true}

var rnClosedAssignment = map[string]bool{"DELIVERED": true, "FAILED": true, "CANCELLED": true, "REJECTED": true}

var rnOnTheWay = map[string]bool{orderstate.DeliveryAssigned: true, orderstate.PickedUp: true, orderstate.OutForDelivery: true}

const (
	rnPickupURL = "https://www.google.com/maps/dir/?api=1&destination=12.9716,77.5946&travelmode=two-wheeler"
	rnDropURL   = "https://www.google.com/maps/dir/?api=1&destination=12.978449,77.640812&travelmode=two-wheeler"
)

func rnPlaces() AssignmentPlaces {
	eta := time.Date(2026, 9, 13, 6, 52, 0, 0, time.UTC)
	return AssignmentPlaces{
		RestaurantLat: f64(12.9716), RestaurantLng: f64(77.5946),
		RestaurantAddressLine1: "1 Test Lane", RestaurantAddressLine2: "Near Test Park",
		RestaurantCity: "Bengaluru", RestaurantPhone: "08040000000",
		DeliverySnapshot: []byte(rnDropSnapshot), CustomerInstruction: "Ring the bell twice",
		ETAAt: &eta, ETASource: "google",
	}
}

// The exact drop-off reaches the rider only between accepting the job and
// handing it over: never on a merely assigned job, never after delivery, never
// on a cancelled order.
func TestDropVisibleWindow(t *testing.T) {
	for _, a := range allAssignmentStatuses {
		for _, o := range allOrderStatuses {
			want := rnOpenAssignment[a] && rnOnTheWay[o]
			if got := DropVisible(a, o); got != want {
				t.Errorf("DropVisible(%s, %s) = %v, want %v", a, o, got, want)
			}
		}
	}
}

// rnAssertRiderDrop checks one rider assignment response against the window.
func rnAssertRiderDrop(t *testing.T, stage string, a DeliveryAssignment, wantDrop bool) {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if (a.Drop != nil) != wantDrop || strings.Contains(body, `"drop":`) != wantDrop {
		t.Fatalf("%s (%s/%s): drop present = %v, want %v: %s", stage, a.Status, a.OrderStatus, a.Drop != nil, wantDrop, body)
	}
	for _, s := range rnDropOnly {
		if strings.Contains(body, s) != wantDrop {
			t.Fatalf("%s (%s/%s): %q present = %v, want %v: %s", stage, a.Status, a.OrderStatus, s, !wantDrop, wantDrop, body)
		}
	}
	for _, s := range rnNever {
		if strings.Contains(body, s) {
			t.Fatalf("%s (%s/%s): carries %q: %s", stage, a.Status, a.OrderStatus, s, body)
		}
	}
	hasDropURL := a.Navigation != nil && a.Navigation.DropURL != ""
	if hasDropURL != wantDrop || strings.Contains(body, "drop_url") != wantDrop {
		t.Fatalf("%s (%s/%s): drop_url present = %v, want %v", stage, a.Status, a.OrderStatus, hasDropURL, wantDrop)
	}
	if !wantDrop {
		return
	}
	d := a.Drop
	if d.AddressLine1 != "42 Lake View Road" || d.AddressLine2 != "Flat 3B" || d.Landmark != "Opp. City Park" ||
		d.City != "Bengaluru" || d.PostalCode != "560038" || d.CustomerFirstName != "Asha" ||
		d.DeliveryInstructions != "Ring the bell twice" ||
		d.Latitude == nil || *d.Latitude != 12.978449 || d.Longitude == nil || *d.Longitude != 77.640812 {
		t.Fatalf("%s: drop = %+v", stage, *d)
	}
	if a.Navigation.DropURL != rnDropURL {
		t.Fatalf("%s: drop_url = %q, want %q", stage, a.Navigation.DropURL, rnDropURL)
	}
}

// Every (assignment, order) status pair: the drop only inside the window, the
// restaurant and payout always, navigation on open jobs, a city-only summary
// on closed ones.
func TestFillRiderViewShowsTheDropOnlyInsideTheWindow(t *testing.T) {
	for _, as := range allAssignmentStatuses {
		for _, os := range allOrderStatuses {
			a := DeliveryAssignment{Status: as, OrderStatus: os, RestaurantName: "Test Kitchen",
				DeliveryFee: 29, DeliveryPartnerPayout: 23.2, DeliveryFeePaise: 2900, DeliveryPartnerPayoutPaise: 2320}
			a.FillRiderView(rnPlaces())
			rnAssertRiderDrop(t, "matrix", a, rnOpenAssignment[as] && rnOnTheWay[os])

			r := a.Restaurant
			if r == nil || r.Name != "Test Kitchen" || r.Latitude == nil || *r.Latitude != 12.9716 || r.Longitude == nil ||
				*r.Longitude != 77.5946 || r.AddressLine1 != "1 Test Lane" || r.AddressLine2 != "Near Test Park" ||
				r.City != "Bengaluru" || r.Phone != "08040000000" {
				t.Fatalf("%s/%s: restaurant = %+v", as, os, r)
			}
			if a.PayoutPaise != 2320 || a.Currency != "INR" {
				t.Fatalf("%s/%s: payout_paise = %d %q", as, os, a.PayoutPaise, a.Currency)
			}
			if rnClosedAssignment[as] {
				if a.Navigation != nil {
					t.Fatalf("%s/%s: a closed job keeps navigation %+v", as, os, a.Navigation)
				}
				if a.DropSummary == nil || a.DropSummary.City != "Bengaluru" || a.DropSummary.Locality != "Bengaluru" {
					t.Fatalf("%s/%s: drop_summary = %+v", as, os, a.DropSummary)
				}
			} else {
				if a.DropSummary != nil {
					t.Fatalf("%s/%s: drop_summary on an open job", as, os)
				}
				if a.Navigation == nil || a.Navigation.PickupURL != rnPickupURL {
					t.Fatalf("%s/%s: navigation = %+v", as, os, a.Navigation)
				}
			}
			if (a.ETAAt == "2026-09-13T06:52:00Z" && a.ETASource == "google") != ETAVisible(os) {
				t.Fatalf("%s/%s: eta = %q %q", as, os, a.ETAAt, a.ETASource)
			}
		}
	}
}

// No coordinates, no links: an accepted job still shows the address.
func TestFillRiderViewWithoutCoordinatesHasNoLinks(t *testing.T) {
	a := DeliveryAssignment{Status: "ACCEPTED", OrderStatus: orderstate.DeliveryAssigned}
	a.FillRiderView(AssignmentPlaces{RestaurantAddressLine1: "1 Test Lane", RestaurantCity: "Bengaluru",
		DeliverySnapshot: []byte(`{"address_line1":"42 Lake View Road","city":"Bengaluru","latitude":0,"longitude":0}`)})
	if a.Drop == nil || a.Drop.AddressLine1 != "42 Lake View Road" || a.Drop.Latitude != nil || a.Drop.Longitude != nil {
		t.Fatalf("drop = %+v", a.Drop)
	}
	if a.Restaurant == nil || a.Restaurant.Latitude != nil || a.Navigation != nil {
		t.Fatalf("restaurant = %+v navigation = %+v", a.Restaurant, a.Navigation)
	}
}

func TestMapsDirectionsURL(t *testing.T) {
	for _, c := range []struct {
		lat, lng float64
		want     string
	}{
		{12.9716, 77.5946, rnPickupURL},
		{12.978449, 77.640812, rnDropURL},
		{-33.8688, 151.2093, "https://www.google.com/maps/dir/?api=1&destination=-33.8688,151.2093&travelmode=two-wheeler"},
	} {
		if got := MapsDirectionsURL(c.lat, c.lng); got != c.want {
			t.Errorf("MapsDirectionsURL(%v, %v) = %q, want %q", c.lat, c.lng, got, c.want)
		}
	}
}

func rnOfferCtx(now time.Time, pingAge time.Duration) DeliveryOfferContext {
	recorded := now.Add(-pingAge)
	return DeliveryOfferContext{
		RestaurantID: uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0002"), RestaurantName: "Test Kitchen",
		RestaurantLat: f64(12.9716), RestaurantLng: f64(77.5946), RestaurantAddressLine1: "1 Test Lane", RestaurantCity: "Bengaluru",
		DropLat: f64(12.978449), DropLng: f64(77.640812), DropCity: "Bengaluru", PayoutPaise: 2320,
		RiderLat: f64(12.9616), RiderLng: f64(77.5846), RiderRecordedAt: &recorded,
	}
}

// rnAssertOfferNamesNoOne: an offer carries a ~500 m drop area and never the
// address, the customer or their exact pin.
func rnAssertOfferNamesNoOne(t *testing.T, body string) {
	t.Helper()
	banned := append(append([]string{}, rnDropOnly...), rnNever...)
	banned = append(banned, "address_line2", "landmark", "postal_code", "phone", "customer", "delivery_instructions")
	for _, s := range banned {
		if strings.Contains(body, s) {
			t.Fatalf("offer carries %q: %s", s, body)
		}
	}
}

func TestBuildDeliveryOfferViewRoundsTheDropAndNamesNoOne(t *testing.T) {
	now := time.Date(2026, 9, 13, 6, 30, 0, 0, time.UTC)
	distance := 1.25
	offer := DeliveryOffer{ID: uuid.New(), OrderID: uuid.New(), DeliveryPartnerID: uuid.New(), Status: "pending",
		DistanceKM: &distance, ExpiresAt: "2026-09-13 06:30:25+00", CreatedAt: "2026-09-13 06:30:00+00"}
	c := rnOfferCtx(now, 30*time.Second)
	v := BuildDeliveryOfferView(offer, &c, now, 2*time.Minute)
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	rnAssertOfferNamesNoOne(t, string(raw))

	if v.DropArea == nil || v.DropArea.Latitude != 12.98 || v.DropArea.Longitude != 77.64 || v.DropArea.Locality != "Bengaluru" {
		t.Fatalf("drop_area = %+v", v.DropArea)
	}
	for _, coord := range []float64{v.DropArea.Latitude, v.DropArea.Longitude} {
		if math.Abs(coord*200-math.Round(coord*200)) > 1e-6 {
			t.Fatalf("drop_area coordinate %v is not on the 0.005 degree grid", coord)
		}
	}
	if r := v.Restaurant; r == nil || r.ID != c.RestaurantID || r.Name != "Test Kitchen" || r.Latitude == nil || *r.Latitude != 12.9716 ||
		r.Longitude == nil || *r.Longitude != 77.5946 || r.AddressLine1 != "1 Test Lane" || r.City != "Bengaluru" {
		t.Fatalf("restaurant = %+v", v.Restaurant)
	}
	wantTrip := int64(math.Round(geo.HaversineKM(12.9716, 77.5946, 12.978449, 77.640812) * 1000))
	if v.TripDistanceMeters == nil || *v.TripDistanceMeters != wantTrip {
		t.Fatalf("trip_distance_meters = %v, want %d", v.TripDistanceMeters, wantTrip)
	}
	wantToRestaurant := int64(math.Round(geo.HaversineKM(12.9616, 77.5846, 12.9716, 77.5946) * 1000))
	if v.DistanceToRestaurantMeters == nil || *v.DistanceToRestaurantMeters != wantToRestaurant {
		t.Fatalf("distance_to_restaurant_meters = %v, want %d", v.DistanceToRestaurantMeters, wantToRestaurant)
	}
	if v.PayoutPaise == nil || *v.PayoutPaise != 2320 || v.Currency != "INR" {
		t.Fatalf("payout = %v %q", v.PayoutPaise, v.Currency)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != offer.ID.String() || got["expires_at"] != offer.ExpiresAt || got["distance_km"] != 1.25 || got["status"] != "pending" {
		t.Fatalf("the existing offer keys changed: %s", raw)
	}

	stale := rnOfferCtx(now, 3*time.Minute)
	if sv := BuildDeliveryOfferView(offer, &stale, now, 2*time.Minute); sv.DistanceToRestaurantMeters != nil || sv.TripDistanceMeters == nil {
		t.Fatalf("stale ping: distance_to_restaurant_meters = %v trip = %v", sv.DistanceToRestaurantMeters, sv.TripDistanceMeters)
	}
	bare, _ := json.Marshal(BuildDeliveryOfferView(offer, nil, now, 2*time.Minute))
	old, _ := json.Marshal(offer)
	if string(bare) != string(old) {
		t.Fatalf("an offer without context changed shape:\n got %s\nwant %s", bare, old)
	}
}

// Two drops ~300 m apart inside one 0.005 degree cell give the same drop area;
// the snapped point is never more than ~400 m from the true pin, and its JSON
// has at most 3 decimals.
func TestDropAreaSnapsToA550MetreCell(t *testing.T) {
	now := time.Date(2026, 9, 13, 6, 30, 0, 0, time.UTC)
	offer := DeliveryOffer{ID: uuid.New(), Status: "pending"}
	area := func(lat, lng float64) (DropArea, string) {
		t.Helper()
		c := rnOfferCtx(now, 30*time.Second)
		c.DropLat, c.DropLng = f64(lat), f64(lng)
		v := BuildDeliveryOfferView(offer, &c, now, 2*time.Minute)
		if v.DropArea == nil {
			t.Fatalf("no drop_area for %v,%v", lat, lng)
		}
		raw, err := json.Marshal(v.DropArea)
		if err != nil {
			t.Fatal(err)
		}
		return *v.DropArea, string(raw)
	}

	aLat, aLng, bLat, bLng := 12.9740, 77.6395, 12.9760, 77.6410
	if d := geo.HaversineKM(aLat, aLng, bLat, bLng) * 1000; d < 250 || d > 350 {
		t.Fatalf("the two drops are %.0f m apart, want ~300", d)
	}
	a, aJSON := area(aLat, aLng)
	b, bJSON := area(bLat, bLng)
	if a != b || aJSON != bJSON {
		t.Fatalf("same cell, different drop_area: %s vs %s", aJSON, bJSON)
	}
	if aJSON != `{"latitude":12.975,"longitude":77.64,"locality":"Bengaluru"}` {
		t.Fatalf("drop_area = %s", aJSON)
	}

	threeDecimals := regexp.MustCompile(`^\{"latitude":-?\d+(\.\d{1,3})?,"longitude":-?\d+(\.\d{1,3})?,`)
	for lat := 12.9; lat < 13.1; lat += 0.0013 {
		for lng := 77.5; lng < 77.7; lng += 0.0017 {
			got, raw := area(lat, lng)
			if d := geo.HaversineKM(lat, lng, got.Latitude, got.Longitude) * 1000; d > 400 {
				t.Fatalf("drop_area %s is %.0f m from %v,%v", raw, d, lat, lng)
			}
			if !threeDecimals.MatchString(raw) {
				t.Fatalf("drop_area %s has more than 3 decimals (from %v,%v)", raw, lat, lng)
			}
		}
	}
}

func TestDeliveryEarningsMapCarriesPaiseSiblings(t *testing.T) {
	m := DeliveryEarningsMap(2, 51.6, 5160, 14, 1234.56, 123456)
	if m["deliveries_today"] != 2 || m["earnings_today"] != 51.6 || m["earnings_today_paise"] != int64(5160) ||
		m["total_deliveries"] != 14 || m["total_earnings"] != 1234.56 || m["total_earnings_paise"] != int64(123456) ||
		m["currency"] != "INR" {
		t.Fatalf("earnings = %v", m)
	}
}
