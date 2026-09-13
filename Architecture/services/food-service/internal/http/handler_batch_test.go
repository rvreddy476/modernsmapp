package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// batchFakeStore answers the two batch lookups the way the real store does:
// the unrestricted lookup finds the batch for anyone, the partner lookup only
// for the partner assigned to the order.
type batchFakeStore struct {
	service.Store
	orderID      uuid.UUID
	assignedUser uuid.UUID
	batch        *postgres.DeliveryBatch
}

func (f *batchFakeStore) GetBatchForOrder(_ context.Context, orderID uuid.UUID) (*postgres.DeliveryBatch, error) {
	if orderID != f.orderID {
		return nil, pgx.ErrNoRows
	}
	return f.batch, nil
}

func (f *batchFakeStore) GetBatchForOrderForPartner(_ context.Context, userID, orderID uuid.UUID) (*postgres.DeliveryBatch, error) {
	if orderID != f.orderID || userID != f.assignedUser {
		return nil, pgx.ErrNoRows
	}
	return f.batch, nil
}

// TestGetBatchForOrder_OnlyAssignedPartnerOrAdmin is the batch IDOR fix: the
// batch detail (sibling order ids, sequence) is visible to the partner
// assigned to the order and to admins. Everyone else gets the same 404 a
// solo order gets, so existence does not leak.
func TestGetBatchForOrder_OnlyAssignedPartnerOrAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orderID := uuid.New()
	assigned := uuid.New()
	fake := &batchFakeStore{
		orderID:      orderID,
		assignedUser: assigned,
		batch: &postgres.DeliveryBatch{
			ID: uuid.New(), Status: "assigned",
			Members: []postgres.BatchMember{{OrderID: orderID, Sequence: 1}, {OrderID: uuid.New(), Sequence: 2}},
		},
	}
	router := gin.New()
	New(service.New(fake)).RegisterRoutes(router)

	cases := []struct {
		name   string
		userID uuid.UUID
		scopes string
		want   int
	}{
		{name: "stranger gets 404", userID: uuid.New(), want: http.StatusNotFound},
		{name: "customer-ish stranger with food scope gets 404", userID: uuid.New(), scopes: "profile food:read", want: http.StatusNotFound},
		{name: "assigned partner gets 200", userID: assigned, want: http.StatusOK},
		{name: "admin gets 200", userID: uuid.New(), scopes: "admin", want: http.StatusOK},
		{name: "superadmin gets 200", userID: uuid.New(), scopes: "superadmin", want: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/food/delivery/orders/"+orderID.String()+"/batch", nil)
			req.Header.Set("X-User-Id", tc.userID.String())
			if tc.scopes != "" {
				req.Header.Set("X-Scopes", tc.scopes)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
