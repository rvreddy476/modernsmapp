package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/atpost/food-service/internal/routing"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// WithRouter wires the routing chain (routing.Cache over routing.Fallback)
// that rider pings price ETAs with. Unwired, the default haversine estimate
// is used.
func (s *Service) WithRouter(r routing.Router) *Service {
	s.router = r
	return s
}

func (s *Service) routes() routing.Router {
	if s.router != nil {
		return s.router
	}
	return routing.Haversine{}
}

// orderETA is one recomputed ETA.
type orderETA struct {
	At     time.Time
	Source string
}

// etaForJob is the B6 formula for an order a rider holds, from the rider's
// position at `now`:
//
//	before pickup (assignment ACCEPTED or ARRIVED_AT_RESTAURANT):
//	    pickup_at = max(now + ride(rider -> restaurant), food_ready_at)
//	                (the ride is 0 once ARRIVED_AT_RESTAURANT; food_ready_at is
//	                 ignored once the order reached READY_FOR_PICKUP)
//	    eta_at    = pickup_at + ride(restaurant -> customer)
//	after pickup (assignment PICKED_UP or ARRIVED_AT_CUSTOMER):
//	    eta_at    = now + ride(rider -> customer)
//
// eta_source is "google" only when every ride it used came from Google, so
// the label never claims traffic awareness for a straight-line leg. False when
// the order has no coordinates to price.
func (s *Service) etaForJob(ctx context.Context, job postgres.ETAJob, rider routing.LatLng, now time.Time) (orderETA, bool) {
	if job.Customer == nil {
		return orderETA{}, false
	}
	source := routing.SourceGoogle
	ride := func(from, to routing.LatLng) (time.Duration, bool) {
		r, err := s.routes().Route(ctx, from, to)
		if err != nil {
			return 0, false
		}
		if r.Source != routing.SourceGoogle {
			source = routing.SourceHaversine
		}
		return r.Duration, true
	}

	switch job.AssignmentStatus {
	case "ACCEPTED", "ARRIVED_AT_RESTAURANT":
		if job.Restaurant == nil {
			return orderETA{}, false
		}
		pickupAt := now
		if job.AssignmentStatus == "ACCEPTED" {
			toRestaurant, ok := ride(rider, *job.Restaurant)
			if !ok {
				return orderETA{}, false
			}
			pickupAt = now.Add(toRestaurant)
		}
		if job.FoodReadyAt != nil && job.FoodReadyAt.After(pickupAt) {
			pickupAt = *job.FoodReadyAt
		}
		toCustomer, ok := ride(*job.Restaurant, *job.Customer)
		if !ok {
			return orderETA{}, false
		}
		return orderETA{At: pickupAt.Add(toCustomer), Source: source}, true
	case "PICKED_UP", "ARRIVED_AT_CUSTOMER":
		toCustomer, ok := ride(rider, *job.Customer)
		if !ok {
			return orderETA{}, false
		}
		return orderETA{At: now.Add(toCustomer), Source: source}, true
	}
	return orderETA{}, false
}

// recomputeETAs prices every order the ping claimed (concurrently: a batch
// run holds up to three, each bounded by the routing timeout) and writes each
// back under its claim. Returns the ETAs that were written, by order.
func (s *Service) recomputeETAs(ctx context.Context, res *postgres.DeliveryLocationResult) map[uuid.UUID]orderETA {
	if len(res.ETAJobs) == 0 {
		return nil
	}
	rider := routing.LatLng{Lat: res.Latitude, Lng: res.Longitude}
	now := res.RecordedAtTime
	if now.IsZero() {
		now = time.Now()
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = make(map[uuid.UUID]orderETA, len(res.ETAJobs))
	)
	for _, job := range res.ETAJobs {
		wg.Add(1)
		go func(job postgres.ETAJob) {
			defer wg.Done()
			eta, ok := s.etaForJob(ctx, job, rider, now)
			if !ok {
				return
			}
			applied, err := s.store.RecordOrderETA(ctx, job.OrderID, job.ClaimedAt, eta.At, eta.Source)
			if err != nil {
				slog.Warn("food-service: eta write failed", "order_id", job.OrderID, "error", err)
				return
			}
			if !applied {
				return
			}
			mu.Lock()
			out[job.OrderID] = eta
			mu.Unlock()
		}(job)
	}
	wg.Wait()
	return out
}
