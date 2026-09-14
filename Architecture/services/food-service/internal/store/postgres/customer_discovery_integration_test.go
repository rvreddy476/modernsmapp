package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const closedEveryDaySQL = `
	INSERT INTO food.restaurant_operating_hours (restaurant_id, day_of_week, opens_at, closes_at, is_closed)
	SELECT $1, d, '00:00', '23:59', TRUE FROM generate_series(0, 6) d`

// sameOutcome: both succeeded, or both refused with target.
func sameOutcome(err, target error) bool {
	if err == nil || target == nil {
		return err == nil && target == nil
	}
	return errors.Is(err, target)
}

// The restaurant list, the restaurant detail and add-to-cart judge an address
// exactly as PlaceOrder does: for each case the list's serviceable/reason is
// PlaceOrder's accept/refuse.
func TestServiceabilityAgreesWithPlaceOrder(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	lat, lng := 12.9, 77.6
	exec := func(t *testing.T, sql string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name         string
		noPin        bool
		addrKM       float64
		setup        string
		listed       bool // the list only carries restaurants accepting orders
		wantPlaceErr error
	}{
		{"in range", false, 1, "", true, nil},
		{"beyond the default radius", false, 50, "", true, ErrAddressOutOfRange},
		{"service area covers beyond the default radius", false, 9,
			`INSERT INTO food.restaurant_service_areas (restaurant_id, area_name, radius_km) VALUES ($1, 'wide', 10)`, true, nil},
		{"service areas exist and none covers", false, 5,
			`INSERT INTO food.restaurant_service_areas (restaurant_id, area_name, radius_km) VALUES ($1, 'tight', 3)`, true, ErrAddressOutOfRange},
		{"closed every day", false, 1, closedEveryDaySQL, true, ErrRestaurantOutsideHours},
		{"switched closed", false, 1, `UPDATE food.restaurants SET is_open = FALSE WHERE id = $1`, true, ErrRestaurantNotAccepting},
		{"not accepting orders", false, 1, `UPDATE food.restaurants SET is_accepting_orders = FALSE WHERE id = $1`, false, ErrRestaurantNotAccepting},
		{"restaurant without a pin", true, 1, "", true, ErrRestaurantLocationMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restLat, restLng := f64(lat), f64(lng)
			if tc.noPin {
				restLat, restLng = nil, nil
			}
			customer, rid, _, menuItem := seedPlaceableCart(t, s, restLat, restLng)
			if tc.setup != "" {
				exec(t, tc.setup, rid)
			}
			addrLat := kmNorth(lat, tc.addrKM)
			addr := seedAddress(t, s, customer, f64(addrLat), f64(lng))
			near := &GeoPoint{Lat: addrLat, Lng: lng}

			_, placeErr := s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString())
			if !sameOutcome(placeErr, tc.wantPlaceErr) {
				t.Fatalf("precondition: PlaceOrder = %v, want %v", placeErr, tc.wantPlaceErr)
			}

			agrees := func(t *testing.T, where string, r RestaurantSummary) {
				t.Helper()
				serviceable := r.Serviceable != nil && *r.Serviceable
				if r.Serviceable == nil || serviceable != (placeErr == nil) {
					t.Fatalf("%s: serviceable = %v (unset %v), PlaceOrder = %v", where, serviceable, r.Serviceable == nil, placeErr)
				}
				if placeErr != nil && (r.Unserviceable == nil || !errors.Is(placeErr, r.Unserviceable)) {
					t.Fatalf("%s: reason %v, PlaceOrder refused with %v", where, r.Unserviceable, placeErr)
				}
				if placeErr == nil && r.Unserviceable != nil {
					t.Fatalf("%s: reason %v on a serviceable restaurant", where, r.Unserviceable)
				}
				if (r.DistanceMeters != nil) == tc.noPin {
					t.Fatalf("%s: distance %v with restaurant pin %v", where, r.DistanceMeters, !tc.noPin)
				}
				if r.DistanceMeters != nil && math.Abs(float64(*r.DistanceMeters)-tc.addrKM*1000) > 2 {
					t.Fatalf("%s: distance %d m, want %.0f", where, *r.DistanceMeters, tc.addrKM*1000)
				}
			}

			detail, err := s.GetRestaurant(ctx, rid, near)
			if err != nil {
				t.Fatalf("detail: %v", err)
			}
			agrees(t, "detail", detail.RestaurantSummary)

			var name string
			if err := s.db.QueryRow(ctx, `SELECT name FROM food.restaurants WHERE id = $1`, rid).Scan(&name); err != nil {
				t.Fatal(err)
			}
			list, err := s.ListRestaurants(ctx, RestaurantFilter{Query: name, Near: near, Limit: 50})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			found := false
			for _, r := range list {
				if r.ID == rid {
					found = true
					agrees(t, "list", r)
				}
			}
			if found != tc.listed {
				t.Fatalf("listed = %v, want %v", found, tc.listed)
			}

			_, addErr := s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: menuItem, Quantity: 1, Near: near})
			if !sameOutcome(addErr, tc.wantPlaceErr) {
				t.Fatalf("add-to-cart with lat/lng = %v, PlaceOrder = %v", addErr, placeErr)
			}
			_, addErr = s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: menuItem, Quantity: 1, AddressID: &addr})
			if !sameOutcome(addErr, tc.wantPlaceErr) {
				t.Fatalf("add-to-cart with address_id = %v, PlaceOrder = %v", addErr, placeErr)
			}
		})
	}
}

