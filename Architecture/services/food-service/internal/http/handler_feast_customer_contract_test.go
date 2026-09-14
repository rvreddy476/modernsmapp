package http

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Golden contract fixtures for the Feast customer routes the Momentum app
// reads: the restaurant list (plain and near-me), the detail, the customer
// menu with variants and add-ons, addresses, add-to-cart (201 and the
// out-of-range refusal), placing, listing, cancelling and tracking an order.
// Regenerate deliberately with UPDATE_CONTRACTS=1 and review the diff.

var (
	ctFeastNightOwl   = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0040")
	ctFeastDhaba      = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0041")
	ctFeastHome       = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0042")
	ctFeastWork       = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0043")
	ctFeastCategory   = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0044")
	ctFeastVariant    = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0045")
	ctFeastGroup      = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0046")
	ctFeastSoldOut    = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0047")
	ctFeastAssignment = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0048")
)

// The customer's Home pin, and a point well outside every restaurant's range.
const (
	ctFeastHomeLat, ctFeastHomeLng = 12.9824, 77.6045
	ctFeastFarLat                  = 13.4
)

type feastContractStore struct {
	service.Store
}

// ctFeastRestaurants is three restaurants in today's order (open first, then
// rating); near adds what the shared rule says for the Home pin, ranked
// serviceable first, then nearest.
func ctFeastRestaurants(near bool) []postgres.RestaurantSummary {
	yes, no := true, false
	meters := func(v int64) *int64 { return &v }
	nextOpen := "2026-09-13T18:00:00+05:30"
	kitchen := postgres.RestaurantSummary{ID: ctRestaurant, Name: "Test Kitchen", Slug: "test-kitchen", Description: "South Indian",
		City: "Bengaluru", State: "Karnataka", Status: "ACTIVE", IsOpen: true, IsAcceptingOrders: true, AvgRating: 4.5, RatingCount: 120,
		MinOrderAmount: 99, PackagingFee: 10, AvgPreparationMins: 20, HeroImageURL: "https://cdn.example.test/test-kitchen.jpg",
		Cuisines: []string{"North Indian", "South Indian"}, EstimatedDelivery: "32-42 min", DeliveryFeeEstimate: 29, IsOpenNow: true}
	dhaba := postgres.RestaurantSummary{ID: ctFeastDhaba, Name: "Highway Dhaba", Slug: "highway-dhaba", City: "Bengaluru", State: "Karnataka",
		Status: "ACTIVE", IsOpen: true, IsAcceptingOrders: true, AvgRating: 4.2, RatingCount: 75, MinOrderAmount: 199, PackagingFee: 20,
		AvgPreparationMins: 30, Cuisines: []string{"Punjabi"}, EstimatedDelivery: "42-52 min", DeliveryFeeEstimate: 29, IsOpenNow: true}
	owl := postgres.RestaurantSummary{ID: ctFeastNightOwl, Name: "Night Owl Biryani", Slug: "night-owl-biryani", City: "Bengaluru", State: "Karnataka",
		Status: "ACTIVE", IsOpen: true, IsAcceptingOrders: true, AvgRating: 3.9, RatingCount: 40, MinOrderAmount: 149, PackagingFee: 15,
		AvgPreparationMins: 25, Cuisines: []string{"Biryani"}, EstimatedDelivery: "37-47 min", DeliveryFeeEstimate: 29, NextOpensAt: &nextOpen}
	if !near {
		return []postgres.RestaurantSummary{kitchen, dhaba, owl}
	}
	kitchen.DistanceMeters, kitchen.Serviceable = meters(1200), &yes
	owl.DistanceMeters, owl.Serviceable, owl.Unserviceable = meters(2600), &no, postgres.ErrRestaurantOutsideHours
	dhaba.DistanceMeters, dhaba.Serviceable, dhaba.Unserviceable = meters(14800), &no, postgres.ErrAddressOutOfRange
	return []postgres.RestaurantSummary{kitchen, owl, dhaba}
}

func (f *feastContractStore) ListRestaurants(_ context.Context, filter postgres.RestaurantFilter) ([]postgres.RestaurantSummary, error) {
	return ctFeastRestaurants(filter.Near != nil), nil
}

