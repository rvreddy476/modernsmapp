package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// lifecycleFakeStore returns a fixed error from each lifecycle writer.
type lifecycleFakeStore struct {
	service.Store
	err error
}

func (f *lifecycleFakeStore) CancelOrder(context.Context, uuid.UUID, uuid.UUID, string) (*postgres.Order, error) {
	return nil, f.err
}

func (f *lifecycleFakeStore) DeliveryUpdateAssignment(context.Context, uuid.UUID, uuid.UUID, string, string) (*postgres.DeliveryAssignment, error) {
	return nil, f.err
}

func (f *lifecycleFakeStore) PlaceOrder(context.Context, uuid.UUID, postgres.PlaceOrderInput, string) (*postgres.Order, error) {
	return nil, f.err
}

func (f *lifecycleFakeStore) PartnerUpdateOrderStatus(context.Context, uuid.UUID, uuid.UUID, string, string, string) (*postgres.Order, error) {
	return nil, f.err
}

func (f *lifecycleFakeStore) AddCartItem(context.Context, uuid.UUID, postgres.AddCartItemInput) (*postgres.Cart, error) {
	return nil, f.err
}

func TestLifecycleErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orderID := uuid.NewString()
	cases := []struct {
		name, method, path, body string
		err                      error
		status                   int
		code                     string
	}{
		{"cancel conflict", http.MethodPost, "/v1/food/orders/" + orderID + "/cancel", `{}`,
			fmt.Errorf("%w: order is no longer CONFIRMED", postgres.ErrOrderStatusConflict), http.StatusConflict, "FOOD_ORDER_STATUS_CONFLICT"},
		{"cancel not allowed", http.MethodPost, "/v1/food/orders/" + orderID + "/cancel", `{}`,
			fmt.Errorf("%w: x", postgres.ErrOrderTransitionNotAllowed), http.StatusConflict, "FOOD_ORDER_TRANSITION_NOT_ALLOWED"},
		{"bare picked-up", http.MethodPost, "/v1/food/delivery/assignments/" + orderID + "/picked-up", `{}`,
			postgres.ErrDeliveryOTPRequired, http.StatusConflict, "FOOD_DELIVERY_OTP_REQUIRED"},
		{"inactive partner", http.MethodPost, "/v1/food/delivery/assignments/" + orderID + "/accept", `{}`,
			postgres.ErrDeliveryPartnerNotActive, http.StatusForbidden, "FOOD_DELIVERY_PARTNER_NOT_ACTIVE"},
		{"partner status conflict", http.MethodPost, "/v1/food/partner/orders/" + orderID + "/mark-ready", `{}`,
			postgres.ErrOrderStatusConflict, http.StatusConflict, "FOOD_ORDER_STATUS_CONFLICT"},
		{"out of range", http.MethodPost, "/v1/food/orders", `{"address_id":"` + uuid.NewString() + `"}`,
			postgres.ErrAddressOutOfRange, http.StatusUnprocessableEntity, "FOOD_ADDRESS_OUT_OF_RANGE"},
		{"outside hours", http.MethodPost, "/v1/food/orders", `{"address_id":"` + uuid.NewString() + `"}`,
			postgres.ErrRestaurantOutsideHours, http.StatusUnprocessableEntity, "FOOD_RESTAURANT_OUTSIDE_HOURS"},
		{"address location", http.MethodPost, "/v1/food/orders", `{"address_id":"` + uuid.NewString() + `"}`,
			postgres.ErrAddressLocationRequired, http.StatusUnprocessableEntity, "FOOD_ADDRESS_LOCATION_REQUIRED"},
		{"restaurant location", http.MethodPost, "/v1/food/orders", `{"address_id":"` + uuid.NewString() + `"}`,
			postgres.ErrRestaurantLocationMissing, http.StatusUnprocessableEntity, "FOOD_RESTAURANT_LOCATION_MISSING"},
		{"not accepting", http.MethodPost, "/v1/food/orders", `{"address_id":"` + uuid.NewString() + `"}`,
			postgres.ErrRestaurantNotAccepting, http.StatusUnprocessableEntity, "FOOD_RESTAURANT_NOT_ACCEPTING"},
		{"foreign add-on", http.MethodPost, "/v1/food/cart/items",
			`{"menu_item_id":"` + uuid.NewString() + `","addons":[{"addon_id":"` + uuid.NewString() + `","quantity":1}]}`,
			fmt.Errorf("%w: not this item", postgres.ErrAddonInvalid), http.StatusUnprocessableEntity, "FOOD_CART_ADDON_INVALID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			New(service.New(&lifecycleFakeStore{err: tc.err})).RegisterRoutes(router)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User-Id", uuid.NewString())
			req.Header.Set("Idempotency-Key", uuid.NewString())
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.status, rec.Body.String())
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("body %s does not carry code %s", rec.Body.String(), tc.code)
			}
		})
	}
}
