package postgres

import "testing"

func TestBatchRoutePreviewPrivacyWindow(t *testing.T) {
	for _, tc := range []struct {
		owned             bool
		assignment, order string
		want              bool
	}{
		{true, "ACCEPTED", "DELIVERY_ASSIGNED", true}, {true, "ARRIVED_AT_RESTAURANT", "DELIVERY_ASSIGNED", true},
		{false, "ACCEPTED", "DELIVERY_ASSIGNED", false}, {true, "ASSIGNED", "DELIVERY_ASSIGNED", false},
		{true, "PICKED_UP", "OUT_FOR_DELIVERY", false}, {true, "CANCELLED", "CANCELLED_BY_ADMIN", false}, {true, "ACCEPTED", "CANCELLED_BY_ADMIN", false}, {true, "DELIVERED", "DELIVERED", false},
	} {
		if got := routePreviewVisible(tc.owned, tc.assignment, tc.order); got != tc.want {
			t.Fatalf("%+v got %v", tc, got)
		}
	}
}