func (f *feastContractStore) GetRestaurant(_ context.Context, id uuid.UUID, near *postgres.GeoPoint) (*postgres.RestaurantDetail, error) {
	if id != ctRestaurant {
		return nil, pgx.ErrNoRows
	}
	return &postgres.RestaurantDetail{RestaurantSummary: ctFeastRestaurants(near != nil)[0], Phone: "08040000000", Email: "kitchen@example.test",
		AddressLine: "1 Test Lane", PostalCode: "560001", Latitude: 12.9716, Longitude: 77.5946}, nil
}

func (f *feastContractStore) GetMenu(context.Context, uuid.UUID) ([]postgres.MenuCategory, error) {
	dish := postgres.MenuItem{ID: ctMenuItem, RestaurantID: ctRestaurant, CategoryID: ctFeastCategory, Name: "Paneer Tikka",
		Description: "Charred cottage cheese", FoodType: "VEG", BasePrice: 250, DiscountPrice: 225.5,
		ImageURL: "https://cdn.example.test/paneer-tikka.jpg", PreparationMinutes: 20, IsAvailable: true, IsRecommended: true, TaxPercentage: 5}
	dish.FillPaise()
	variants := []postgres.MenuVariant{{ID: ctFeastVariant, MenuItemID: ctMenuItem, Name: "Half", Price: 149.99, PricePaise: 14999, IsAvailable: true, SortOrder: 1}}
	groups := []postgres.MenuAddonGroup{{ID: ctFeastGroup, MenuItemID: ctMenuItem, Name: "Extras", MinSelect: 0, MaxSelect: 2, SortOrder: 1,
		Addons: []postgres.MenuAddon{{ID: ctAddon, AddonGroupID: ctFeastGroup, Name: "Extra cheese", Price: 30, PricePaise: 3000, IsAvailable: true, SortOrder: 1}}}}
	dish.Variants, dish.AddonGroups = &variants, &groups

	soldOut := postgres.MenuItem{ID: ctFeastSoldOut, RestaurantID: ctRestaurant, CategoryID: ctFeastCategory, Name: "Veg Thali", FoodType: "VEG",
		BasePrice: 180, PreparationMinutes: 15, IsAvailable: false, TaxPercentage: 5}
	soldOut.FillPaise()
	noVariants, noGroups := []postgres.MenuVariant{}, []postgres.MenuAddonGroup{}
	soldOut.Variants, soldOut.AddonGroups = &noVariants, &noGroups
	return []postgres.MenuCategory{{ID: ctFeastCategory, Name: "Mains", SortOrder: 1, Items: []postgres.MenuItem{dish, soldOut}}}, nil
}

func ctFeastHomeAddress(userID uuid.UUID) postgres.Address {
	// 11 digits with a leading 0: no Aadhaar-shaped run for the KYC fixture scan.
	return postgres.Address{ID: ctFeastHome, UserID: userID, Label: "Home", ReceiverName: "Test Customer", Phone: "08040000001",
		AddressLine1: "2 Test Road", AddressLine2: "Flat 4", Landmark: "Near Test Park", City: "Bengaluru", State: "Karnataka",
		Country: "India", PostalCode: "560001", Latitude: ctFeastHomeLat, Longitude: ctFeastHomeLng, IsDefault: true}
}

func (f *feastContractStore) ListAddresses(_ context.Context, userID uuid.UUID) ([]postgres.Address, error) {
	return []postgres.Address{
		ctFeastHomeAddress(userID),
		// Saved without a map pin: latitude/longitude are omitted.
		{ID: ctFeastWork, UserID: userID, Label: "Work", AddressLine1: "9 Office Park", City: "Bengaluru", Country: "India"},
	}, nil
}

func (f *feastContractStore) CreateAddress(_ context.Context, userID uuid.UUID, in postgres.AddressInput) (*postgres.Address, error) {
	a := postgres.Address{ID: ctFeastHome, UserID: userID, Label: in.Label, ReceiverName: in.ReceiverName, Phone: in.Phone,
		AddressLine1: in.AddressLine1, AddressLine2: in.AddressLine2, Landmark: in.Landmark, City: in.City, State: in.State,
		Country: in.Country, PostalCode: in.PostalCode, IsDefault: in.IsDefault}
	if in.Latitude != nil && in.Longitude != nil {
		a.Latitude, a.Longitude = *in.Latitude, *in.Longitude
	}
	return &a, nil
}

