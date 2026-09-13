package http

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Golden contract fixtures for B5a: the scoped realtime token, the (newly
// pinned) capabilities shape, the rider location response, the rider's
// assignment with pickup_code, and the order detail with delivery_code.
// Regenerate deliberately with UPDATE_CONTRACTS=1 and review the diff.

var (
	ctRtStranger      = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0020")
	ctRtAssignment    = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0021")
	ctRtAssignment2   = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0022")
	ctRtLocation      = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0023")
	ctRtRiderAssigned = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0024")
	ctRtNow           = time.Date(2026, 9, 13, 6, 30, 0, 0, time.UTC)
)

type fixedRealtimeSigner struct{}

func (fixedRealtimeSigner) Sign(string, []string) (string, error) { return "test-realtime-token", nil }

type discardPublisher struct{}

func (discardPublisher) Publish(context.Context, string, string, any) error { return nil }

type realtimeContractStore struct{ service.Store }

func (realtimeContractStore) GetOrder(_ context.Context, user, order uuid.UUID) (*postgres.Order, error) {
	if user != ctCustomer || order != ctOrder {
		return nil, pgx.ErrNoRows
	}
	return &postgres.Order{
		ID: ctOrder, OrderNumber: "FG1000000000002", UserID: ctCustomer, RestaurantID: ctRestaurant, RestaurantName: "Test Kitchen",
		Status: "OUT_FOR_DELIVERY", PaymentStatus: "CAPTURED", PaymentMethod: "ONLINE",
		Totals:                postgres.PriceBreakdown{ItemSubtotal: 250, DeliveryFee: 29, PlatformFee: 5, TaxTotal: 17.62, FinalAmount: 301.62},
		EstimatedDeliveryMins: 29, PlacedAt: ctTime,
		History: []postgres.OrderStatusHistory{
			{FromStatus: "PICKED_UP", ToStatus: "OUT_FOR_DELIVERY", Reason: "delivery partner arrived at customer", CreatedAt: ctTime},
		},
		DeliveryCode: "7390",
		ETAAt:        "2026-09-13T06:52:00Z", ETASource: "google",
	}, nil
}

func (realtimeContractStore) ListPartnerRestaurants(_ context.Context, user uuid.UUID) ([]postgres.PartnerRestaurant, error) {
	if user != ctOwner {
		return []postgres.PartnerRestaurant{}, nil
	}
	return []postgres.PartnerRestaurant{{ID: ctRestaurant, OwnerUserID: ctOwner, Name: "Test Kitchen", Status: "ACTIVE", City: "Bengaluru", CreatedAt: ctTime}}, nil
}

func (realtimeContractStore) GetDeliveryPartner(_ context.Context, user uuid.UUID) (*postgres.DeliveryPartner, error) {
	switch user {
	case ctRider, ctOwner, ctRtRiderAssigned:
		return &postgres.DeliveryPartner{ID: ctPartner, UserID: user, FullName: "Test Rider", Status: "ACTIVE", IsOnline: true, CreatedAt: ctTime}, nil
	}
	return nil, pgx.ErrNoRows
}

func (realtimeContractStore) GetCurrentDeliveryAssignment(_ context.Context, user uuid.UUID) (*postgres.DeliveryAssignment, error) {
	partner := ctPartner
	a := &postgres.DeliveryAssignment{
		ID: ctRtAssignment, OrderID: ctOrder, OrderNumber: "FG1000000000002", RestaurantName: "Test Kitchen", RestaurantID: ctRestaurant,
		DeliveryPartnerID: &partner, OrderStatus: "DELIVERY_ASSIGNED", DeliveryFee: 29, DeliveryPartnerPayout: 23.2, CreatedAt: ctTime,
	}
	switch user {
	case ctRider:
		a.Status, a.PickupCode = "ACCEPTED", "4821"
	case ctRtRiderAssigned:
		a.Status = "ASSIGNED"
	default:
		return nil, pgx.ErrNoRows
	}
	return a, nil
}

func (realtimeContractStore) UpdateDeliveryLocation(context.Context, uuid.UUID, postgres.LocationUpdate) (*postgres.DeliveryLocationResult, error) {
	accuracy, heading := 8.5, 90.0
	return &postgres.DeliveryLocationResult{
		ID: ctRtLocation, DeliveryPartnerID: ctPartner, AssignmentID: ctRtAssignment,
		AssignmentIDs: []uuid.UUID{ctRtAssignment, ctRtAssignment2},
		Latitude:      12.9716, Longitude: 77.5946, AccuracyMeters: &accuracy, Heading: &heading,
		RecordedAt: "2026-09-13 06:30:00+00", RecordedAtTime: ctRtNow,
		Frames: []postgres.RiderLocationFrame{{OrderID: ctOrder, AssignmentID: ctRtAssignment}},
	}, nil
}

func realtimeRouter(wired bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.New(realtimeContractStore{})
	if wired {
		svc.WithRealtime(discardPublisher{}, fixedRealtimeSigner{}).WithRealtimeClock(func() time.Time { return ctRtNow })
	}
	router := gin.New()
	New(svc).RegisterRoutes(router)
	return router
}

// realtimeFixtures lists the fixtures this file owns, for the OTP scan.
var realtimeFixtures = []string{
	"realtime_token_post_200_order", "realtime_token_post_200_restaurant", "realtime_token_post_200_delivery",
	"realtime_token_post_404_foreign_order", "realtime_token_post_404_foreign_restaurant", "realtime_token_post_404_not_delivery_partner",
	"realtime_token_post_422_scope_invalid", "realtime_token_post_422_id_required", "realtime_token_post_422_id_invalid",
	"realtime_token_post_422_id_not_allowed", "realtime_token_post_400_invalid_body", "realtime_token_post_503_not_configured",
	"me_capabilities_get_200_customer", "me_capabilities_get_200_all_roles",
	"delivery_location_post_200", "delivery_assignment_current_get_200_accepted", "delivery_assignment_current_get_200_assigned",
	"order_get_200_out_for_delivery",
}

func TestRealtimeContracts(t *testing.T) {
	const tokenPath = "/v1/food/realtime/token"
	cases := []struct {
		fixture string
		method  string
		path    string
		body    string
		user    uuid.UUID
		admin   bool
		wired   bool
		status  int
	}{
		{"realtime_token_post_200_order", http.MethodPost, tokenPath, `{"scope":"order","id":"` + ctOrder.String() + `"}`, ctCustomer, false, true, http.StatusOK},
		{"realtime_token_post_200_restaurant", http.MethodPost, tokenPath, `{"scope":"restaurant","id":"` + ctRestaurant.String() + `"}`, ctOwner, false, true, http.StatusOK},
		{"realtime_token_post_200_delivery", http.MethodPost, tokenPath, `{"scope":"delivery"}`, ctRider, false, true, http.StatusOK},
		{"realtime_token_post_404_foreign_order", http.MethodPost, tokenPath, `{"scope":"order","id":"` + ctOrder.String() + `"}`, ctRtStranger, false, true, http.StatusNotFound},
		{"realtime_token_post_404_foreign_restaurant", http.MethodPost, tokenPath, `{"scope":"restaurant","id":"` + ctRestaurant.String() + `"}`, ctRtStranger, false, true, http.StatusNotFound},
		{"realtime_token_post_404_not_delivery_partner", http.MethodPost, tokenPath, `{"scope":"delivery"}`, ctCustomer, false, true, http.StatusNotFound},
		{"realtime_token_post_422_scope_invalid", http.MethodPost, tokenPath, `{"scope":"admin"}`, ctCustomer, false, true, http.StatusUnprocessableEntity},
		{"realtime_token_post_422_id_required", http.MethodPost, tokenPath, `{"scope":"order"}`, ctCustomer, false, true, http.StatusUnprocessableEntity},
		{"realtime_token_post_422_id_invalid", http.MethodPost, tokenPath, `{"scope":"order","id":"not-a-uuid"}`, ctCustomer, false, true, http.StatusUnprocessableEntity},
		{"realtime_token_post_422_id_not_allowed", http.MethodPost, tokenPath, `{"scope":"delivery","id":"` + ctOrder.String() + `"}`, ctRider, false, true, http.StatusUnprocessableEntity},
		{"realtime_token_post_400_invalid_body", http.MethodPost, tokenPath, `scope=order`, ctCustomer, false, true, http.StatusBadRequest},
		{"realtime_token_post_503_not_configured", http.MethodPost, tokenPath, `{"scope":"order","id":"` + ctOrder.String() + `"}`, ctCustomer, false, false, http.StatusServiceUnavailable},
		{"me_capabilities_get_200_customer", http.MethodGet, "/v1/food/me/capabilities", ``, ctCustomer, false, true, http.StatusOK},
		{"me_capabilities_get_200_all_roles", http.MethodGet, "/v1/food/me/capabilities", ``, ctOwner, true, true, http.StatusOK},
		{"delivery_location_post_200", http.MethodPost, "/v1/food/delivery/location",
			`{"latitude":12.9716,"longitude":77.5946,"accuracy_meters":8.5,"heading":90}`, ctRider, false, true, http.StatusOK},
		{"delivery_assignment_current_get_200_accepted", http.MethodGet, "/v1/food/delivery/assignments/current", ``, ctRider, false, true, http.StatusOK},
		{"delivery_assignment_current_get_200_assigned", http.MethodGet, "/v1/food/delivery/assignments/current", ``, ctRtRiderAssigned, false, true, http.StatusOK},
		{"order_get_200_out_for_delivery", http.MethodGet, "/v1/food/orders/" + ctOrder.String(), ``, ctCustomer, false, true, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			rec := doJSON(realtimeRouter(tc.wired), tc.method, tc.path, tc.body, tc.user, tc.admin)
			assertContract(t, rec, tc.status, tc.fixture)
		})
	}
}

// Only the accepted assignment may show pickup_code and only the order on its
// way may show delivery_code; no other fixture here carries either key.
func TestRealtimeFixturesShowCodesOnlyInsideTheirWindows(t *testing.T) {
	for _, name := range realtimeFixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body := string(raw)
		if strings.Contains(body, `"pickup_code"`) != (name == "delivery_assignment_current_get_200_accepted") {
			t.Fatalf("%s: pickup_code presence is wrong", name)
		}
		if strings.Contains(body, `"delivery_code"`) != (name == "order_get_200_out_for_delivery") {
			t.Fatalf("%s: delivery_code presence is wrong", name)
		}
	}
}
