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
)

// Golden contract fixtures for rider navigation and money: the offer inbox
// with restaurant, drop area and payout; history with paise siblings and the
// city-only drop summary; earnings in paise. The two current-assignment
// fixtures (handler_realtime_contract_test.go) gain the same keys. Regenerate
// deliberately with UPDATE_CONTRACTS=1 and review the diff.

var (
	ctRnOffer               = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0025")
	ctRnOrderDelivered      = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0026")
	ctRnAssignmentDelivered = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0027")
	ctRnOrderCancelled      = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0028")
	ctRnAssignmentCancelled = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0029")
)

// ctRnDropSnapshot is a delivery_address_snapshot as PlaceOrder writes it,
// including the receiver's full name and phone that no fixture may carry.
const ctRnDropSnapshot = `{"id":"0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0099","label":"Home","receiver_name":"Asha Rao","phone":"+919812345678",` +
	`"address_line1":"42 Lake View Road","address_line2":"Flat 3B","landmark":"Opp. City Park","city":"Bengaluru",` +
	`"state":"Karnataka","country":"India","postal_code":"560038","latitude":12.978449,"longitude":77.640812}`

// ctRnPlaces is what the assignment column list reads beyond the original
// columns. The phone is 11 digits with a leading 0, as in the B8 fixtures.
func ctRnPlaces() postgres.AssignmentPlaces {
	lat, lng := 12.9716, 77.5946
	eta := time.Date(2026, 9, 13, 6, 52, 0, 0, time.UTC)
	return postgres.AssignmentPlaces{
		RestaurantLat: &lat, RestaurantLng: &lng, RestaurantAddressLine1: "1 Test Lane", RestaurantAddressLine2: "Near Test Park",
		RestaurantCity: "Bengaluru", RestaurantPhone: "08040000000",
		DeliverySnapshot: []byte(ctRnDropSnapshot), CustomerInstruction: "Ring the bell twice", ETAAt: &eta, ETASource: "google",
	}
}

type riderNavContractStore struct{ realtimeContractStore }

func (riderNavContractStore) ListMyPendingDeliveryOffers(_ context.Context, user uuid.UUID) ([]postgres.DeliveryOffer, error) {
	if user != ctRider {
		return []postgres.DeliveryOffer{}, nil
	}
	distance := 1.25
	return []postgres.DeliveryOffer{{ID: ctRnOffer, OrderID: ctOrder, DeliveryPartnerID: ctPartner, Status: "pending",
		DistanceKM: &distance, ExpiresAt: "2026-09-13 06:30:25+00", CreatedAt: "2026-09-13 06:30:00+00"}}, nil
}

func (riderNavContractStore) DeliveryOfferContexts(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.DeliveryOfferContext, error) {
	lat, lng, dLat, dLng, rLat, rLng := 12.9716, 77.5946, 12.978449, 77.640812, 12.9616, 77.5846
	recorded := ctRtNow.Add(-20 * time.Second)
	out := map[uuid.UUID]postgres.DeliveryOfferContext{}
	for _, id := range ids {
		out[id] = postgres.DeliveryOfferContext{OfferID: id, RestaurantID: ctRestaurant, RestaurantName: "Test Kitchen",
			RestaurantLat: &lat, RestaurantLng: &lng, RestaurantAddressLine1: "1 Test Lane", RestaurantCity: "Bengaluru",
			DropLat: &dLat, DropLng: &dLng, DropCity: "Bengaluru", PayoutPaise: 2320,
			RiderLat: &rLat, RiderLng: &rLng, RiderRecordedAt: &recorded}
	}
	return out, nil
}