func (f *feastContractStore) AddCartItem(_ context.Context, _ uuid.UUID, in postgres.AddCartItemInput) (*postgres.Cart, error) {
	if in.Near != nil && in.Near.Lat >= ctFeastFarLat {
		return nil, postgres.ErrAddressOutOfRange
	}
	return ctCartFor("RESTAURANT_STANDALONE", ""), nil
}

func ctFeastOrder(ctx context.Context) *postgres.Order {
	o, err := (&moneyContractStore{}).GetOrder(ctx, ctCustomer, ctOrder)
	if err != nil {
		panic(err)
	}
	return o
}

func (f *feastContractStore) PlaceOrder(ctx context.Context, _ uuid.UUID, _ postgres.PlaceOrderInput, _ string) (*postgres.Order, error) {
	o := ctFeastOrder(ctx)
	o.Status, o.PaymentStatus = "PAYMENT_PENDING", "PENDING"
	o.History = []postgres.OrderStatusHistory{{ToStatus: "PAYMENT_PENDING", Reason: "order placed", CreatedAt: ctTime}}
	return o, nil
}

func (f *feastContractStore) ListOrders(ctx context.Context, _ uuid.UUID) ([]postgres.Order, error) {
	o := ctFeastOrder(ctx)
	// ListOrders' SELECT carries no items, history or money block.
	o.Items, o.History, o.Money = nil, nil, nil
	return []postgres.Order{*o}, nil
}

func (f *feastContractStore) CancelOrder(ctx context.Context, _, _ uuid.UUID, reason string) (*postgres.Order, error) {
	o := ctFeastOrder(ctx)
	o.Status, o.PaymentStatus = "CANCELLED_BY_CUSTOMER", "PENDING"
	o.History = []postgres.OrderStatusHistory{
		{ToStatus: "PAYMENT_PENDING", Reason: "order placed", CreatedAt: ctTime},
		{FromStatus: "PAYMENT_PENDING", ToStatus: "CANCELLED_BY_CUSTOMER", Reason: reason, CreatedAt: "2026-09-13T06:32:00Z"},
	}
	return o, nil
}

// An unpaid order has no refund to submit.
func (f *feastContractStore) OpenRefundPlan(context.Context, uuid.UUID) (*postgres.RefundPlan, error) {
	return nil, pgx.ErrNoRows
}

// GetOrderTracking mirrors tracking_payments.go: the timeline, assignment and
// delivery location rows as the store builds them.
func (f *feastContractStore) GetOrderTracking(_ context.Context, _, orderID uuid.UUID) (map[string]any, error) {
	step := func(from, to, label, reason, at string) map[string]any {
		return map[string]any{"from_status": from, "to_status": to, "label": label, "reason": reason, "completed": true, "created_at": at}
	}
	return map[string]any{
		"order_id":     orderID,
		"order_number": "FG1000000000001",
		"status":       "OUT_FOR_DELIVERY",
		"timeline": []map[string]any{
			step("PAYMENT_PENDING", "CONFIRMED", "Order confirmed", "payment captured", "2026-09-13 06:30:00+00"),
			step("CONFIRMED", "PREPARING", "Restaurant preparing", "", "2026-09-13 06:33:00+00"),
			step("PREPARING", "READY_FOR_PICKUP", "Ready for pickup", "", "2026-09-13 06:48:00+00"),
			step("READY_FOR_PICKUP", "OUT_FOR_DELIVERY", "Out for delivery", "", "2026-09-13 06:52:00+00"),
		},
		"assignment": map[string]any{"id": ctFeastAssignment.String(), "delivery_partner_id": ctPartner.String(), "status": "PICKED_UP",
			"created_at": "2026-09-13 06:45:00+00"},
		"restaurant_location": map[string]any{"latitude": 12.9716, "longitude": 77.5946, "address_line1": "1 Test Lane", "city": "Bengaluru", "state": "Karnataka"},
		"delivery_location": map[string]any{"delivery_partner_id": ctPartner.String(), "latitude": 12.9751, "longitude": 77.5988,
			"recorded_at": "2026-09-13 06:55:00+00"},
		"customer_location":          map[string]any{"latitude": ctFeastHomeLat, "longitude": ctFeastHomeLng, "address_line1": "2 Test Road", "city": "Bengaluru", "state": "Karnataka"},
		"estimated_delivery_minutes": 29,
		"eta_at":                     "2026-09-13T07:05:00Z",
		"eta_source":                 "haversine",
	}, nil
}

func feastContractRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.New(&feastContractStore{})).RegisterRoutes(router)
	return router
}

// feastCustomerFixtures lists the fixtures this file owns.
var feastCustomerFixtures = []string{
	"restaurants_get_200", "restaurants_get_200_near", "restaurants_get_422_location_required",
	"restaurant_get_200", "restaurant_get_200_near", "restaurant_menu_get_200",
	"addresses_get_200", "address_post_201",
	"cart_item_post_201", "cart_item_post_422_out_of_range",
	"order_place_201", "orders_get_200", "order_cancel_post_200", "order_tracking_get_200",
}

func TestFeastCustomerContracts(t *testing.T) {
	restaurant := "/v1/food/restaurants/" + ctRestaurant.String()
	order := "/v1/food/orders/" + ctOrder.String()
	home := "lat=12.9824&lng=77.6045"
	addon := `"addons":[{"addon_id":"` + ctAddon.String() + `","quantity":1}]`
	cases := []struct {
		fixture, method, path, body string
		status                      int
	}{
		{"restaurants_get_200", http.MethodGet, "/v1/food/restaurants?city=Bengaluru", ``, http.StatusOK},
		{"restaurants_get_200_near", http.MethodGet, "/v1/food/restaurants?city=Bengaluru&" + home, ``, http.StatusOK},
		{"restaurants_get_422_location_required", http.MethodGet, "/v1/food/restaurants?lat=12.9824", ``, http.StatusUnprocessableEntity},
		{"restaurant_get_200", http.MethodGet, restaurant, ``, http.StatusOK},
		{"restaurant_get_200_near", http.MethodGet, restaurant + "?" + home, ``, http.StatusOK},
		{"restaurant_menu_get_200", http.MethodGet, restaurant + "/menu", ``, http.StatusOK},
		{"addresses_get_200", http.MethodGet, "/v1/food/addresses", ``, http.StatusOK},
		{"address_post_201", http.MethodPost, "/v1/food/addresses",
			`{"label":"Home","receiver_name":"Test Customer","phone":"08040000001","address_line1":"2 Test Road","address_line2":"Flat 4",` +
				`"landmark":"Near Test Park","city":"Bengaluru","state":"Karnataka","country":"India","postal_code":"560001",` +
				`"latitude":12.9824,"longitude":77.6045,"is_default":true}`, http.StatusCreated},
		{"cart_item_post_201", http.MethodPost, "/v1/food/cart/items",
			`{"menu_item_id":"` + ctMenuItem.String() + `","quantity":2,` + addon + `,"lat":12.9824,"lng":77.6045}`, http.StatusCreated},
		{"cart_item_post_422_out_of_range", http.MethodPost, "/v1/food/cart/items",
			`{"menu_item_id":"` + ctMenuItem.String() + `","quantity":1,"lat":13.4,"lng":77.6045}`, http.StatusUnprocessableEntity},
		{"order_place_201", http.MethodPost, "/v1/food/orders", `{"address_id":"` + ctFeastHome.String() + `","payment_method":"upi"}`, http.StatusCreated},
		{"orders_get_200", http.MethodGet, "/v1/food/orders", ``, http.StatusOK},
		{"order_cancel_post_200", http.MethodPost, order + "/cancel", `{"reason":"changed my mind"}`, http.StatusOK},
		{"order_tracking_get_200", http.MethodGet, order + "/tracking", ``, http.StatusOK},
	}
	if len(cases) != len(feastCustomerFixtures) {
		t.Fatalf("cases %d, fixtures listed %d", len(cases), len(feastCustomerFixtures))
	}
	for i, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			if tc.fixture != feastCustomerFixtures[i] {
				t.Fatalf("case %d is %s, listed as %s", i, tc.fixture, feastCustomerFixtures[i])
			}
			rec := doJSON(feastContractRouter(), tc.method, tc.path, tc.body, ctCustomer, false)
			assertContract(t, rec, tc.status, tc.fixture)
			if strings.Contains(rec.Body.String(), `"delivery_code"`) {
				t.Fatalf("%s carries a delivery code", tc.fixture)
			}
		})
	}
}

