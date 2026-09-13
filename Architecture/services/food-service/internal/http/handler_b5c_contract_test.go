package http

import (
	"context"
	"fmt"
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

// Golden contract fixtures for B5c: the delivery code is entered by the rider
// (new route), the customer's old verify route is gone (410), and pickup needs
// an accepted job (409). The store's real behaviour behind each answer is
// pinned by internal/store/postgres/b5c_integration_test.go; this file pins the
// wire. Regenerate deliberately with UPDATE_CONTRACTS=1 and review the diff.

var (
	ctB5cAssignment          = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0040")
	ctB5cAssignmentLocked    = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0041")
	ctB5cAssignmentNotPicked = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0042")
	ctB5cSuspendedRider      = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0043")
	ctB5cOtherRider          = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0044")
)

const ctB5cDeliveryCode = "7390"

type b5cContractStore struct {
	service.Store
	// customerVerifyCalls counts store calls the gone customer route makes
	// (it must make none).
	calls int
}

func (f *b5cContractStore) RiderVerifyDeliveryCode(_ context.Context, rider, assignment uuid.UUID, code string) (*postgres.DeliveryVerification, error) {
	f.calls++
	switch {
	case rider == ctB5cSuspendedRider:
		return nil, postgres.ErrDeliveryPartnerNotActive
	case rider != ctRider:
		// Another rider's assignment does not exist for this caller.
		return nil, pgx.ErrNoRows
	case assignment == ctB5cAssignmentLocked:
		return nil, postgres.ErrDeliveryCodeLocked
	case assignment == ctB5cAssignmentNotPicked:
		return nil, fmt.Errorf("%w: assignment is ACCEPTED", postgres.ErrAssignmentNotReady)
	case assignment != ctB5cAssignment:
		return nil, pgx.ErrNoRows
	case code != ctB5cDeliveryCode:
		return nil, postgres.ErrDeliveryCodeInvalid
	}
	return &postgres.DeliveryVerification{OrderID: ctOrder, CustomerID: ctCustomer}, nil
}

func (f *b5cContractStore) VerifyPickupCode(_ context.Context, _, _ uuid.UUID, _ string) error {
	f.calls++
	return fmt.Errorf("%w: assignment is ASSIGNED", postgres.ErrAssignmentNotReady)
}

// The post-verify loyalty and referral hooks: the order lookup misses (no
// points) and the referral mark is a no-op.
func (f *b5cContractStore) GetOrder(context.Context, uuid.UUID, uuid.UUID) (*postgres.Order, error) {
	return nil, pgx.ErrNoRows
}

func (f *b5cContractStore) MarkReferralRewarded(context.Context, uuid.UUID, int) error { return nil }

func b5cRouter(st *b5cContractStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.New(st).WithRealtime(discardPublisher{}, nil)).RegisterRoutes(router)
	return router
}

var b5cFixtures = []string{
	"customer_verify_delivery_post_410_rider_enters_code",
	"delivery_verify_delivery_post_200",
	"delivery_verify_delivery_post_422_code_invalid",
	"delivery_verify_delivery_post_429_attempts_exceeded",
	"delivery_verify_delivery_post_409_not_picked_up",
	"delivery_verify_delivery_post_404_not_your_assignment",
	"delivery_verify_delivery_post_403_partner_not_active",
	"delivery_verify_delivery_post_400_invalid_body",
	"delivery_verify_delivery_post_400_invalid_assignment_id",
	"partner_verify_pickup_post_409_not_accepted",
}

func TestB5cDeliveryVerifyContracts(t *testing.T) {
	rider := func(id uuid.UUID) string {
		return "/v1/food/delivery/assignments/" + id.String() + "/verify-delivery"
	}
	good := `{"code":"` + ctB5cDeliveryCode + `"}`
	cases := []struct {
		fixture string
		path    string
		body    string
		user    uuid.UUID
		status  int
		// storeCalls is how many store verifies the request may reach.
		storeCalls int
	}{
		{"customer_verify_delivery_post_410_rider_enters_code", "/v1/food/orders/" + ctOrder.String() + "/verify-delivery", good, ctCustomer, http.StatusGone, 0},
		{"delivery_verify_delivery_post_200", rider(ctB5cAssignment), good, ctRider, http.StatusOK, 1},
		{"delivery_verify_delivery_post_422_code_invalid", rider(ctB5cAssignment), `{"code":"0000"}`, ctRider, http.StatusUnprocessableEntity, 1},
		{"delivery_verify_delivery_post_429_attempts_exceeded", rider(ctB5cAssignmentLocked), good, ctRider, http.StatusTooManyRequests, 1},
		{"delivery_verify_delivery_post_409_not_picked_up", rider(ctB5cAssignmentNotPicked), good, ctRider, http.StatusConflict, 1},
		{"delivery_verify_delivery_post_404_not_your_assignment", rider(ctB5cAssignment), good, ctB5cOtherRider, http.StatusNotFound, 1},
		{"delivery_verify_delivery_post_403_partner_not_active", rider(ctB5cAssignment), good, ctB5cSuspendedRider, http.StatusForbidden, 1},
		{"delivery_verify_delivery_post_400_invalid_body", rider(ctB5cAssignment), `{}`, ctRider, http.StatusBadRequest, 0},
		{"delivery_verify_delivery_post_400_invalid_assignment_id", "/v1/food/delivery/assignments/not-a-uuid/verify-delivery", good, ctRider, http.StatusBadRequest, 0},
		{"partner_verify_pickup_post_409_not_accepted", "/v1/food/partner/orders/" + ctOrder.String() + "/verify-pickup", `{"code":"4821"}`, ctOwner, http.StatusConflict, 1},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			st := &b5cContractStore{}
			rec := doJSON(b5cRouter(st), http.MethodPost, tc.path, tc.body, tc.user, false)
			assertContract(t, rec, tc.status, tc.fixture)
			if st.calls != tc.storeCalls {
				t.Fatalf("store verifies reached = %d, want %d", st.calls, tc.storeCalls)
			}
		})
	}
}

// The customer route never completes a delivery, whatever it is sent.
func TestCustomerVerifyDeliveryRouteNeverMarksDelivered(t *testing.T) {
	for _, body := range []string{`{"code":"` + ctB5cDeliveryCode + `"}`, `{}`, `garbage`} {
		st := &b5cContractStore{}
		rec := doJSON(b5cRouter(st), http.MethodPost, "/v1/food/orders/"+ctOrder.String()+"/verify-delivery", body, ctCustomer, false)
		if rec.Code != http.StatusGone || errorCode(t, rec) != "FOOD_DELIVERY_CODE_ENTERED_BY_RIDER" || st.calls != 0 {
			t.Fatalf("body %q: status %d code %s store calls %d (%s)", body, rec.Code, errorCode(t, rec), st.calls, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "DELIVERED") {
			t.Fatalf("customer route reports a delivery: %s", rec.Body.String())
		}
	}
}

// No B5c fixture carries a code key or the code value.
func TestB5cFixturesCarryNoCodes(t *testing.T) {
	for _, name := range b5cFixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body := string(raw)
		if strings.Contains(body, "pickup_code") || strings.Contains(body, "delivery_code") || strings.Contains(body, ctB5cDeliveryCode) {
			t.Fatalf("%s carries a code: %s", name, body)
		}
	}
}