// With coordinates the list is serviceable first, then nearest, and the limit
// applies after ranking; without them nothing about serviceability is set.
func TestNearMeListRanksServiceableThenNearest(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	lat, lng := 12.9, 77.6
	city := "Rank " + uuid.NewString()[:8]
	seed := func(km float64, setup string) uuid.UUID {
		t.Helper()
		_, rid, _, _ := seedPlaceableCart(t, s, f64(kmNorth(lat, km)), f64(lng))
		if _, err := s.db.Exec(ctx, `UPDATE food.restaurants SET city = $2 WHERE id = $1`, rid, city); err != nil {
			t.Fatal(err)
		}
		if setup != "" {
			if _, err := s.db.Exec(ctx, setup, rid); err != nil {
				t.Fatal(err)
			}
		}
		return rid
	}
	far := seed(3, "")
	near := seed(1, "")
	closedNearest := seed(0.5, closedEveryDaySQL)
	outOfRange := seed(50, "")
	here := &GeoPoint{Lat: lat, Lng: lng}

	ids := func(rs []RestaurantSummary) []uuid.UUID {
		out := make([]uuid.UUID, len(rs))
		for i, r := range rs {
			out[i] = r.ID
		}
		return out
	}
	same := func(a, b []uuid.UUID) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	got, err := s.ListRestaurants(ctx, RestaurantFilter{City: city, Near: here, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if want := []uuid.UUID{near, far, closedNearest, outOfRange}; !same(ids(got), want) {
		t.Fatalf("near-me order = %v, want %v", ids(got), want)
	}
	top, err := s.ListRestaurants(ctx, RestaurantFilter{City: city, Near: here, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := []uuid.UUID{near, far}; !same(ids(top), want) {
		t.Fatalf("limit 2 = %v, want %v", ids(top), want)
	}

	plain, err := s.ListRestaurants(ctx, RestaurantFilter{City: city, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 4 {
		t.Fatalf("plain list = %d restaurants", len(plain))
	}
	for _, r := range plain {
		if r.Serviceable != nil || r.DistanceMeters != nil || r.Unserviceable != nil {
			t.Fatalf("no coordinates, but %s carries serviceability: %+v", r.Name, r)
		}
		if r.ID == closedNearest && (r.IsOpenNow || r.NextOpensAt != nil) {
			t.Fatalf("a restaurant closed every day: is_open_now %v next %v", r.IsOpenNow, r.NextOpensAt)
		}
		if r.ID != closedNearest && !r.IsOpenNow {
			t.Fatalf("%s has no schedule, so it is open now", r.Name)
		}
	}
}

// The customer menu carries each item's available variants and add-ons with
// exact paise; unavailable ones are left out; the partner read is unchanged.
func TestCustomerMenuCarriesAvailableVariantsAndAddons(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_, rid, owner, item := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	yes, no, zero, two := true, false, 0, 2

	var categoryID uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT category_id FROM food.menu_items WHERE id = $1`, item).Scan(&categoryID); err != nil {
		t.Fatal(err)
	}
	var soldOut uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.menu_items (restaurant_id, category_id, name, base_price, food_type, preparation_minutes, is_available, is_active, tax_percentage)
		VALUES ($1, $2, 'Sold Out Thali', 120, 'VEG', 10, FALSE, TRUE, 5) RETURNING id`, rid, categoryID).Scan(&soldOut); err != nil {
		t.Fatal(err)
	}
	name := func(v string) *string { return &v }
	price := func(v float64) *float64 { return &v }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.CreateMenuVariant(ctx, owner, item, MenuPriceInput{Name: name("Half"), Price: price(19.99), IsAvailable: &yes})
	must(err)
	_, err = s.CreateMenuVariant(ctx, owner, item, MenuPriceInput{Name: name("Family"), Price: price(25), IsAvailable: &no})
	must(err)
	group, err := s.CreateAddonGroup(ctx, owner, item, MenuAddonGroupInput{Name: name("Extras"), MinSelect: &zero, MaxSelect: &two})
	must(err)
	_, err = s.CreateAddon(ctx, owner, item, group.ID, MenuPriceInput{Name: name("Extra cheese"), Price: price(1.15), IsAvailable: &yes})
	must(err)
	_, err = s.CreateAddon(ctx, owner, item, group.ID, MenuPriceInput{Name: name("Truffle"), Price: price(2), IsAvailable: &no})
	must(err)
	_, err = s.CreateMenuVariant(ctx, owner, soldOut, MenuPriceInput{Name: name("Large"), Price: price(0.29), IsAvailable: &yes})
	must(err)

	menu, err := s.GetMenu(ctx, rid)
	must(err)
	byID := map[uuid.UUID]MenuItem{}
	for _, c := range menu {
		for _, it := range c.Items {
			byID[it.ID] = it
		}
	}
	exactPaise := func(t *testing.T, what string, rupees float64, paise, want int64) {
		t.Helper()
		if paise != want || float64(paise) != math.Round(rupees*100) {
			t.Fatalf("%s: price %v price_paise %d, want %d", what, rupees, paise, want)
		}
	}

	dish := byID[item]
	if dish.Variants == nil || len(*dish.Variants) != 1 || (*dish.Variants)[0].Name != "Half" || !(*dish.Variants)[0].IsAvailable {
		t.Fatalf("variants = %+v (only the available one)", dish.Variants)
	}
	exactPaise(t, "Half", (*dish.Variants)[0].Price, (*dish.Variants)[0].PricePaise, 1999)
	if dish.AddonGroups == nil || len(*dish.AddonGroups) != 1 || (*dish.AddonGroups)[0].ID != group.ID {
		t.Fatalf("addon groups = %+v", dish.AddonGroups)
	}
	addons := (*dish.AddonGroups)[0].Addons
	if len(addons) != 1 || addons[0].Name != "Extra cheese" || (*dish.AddonGroups)[0].MaxSelect != 2 {
		t.Fatalf("add-ons = %+v (only the available one)", addons)
	}
	exactPaise(t, "Extra cheese", addons[0].Price, addons[0].PricePaise, 115)

	sold, ok := byID[soldOut]
	if !ok || sold.IsAvailable {
		t.Fatalf("an unavailable item is still listed as unavailable: %+v %v", sold, ok)
	}
	if sold.Variants == nil || len(*sold.Variants) != 1 || sold.AddonGroups == nil || len(*sold.AddonGroups) != 0 {
		t.Fatalf("sold-out extras = %+v %+v", sold.Variants, sold.AddonGroups)
	}
	exactPaise(t, "Large", (*sold.Variants)[0].Price, (*sold.Variants)[0].PricePaise, 29)

	raw, err := json.Marshal(menu)
	must(err)
	if strings.Count(string(raw), `"variants":[`) != 2 || strings.Count(string(raw), `"addon_groups":[`) != 2 || strings.Contains(string(raw), "Truffle") {
		t.Fatalf("customer menu JSON = %s", raw)
	}

	partner, err := s.GetPartnerMenuItem(ctx, owner, item)
	must(err)
	if len(partner.Variants) != 2 || len(partner.AddonGroups[0].Addons) != 2 {
		t.Fatalf("the partner read must still show unavailable extras: %+v", partner)
	}
	cats, err := s.ListMenuCategories(ctx, owner, rid)
	must(err)
	if raw, _ := json.Marshal(cats); strings.Contains(string(raw), `"variants"`) || strings.Contains(string(raw), `"addon_groups"`) {
		t.Fatalf("the partner category list changed shape: %s", raw)
	}
}

// Add-to-cart refuses an out-of-range point with PlaceOrder's error; without a
// point it adds as before.
func TestAddToCartRefusesOutOfRangeLikePlaceOrder(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	lat, lng := 12.9, 77.6
	customer, _, _, item := seedPlaceableCart(t, s, f64(lat), f64(lng))

	_, err := s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: item, Quantity: 1, Near: &GeoPoint{Lat: kmNorth(lat, 50), Lng: lng}})
	assertAddressOutOfRange(t, err)
	farAddr := seedAddress(t, s, customer, f64(kmNorth(lat, 50)), f64(lng))
	_, err = s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: item, Quantity: 1, AddressID: &farAddr})
	assertAddressOutOfRange(t, err)
	_, err = s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: farAddr, PaymentMethod: "COD"}, uuid.NewString())
	assertAddressOutOfRange(t, err)

	pinless := seedAddress(t, s, customer, nil, nil)
	_, err = s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: item, Quantity: 1, AddressID: &pinless})
	assertAddressLocationRequired(t, err)
	strangers := seedAddress(t, s, uuid.New(), f64(lat), f64(lng))
	if _, err := s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: item, Quantity: 1, AddressID: &strangers}); !errors.Is(err, ErrCartAddressNotFound) {
		t.Fatalf("someone else's address = %v", err)
	}

	cart, err := s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: item, Quantity: 1})
	if err != nil || len(cart.Items) != 2 {
		t.Fatalf("no point: add as today = %+v, %v", cart, err)
	}
	near := seedAddress(t, s, customer, f64(kmNorth(lat, 1)), f64(lng))
	if cart, err := s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: item, Quantity: 1, AddressID: &near}); err != nil || len(cart.Items) != 3 {
		t.Fatalf("in range = %+v, %v", cart, err)
	}
}
