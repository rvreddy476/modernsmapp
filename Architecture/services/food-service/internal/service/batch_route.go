package service

import (
	"context"
	"errors"
	"reflect"

	"github.com/atpost/food-service/internal/batchroute"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type BatchRoutePreviewer interface {
	Preview(context.Context, batchroute.Input) (batchroute.Suggestion, error)
}
type batchRouteReader interface {
	GetAcceptedBatchRouteInput(context.Context, uuid.UUID, uuid.UUID) (*postgres.BatchRouteInput, error)
}

func (s *Service) WithBatchRoutePreview(p BatchRoutePreviewer) *Service {
	s.batchRoutePreview = p
	return s
}

// This never calls CreateBatch, CreateDeliveryOffer or an assignment mutation.
func (s *Service) PreviewAcceptedBatchRoute(ctx context.Context, userID, orderID uuid.UUID) (batchroute.Suggestion, error) {
	reader, ok := s.store.(batchRouteReader)
	if !ok {
		return batchroute.Suggestion{}, batchroute.ErrUnavailable
	}
	input, err := reader.GetAcceptedBatchRouteInput(ctx, userID, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return batchroute.Suggestion{}, pgx.ErrNoRows
		}
		return batchroute.Suggestion{}, batchroute.ErrUnavailable
	}
	if input == nil {
		return batchroute.Suggestion{}, pgx.ErrNoRows
	}
	if s.batchRoutePreview == nil {
		return batchroute.Suggestion{}, batchroute.ErrUnavailable
	}
	request := batchroute.Input{Pickup: batchroute.Point{Latitude: input.PickupLat, Longitude: input.PickupLng}}
	for _, stop := range input.Stops {
		request.Stops = append(request.Stops, batchroute.Stop{OrderID: stop.OrderID.String(), Point: batchroute.Point{Latitude: stop.Latitude, Longitude: stop.Longitude}})
	}
	result, err := s.batchRoutePreview.Preview(ctx, request)
	if err != nil {
		return batchroute.Suggestion{}, err
	}
	// Acceptance/cancellation can change while Google is planning the route.
	current, err := reader.GetAcceptedBatchRouteInput(ctx, userID, orderID)
	if err != nil || !reflect.DeepEqual(input, current) {
		return batchroute.Suggestion{}, pgx.ErrNoRows
	}
	return result, nil
}
