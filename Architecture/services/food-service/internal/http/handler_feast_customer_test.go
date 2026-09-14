package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// feastRecordingStore records what the customer discovery and cart routes
// pass down, and answers with fixed data or err.
type feastRecordingStore struct {
	service.Store
	err           error
	unserviceable error
	listCalls     int
	filter        postgres.RestaurantFilter
	detailCalls   int
	detailNear    *postgres.GeoPoint
	added         []postgres.AddCartItemInput
}

func (f *feastRecordingStore) ListRestaurants(_ context.Context, filter postgres.RestaurantFilter) ([]postgres.RestaurantSummary, error) {
	f.listCalls++
	f.filter = filter
	r := postgres.RestaurantSummary{ID: ctRestaurant, Name: "Test Kitchen", Cuisines: []string{}}
	if filter.Near != nil {
		serviceable := f.unserviceable == nil
		r.Serviceable, r.Unserviceable = &serviceable, f.unserviceable
	}
	return []postgres.RestaurantSummary{r}, nil
}

func (f *feastRecordingStore) GetRestaurant(_ context.Context, id uuid.UUID, near *postgres.GeoPoint) (*postgres.RestaurantDetail, error) {
	f.detailCalls++
	f.detailNear = near
	return &postgres.RestaurantDetail{RestaurantSummary: postgres.RestaurantSummary{ID: id, Name: "Test Kitchen", Cuisines: []string{}}}, nil
}

func (f *feastRecordingStore) AddCartItem(_ context.Context, _ uuid.UUID, in postgres.AddCartItemInput) (*postgres.Cart, error) {
	f.added = append(f.added, in)
	if f.err != nil {
		return nil, f.err
	}
	return &postgres.Cart{ID: ctCart, UserID: ctCustomer, Items: []postgres.CartItem{}}, nil
}

func (f *feastRecordingStore) PlaceOrder(context.Context, uuid.UUID, postgres.PlaceOrderInput, string) (*postgres.Order, error) {
	return nil, f.err
}

func feastRouter(st service.Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.New(st)).RegisterRoutes(router)
	return router
}

type feastErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Field string `json:"field"`
		} `json:"details"`
	} `json:"error"`
}

func decodeFeastError(t *testing.T, rec *httptest.ResponseRecorder) feastErrorBody {
	t.Helper()
	var b feastErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("error body %s: %v", rec.Body.String(), err)
	}
	return b
}

func TestNearCoordinatesValidation(t *testing.T) {
	detail := "/v1/food/restaurants/" + ctRestaurant.String()
	cases := []struct{ path, code, field string }{
		{"/v1/food/restaurants?lat=12.9", "FOOD_LOCATION_REQUIRED", "lng"},
		{"/v1/food/restaurants?lng=77.6", "FOOD_LOCATION_REQUIRED", "lat"},
		{"/v1/food/restaurants?lat=abc&lng=77.6", "FOOD_COORDINATES_OUT_OF_RANGE", "lat"},
		{"/v1/food/restaurants?lat=&lng=77.6", "FOOD_COORDINATES_OUT_OF_RANGE", "lat"},
		{"/v1/food/restaurants?lat=NaN&lng=77.6", "FOOD_COORDINATES_OUT_OF_RANGE", "lat"},
		{"/v1/food/restaurants?lat=90.0001&lng=77.6", "FOOD_COORDINATES_OUT_OF_RANGE", "lat"},
		{"/v1/food/restaurants?lat=12.9&lng=-180.5", "FOOD_COORDINATES_OUT_OF_RANGE", "lng"},
		{"/v1/food/restaurants?lat=12.9&lng=Inf", "FOOD_COORDINATES_OUT_OF_RANGE", "lng"},
		{detail + "?lat=-91&lng=77.6", "FOOD_COORDINATES_OUT_OF_RANGE", "lat"},
		{detail + "?lat=12.9", "FOOD_LOCATION_REQUIRED", "lng"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			st := &feastRecordingStore{}
			rec := doJSON(feastRouter(st), http.MethodGet, tc.path, ``, ctCustomer, false)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
			}
			b := decodeFeastError(t, rec)
			if b.Error.Code != tc.code || b.Error.Details.Field != tc.field {
				t.Fatalf("error = %+v, want %s on %s", b.Error, tc.code, tc.field)
			}
			if st.listCalls+st.detailCalls != 0 {
				t.Fatal("an invalid request reached the store")
			}
		})
	}
}

