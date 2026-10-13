package http

import (
	"context"
	"github.com/atpost/food-service/internal/batchroute"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"testing"
)

type routeHTTPStore struct {
	service.Store
	owner   uuid.UUID
	orderID uuid.UUID
}

func (s *routeHTTPStore) GetAcceptedBatchRouteInput(_ context.Context, userID, orderID uuid.UUID) (*postgres.BatchRouteInput, error) {
	if userID != s.owner || orderID != s.orderID {
		return nil, pgx.ErrNoRows
	}
	return &postgres.BatchRouteInput{PickupLat: 17.37, PickupLng: 78.48, Stops: []postgres.BatchRouteStop{{OrderID: s.orderID, Latitude: 17.36, Longitude: 78.47}, {OrderID: uuid.Nil, Latitude: 17.38, Longitude: 78.49}}}, nil
}

type routeHTTPPreview struct {
	calls int
	busy  bool
}

func (p *routeHTTPPreview) Preview(_ context.Context, in batchroute.Input) (batchroute.Suggestion, error) {
	p.calls++
	if p.busy {
		return batchroute.Suggestion{}, batchroute.ErrBusy
	}
	return batchroute.Suggestion{Advisory: true, Source: "google_route_optimization", OrderIDs: []string{in.Stops[1].OrderID, in.Stops[0].OrderID}}, nil
}
func TestRoutePreviewAuthenticationAndNoStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner, order := uuid.New(), uuid.New()
	store := &routeHTTPStore{owner: owner, orderID: order}
	preview := &routeHTTPPreview{}
	router := gin.New()
	New(service.New(store).WithBatchRoutePreview(preview)).RegisterRoutes(router)
	for _, tc := range []struct {
		name, user, order string
		want              int
		busy              bool
	}{
		{"unauthenticated", "", order.String(), 401, false},
		{"stranger", uuid.NewString(), order.String(), 404, false},
		{"invalid id", owner.String(), "bad", 400, false},
		{"accepted partner", owner.String(), order.String(), 200, false},
		{"throttled", owner.String(), order.String(), 429, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preview.busy = tc.busy
			before := preview.calls
			request := httptest.NewRequest("POST", "/v1/food/delivery/orders/"+tc.order+"/batch/route-preview", nil)
			if tc.user != "" {
				request.Header.Set("X-User-Id", tc.user)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tc.want, recorder.Body.String())
			}
			if tc.want != http.StatusUnauthorized && recorder.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("route result may be cached by a proxy")
			}
			if tc.want == 429 && recorder.Header().Get("Retry-After") != "10" {
				t.Fatal("missing retry guidance")
			}
			if tc.want == 404 && preview.calls != before {
				t.Fatal("unauthorized request called Google")
			}
		})
	}
}
