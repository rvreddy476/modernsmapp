package service

import (
	"context"
	"errors"
	"github.com/atpost/food-service/internal/batchroute"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"testing"
)

type routeReaderFake struct {
	Store
	allowed bool
	reads   int
	revoke  bool
}

func (f *routeReaderFake) GetAcceptedBatchRouteInput(context.Context, uuid.UUID, uuid.UUID) (*postgres.BatchRouteInput, error) {
	f.reads++
	if !f.allowed || f.revoke && f.reads > 1 {
		return nil, pgx.ErrNoRows
	}
	return &postgres.BatchRouteInput{PickupLat: 17.37, PickupLng: 78.48, Stops: []postgres.BatchRouteStop{{OrderID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Latitude: 17.36, Longitude: 78.47}, {OrderID: uuid.MustParse("00000000-0000-0000-0000-000000000002"), Latitude: 17.38, Longitude: 78.49}}}, nil
}

type previewFake struct{ calls int }

func (f *previewFake) Preview(_ context.Context, in batchroute.Input) (batchroute.Suggestion, error) {
	f.calls++
	return batchroute.Suggestion{Advisory: true, OrderIDs: []string{in.Stops[1].OrderID, in.Stops[0].OrderID}}, nil
}
func TestRoutePreviewChecksAccessBeforeAndAfterGoogle(t *testing.T) {
	for _, tc := range []struct {
		name            string
		allowed, revoke bool
		wantCalls       int
		wantErr         bool
	}{{"stranger", false, false, 0, true}, {"accepted partner", true, false, 1, false}, {"cancelled during planning", true, true, 1, true}} {
		t.Run(tc.name, func(t *testing.T) {
			store := &routeReaderFake{allowed: tc.allowed, revoke: tc.revoke}
			google := &previewFake{}
			svc := New(store).WithBatchRoutePreview(google)
			_, err := svc.PreviewAcceptedBatchRoute(context.Background(), uuid.New(), uuid.New())
			if (err != nil) != tc.wantErr || google.calls != tc.wantCalls {
				t.Fatalf("err=%v calls=%d", err, google.calls)
			}
			if tc.wantErr && !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal(err)
			}
		})
	}
}