func TestNearCoordinatesReachTheStore(t *testing.T) {
	st := &feastRecordingStore{}
	router := feastRouter(st)
	rec := doJSON(router, http.MethodGet, "/v1/food/restaurants?q=dosa&city=Bengaluru&limit=5&lat=12.9716&lng=-77.5946", ``, ctCustomer, false)
	if rec.Code != http.StatusOK || st.filter.Near == nil || *st.filter.Near != (postgres.GeoPoint{Lat: 12.9716, Lng: -77.5946}) ||
		st.filter.Query != "dosa" || st.filter.City != "Bengaluru" || st.filter.Limit != 5 {
		t.Fatalf("status %d filter %+v near %v", rec.Code, st.filter, st.filter.Near)
	}
	rec = doJSON(router, http.MethodGet, "/v1/food/restaurants?q=dosa", ``, ctCustomer, false)
	if rec.Code != http.StatusOK || st.filter.Near != nil || strings.Contains(rec.Body.String(), "serviceable") {
		t.Fatalf("no coordinates: status %d near %v body %s", rec.Code, st.filter.Near, rec.Body.String())
	}
	detail := "/v1/food/restaurants/" + ctRestaurant.String()
	rec = doJSON(router, http.MethodGet, detail+"?lat=90&lng=180", ``, ctCustomer, false)
	if rec.Code != http.StatusOK || st.detailNear == nil || *st.detailNear != (postgres.GeoPoint{Lat: 90, Lng: 180}) {
		t.Fatalf("detail: status %d near %v", rec.Code, st.detailNear)
	}
	rec = doJSON(router, http.MethodGet, detail, ``, ctCustomer, false)
	if rec.Code != http.StatusOK || st.detailNear != nil {
		t.Fatalf("detail without coordinates: status %d near %v", rec.Code, st.detailNear)
	}
}

func TestAddToCartDeliveryPoint(t *testing.T) {
	item := `"menu_item_id":"` + ctMenuItem.String() + `"`
	address := uuid.New()
	refused := []struct {
		name, body  string
		status      int
		code, field string
	}{
		{"lat without lng", `{` + item + `,"lat":12.9}`, http.StatusUnprocessableEntity, "FOOD_LOCATION_REQUIRED", "lng"},
		{"lng out of range", `{` + item + `,"lat":12.9,"lng":200}`, http.StatusUnprocessableEntity, "FOOD_COORDINATES_OUT_OF_RANGE", "lng"},
		{"address and coordinates", `{` + item + `,"address_id":"` + address.String() + `","lat":12.9,"lng":77.6}`,
			http.StatusUnprocessableEntity, "FOOD_ADDRESS_INVALID", "address_id"},
		{"malformed address id", `{` + item + `,"address_id":"nope"}`, http.StatusBadRequest, "INVALID_ADDRESS", ""},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			st := &feastRecordingStore{}
			rec := doJSON(feastRouter(st), http.MethodPost, "/v1/food/cart/items", tc.body, ctCustomer, false)
			b := decodeFeastError(t, rec)
			if rec.Code != tc.status || b.Error.Code != tc.code || b.Error.Details.Field != tc.field || len(st.added) != 0 {
				t.Fatalf("status %d error %+v added %d", rec.Code, b.Error, len(st.added))
			}
		})
	}

	st := &feastRecordingStore{}
	router := feastRouter(st)
	for _, body := range []string{
		`{` + item + `,"lat":12.9,"lng":77.6}`,
		`{` + item + `,"address_id":"` + address.String() + `"}`,
		`{` + item + `,"quantity":2}`,
	} {
		if rec := doJSON(router, http.MethodPost, "/v1/food/cart/items", body, ctCustomer, false); rec.Code != http.StatusCreated {
			t.Fatalf("%s: status %d body %s", body, rec.Code, rec.Body.String())
		}
	}
	if len(st.added) != 3 ||
		st.added[0].Near == nil || *st.added[0].Near != (postgres.GeoPoint{Lat: 12.9, Lng: 77.6}) || st.added[0].AddressID != nil ||
		st.added[1].AddressID == nil || *st.added[1].AddressID != address || st.added[1].Near != nil ||
		st.added[2].AddressID != nil || st.added[2].Near != nil || st.added[2].Quantity != 2 {
		t.Fatalf("store inputs = %+v", st.added)
	}
}

