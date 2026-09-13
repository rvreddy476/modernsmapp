package foodevents

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/google/uuid"
)

func TestForTransition(t *testing.T) {
	cases := []struct{ from, to, want string }{
		{orderstate.PaymentPending, orderstate.Confirmed, OrderConfirmed},
		{orderstate.Placed, orderstate.PaymentPending, ""},
		{orderstate.Confirmed, orderstate.Preparing, OrderRestaurantAccepted},
		{orderstate.Preparing, orderstate.ReadyForPickup, OrderReadyForPickup},
		{orderstate.ReadyForPickup, orderstate.DeliveryAssigning, ""},
		{orderstate.Confirmed, orderstate.RestaurantRejected, OrderRestaurantRejected},
		{orderstate.DeliveryAssigning, orderstate.DeliveryAssigned, DeliveryAssigned},
		{orderstate.DeliveryAssigned, orderstate.DeliveryAssigning, ""},
		{orderstate.DeliveryAssigned, orderstate.PickedUp, DeliveryPickedUp},
		{orderstate.PickedUp, orderstate.OutForDelivery, ""},
		{orderstate.OutForDelivery, orderstate.Delivered, DeliveryDelivered},
		{orderstate.Preparing, orderstate.CancelledByCustomer, OrderCancelled},
		{orderstate.Placed, orderstate.CancelledByRestaurant, OrderCancelled},
		{orderstate.OutForDelivery, orderstate.CancelledByAdmin, OrderCancelled},
		{orderstate.RestaurantRejected, orderstate.RefundPending, OrderRefundRequested},
		{orderstate.PaymentPending, orderstate.PaymentFailed, OrderPaymentFailed},
		{orderstate.RefundPending, orderstate.Refunded, OrderRefunded},
	}
	for _, tc := range cases {
		if got := ForTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("ForTransition(%s, %s) = %q, want %q", tc.from, tc.to, got, tc.want)
		}
	}
}

// The payload carries ids and statuses only: no amounts, names, addresses,
// free-text reasons or codes.
func TestOrderEventCarriesOnlyIDsAndStatus(t *testing.T) {
	h := OrderHeader{OrderID: uuid.New(), OrderNumber: "FG1000000000001", UserID: uuid.New(),
		RestaurantID: uuid.New(), RestaurantOwnerUserID: uuid.New(), Status: orderstate.Preparing}
	raw, err := Marshal(OrderRestaurantAccepted, NewOrderEvent(h, orderstate.Confirmed))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := "id,order_id,order_number,previous_status,restaurant_id,restaurant_owner_user_id,status,user_id"
	if strings.Join(keys, ",") != want {
		t.Fatalf("keys = %v, want %s", keys, want)
	}
	// A nil id is omitted as empty, never sent as the zero UUID.
	raw, _ = Marshal(OrderPlaced, NewOrderEvent(OrderHeader{OrderID: h.OrderID}, ""))
	if strings.Contains(string(raw), uuid.Nil.String()) {
		t.Fatalf("zero uuid in %s", raw)
	}
}

func TestMarshalRefusesAnyOTPKey(t *testing.T) {
	for _, data := range []any{
		map[string]any{"pickup_code": "1111"},
		map[string]any{"order": map[string]any{"delivery_code": "2222"}},
		[]any{map[string]any{"delivery_otp": "3333"}},
	} {
		if _, err := Marshal("food.order.x", data); !errors.Is(err, ErrPayloadCarriesOTP) {
			t.Fatalf("payload %v: err = %v", data, err)
		}
	}
}