// The near-me fixture names each refusal exactly as POST /orders does, and the
// add-to-cart refusal fixture is POST /orders' answer for the same error.
func TestFeastCustomerFixturesSpeakLikePlaceOrder(t *testing.T) {
	var list struct {
		Data struct {
			Items []struct {
				Serviceable *bool  `json:"serviceable"`
				Code        string `json:"unserviceable_reason_code"`
				Message     string `json:"unserviceable_message"`
			} `json:"items"`
		} `json:"data"`
	}
	readFixture(t, "restaurants_get_200_near", &list)
	wantCodes := []string{"", "FOOD_RESTAURANT_OUTSIDE_HOURS", "FOOD_ADDRESS_OUT_OF_RANGE"}
	wantErrs := []error{nil, postgres.ErrRestaurantOutsideHours, postgres.ErrAddressOutOfRange}
	if len(list.Data.Items) != len(wantCodes) {
		t.Fatalf("near-me items = %d", len(list.Data.Items))
	}
	for i, item := range list.Data.Items {
		if item.Serviceable == nil || *item.Serviceable != (wantErrs[i] == nil) || item.Code != wantCodes[i] {
			t.Fatalf("item %d = %+v", i, item)
		}
		if wantErrs[i] != nil {
			place := doJSON(feastRouter(&feastRecordingStore{err: wantErrs[i]}), http.MethodPost, "/v1/food/orders",
				`{"address_id":"`+ctFeastHome.String()+`","payment_method":"upi"}`, ctCustomer, false)
			if pb := decodeFeastError(t, place); pb.Error.Code != item.Code || pb.Error.Message != item.Message {
				t.Fatalf("item %d says %s %q; POST /orders says %s %q", i, item.Code, item.Message, pb.Error.Code, pb.Error.Message)
			}
		}
	}

	var refusal feastErrorBody
	readFixture(t, "cart_item_post_422_out_of_range", &refusal)
	place := doJSON(feastRouter(&feastRecordingStore{err: postgres.ErrAddressOutOfRange}), http.MethodPost, "/v1/food/orders",
		`{"address_id":"`+ctFeastHome.String()+`","payment_method":"upi"}`, ctCustomer, false)
	if pb := decodeFeastError(t, place); place.Code != http.StatusUnprocessableEntity || pb.Error != refusal.Error {
		t.Fatalf("cart refusal %+v, POST /orders %d %+v", refusal.Error, place.Code, pb.Error)
	}
}

// Every variant and add-on price in the menu fixture carries integer paise
// equal to its rupee price times 100.
func TestFeastMenuFixturePaise(t *testing.T) {
	type priced struct {
		Price      float64 `json:"price"`
		PricePaise int64   `json:"price_paise"`
	}
	var menu struct {
		Data struct {
			Categories []struct {
				Items []struct {
					Variants    *[]priced `json:"variants"`
					AddonGroups *[]struct {
						Addons []priced `json:"addons"`
					} `json:"addon_groups"`
				} `json:"items"`
			} `json:"categories"`
		} `json:"data"`
	}
	readFixture(t, "restaurant_menu_get_200", &menu)
	checked := 0
	check := func(p priced) {
		if float64(p.PricePaise) != math.Round(p.Price*100) {
			t.Fatalf("price %v price_paise %d", p.Price, p.PricePaise)
		}
		checked++
	}
	for _, c := range menu.Data.Categories {
		for _, item := range c.Items {
			if item.Variants == nil || item.AddonGroups == nil {
				t.Fatal("a customer menu item lacks variants or addon_groups")
			}
			for _, v := range *item.Variants {
				check(v)
			}
			for _, g := range *item.AddonGroups {
				for _, a := range g.Addons {
					check(a)
				}
			}
		}
	}
	if checked != 2 {
		t.Fatalf("checked %d prices, want 2", checked)
	}
}

func readFixture(t *testing.T, name string, dst any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}