// Add-to-cart and the restaurant list name a refusal with exactly the status,
// code and message POST /orders answers for it.
func TestServiceabilityRefusalsAnswerLikePlaceOrder(t *testing.T) {
	for _, sentinel := range []error{postgres.ErrAddressOutOfRange, postgres.ErrRestaurantOutsideHours, postgres.ErrRestaurantNotAccepting,
		postgres.ErrAddressLocationRequired, postgres.ErrRestaurantLocationMissing} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			router := feastRouter(&feastRecordingStore{err: sentinel, unserviceable: sentinel})
			place := doJSON(router, http.MethodPost, "/v1/food/orders", `{"address_id":"`+uuid.NewString()+`","payment_method":"upi"}`, ctCustomer, false)
			add := doJSON(router, http.MethodPost, "/v1/food/cart/items", `{"menu_item_id":"`+ctMenuItem.String()+`","lat":12.9,"lng":77.6}`, ctCustomer, false)
			pb, ab := decodeFeastError(t, place), decodeFeastError(t, add)
			if place.Code != http.StatusUnprocessableEntity || add.Code != place.Code || ab.Error.Code != pb.Error.Code || ab.Error.Message != pb.Error.Message {
				t.Fatalf("place %d %+v, add %d %+v", place.Code, pb.Error, add.Code, ab.Error)
			}

			list := doJSON(router, http.MethodGet, "/v1/food/restaurants?lat=12.9&lng=77.6", ``, ctCustomer, false)
			var lb struct {
				Data struct {
					Items []struct {
						Serviceable *bool  `json:"serviceable"`
						Code        string `json:"unserviceable_reason_code"`
						Message     string `json:"unserviceable_message"`
					} `json:"items"`
				} `json:"data"`
			}
			if err := json.Unmarshal(list.Body.Bytes(), &lb); err != nil || len(lb.Data.Items) != 1 {
				t.Fatalf("list body %s: %v", list.Body.String(), err)
			}
			got := lb.Data.Items[0]
			if got.Serviceable == nil || *got.Serviceable || got.Code != pb.Error.Code || got.Message != pb.Error.Message {
				t.Fatalf("list item %+v, POST /orders %+v", got, pb.Error)
			}
		})
	}

	// The answers add-to-cart gave before are unchanged.
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{pgx.ErrNoRows, http.StatusBadRequest, "FOOD_CART_ADD_FAILED"},
		{errors.New("menu item is unavailable"), http.StatusBadRequest, "FOOD_CART_ADD_FAILED"},
		{postgres.ErrCartRestaurantConflict, http.StatusConflict, "FOOD_CART_RESTAURANT_CONFLICT"},
		{postgres.ErrAddonInvalid, http.StatusUnprocessableEntity, "FOOD_CART_ADDON_INVALID"},
		{postgres.ErrCartAddressNotFound, http.StatusNotFound, "FOOD_NOT_FOUND"},
	} {
		rec := doJSON(feastRouter(&feastRecordingStore{err: tc.err}), http.MethodPost, "/v1/food/cart/items",
			`{"menu_item_id":"`+ctMenuItem.String()+`"}`, ctCustomer, false)
		if b := decodeFeastError(t, rec); rec.Code != tc.status || b.Error.Code != tc.code {
			t.Fatalf("%v: status %d code %s, want %d %s", tc.err, rec.Code, b.Error.Code, tc.status, tc.code)
		}
	}
}