func (riderNavContractStore) DeliveryHistory(_ context.Context, user uuid.UUID) ([]postgres.DeliveryAssignment, error) {
	if user != ctRider {
		return []postgres.DeliveryAssignment{}, nil
	}
	partner := ctPartner
	delivered := postgres.DeliveryAssignment{ID: ctRnAssignmentDelivered, OrderID: ctRnOrderDelivered, OrderNumber: "FG1000000000003",
		RestaurantName: "Test Kitchen", RestaurantID: ctRestaurant, DeliveryPartnerID: &partner, Status: "DELIVERED", OrderStatus: "DELIVERED",
		DeliveryFee: 29, DeliveryPartnerPayout: 23.2, CreatedAt: ctTime, DeliveryFeePaise: 2900, DeliveryPartnerPayoutPaise: 2320}
	delivered.FillRiderView(ctRnPlaces())
	cancelled := postgres.DeliveryAssignment{ID: ctRnAssignmentCancelled, OrderID: ctRnOrderCancelled, OrderNumber: "FG1000000000004",
		RestaurantName: "Test Kitchen", RestaurantID: ctRestaurant, DeliveryPartnerID: &partner, Status: "CANCELLED", OrderStatus: "CANCELLED_BY_ADMIN",
		DeliveryFee: 35.5, DeliveryPartnerPayout: 28.4, CreatedAt: ctTime, DeliveryFeePaise: 3550, DeliveryPartnerPayoutPaise: 2840}
	cancelled.FillRiderView(ctRnPlaces())
	return []postgres.DeliveryAssignment{delivered, cancelled}, nil
}

func (riderNavContractStore) DeliveryEarnings(context.Context, uuid.UUID) (map[string]any, error) {
	return postgres.DeliveryEarningsMap(2, 51.6, 5160, 14, 1234.56, 123456), nil
}

func riderNavRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.New(riderNavContractStore{}).
		WithRealtime(discardPublisher{}, fixedRealtimeSigner{}).
		WithRealtimeClock(func() time.Time { return ctRtNow }).
		WithDispatchConfig(5, 120*time.Second)
	router := gin.New()
	New(svc).RegisterRoutes(router)
	return router
}

func TestRiderNavigationContracts(t *testing.T) {
	for _, tc := range []struct{ fixture, path string }{
		{"delivery_offers_me_get_200", "/v1/food/delivery/offers/me"},
		{"delivery_history_get_200", "/v1/food/delivery/history"},
		{"delivery_earnings_get_200", "/v1/food/delivery/earnings"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			assertContract(t, doJSON(riderNavRouter(), http.MethodGet, tc.path, ``, ctRider, false), http.StatusOK, tc.fixture)
		})
	}
}

// The exact drop-off appears in one rider fixture only, the accepted
// assignment; no fixture carries the customer's full name or phone; the money
// fixtures carry their paise siblings.
func TestRiderFixturesShowTheDropOnlyInsideItsWindow(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return string(raw)
	}
	for name, wantDrop := range map[string]bool{
		"delivery_assignment_current_get_200_assigned": false,
		"delivery_assignment_current_get_200_accepted": true,
		"delivery_offers_me_get_200":                   false,
		"delivery_history_get_200":                     false,
		"delivery_earnings_get_200":                    false,
	} {
		body := read(name)
		for _, s := range []string{"42 Lake View Road", "Flat 3B", "Opp. City Park", "560038", "Asha", "Ring the bell twice", "12.978449", `"drop":`, "drop_url"} {
			if strings.Contains(body, s) != wantDrop {
				t.Fatalf("%s: %q present = %v, want %v", name, s, !wantDrop, wantDrop)
			}
		}
		for _, s := range []string{"Rao", "9812345678", "receiver_name"} {
			if strings.Contains(body, s) {
				t.Fatalf("%s carries %q", name, s)
			}
		}
	}
	for name, wants := range map[string][]string{
		"delivery_offers_me_get_200":                   {`"drop_area"`, `"latitude": 12.98,`, `"payout_paise": 2320`, `"trip_distance_meters"`, `"distance_to_restaurant_meters"`},
		"delivery_assignment_current_get_200_assigned": {`"pickup_url"`, `"payout_paise": 2320`, `"delivery_fee_paise": 2900`},
		"delivery_assignment_current_get_200_accepted": {`"customer_first_name": "Asha"`, `"payout_paise": 2320`},
		"delivery_history_get_200":                     {`"drop_summary"`, `"delivery_partner_payout_paise": 2840`, `"delivery_fee_paise": 3550`},
		"delivery_earnings_get_200":                    {`"total_earnings_paise": 123456`, `"earnings_today_paise": 5160`},
	} {
		body := read(name)
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Fatalf("%s lacks %s", name, want)
			}
		}
	}
}
