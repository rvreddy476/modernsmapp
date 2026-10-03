// Travel mode (mechanic M8, DATING_TRAVEL_ENABLED).
//
// A pass holder picks a city from a fixed list and browses it for 1 to
// MaxTravelDays days. While the trip is active the traveller's effective
// location is the city's public centre everywhere in discovery (the store's
// effectiveCol): their deck and picks are that city's, and in other
// people's decks they appear there with a travelling marker and the
// destination city. Their real location is never shown to anyone. A trip
// stops applying the moment the pass runs out.
package service

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// MaxTravelDays is the longest trip.
const MaxTravelDays = 7

var (
	// ErrTravelRequiresPass maps to 403 TRAVEL_REQUIRES_PASS.
	ErrTravelRequiresPass = errors.New("forbidden: travel mode comes with a pass")
	// ErrInvalidCity maps to 400 INVALID_CITY.
	ErrInvalidCity = errors.New("invalid: city must be one of the travel cities")
	// ErrInvalidTravelDays maps to 400 INVALID_TRAVEL_DAYS.
	ErrInvalidTravelDays = errors.New("invalid: days must be 1 to 7")
)

// TravelCity is a destination: a code, a label and its public centre.
type TravelCity struct {
	Code  string  `json:"code"`
	Label string  `json:"label"`
	lat   float64 `json:"-"`
	lng   float64 `json:"-"`
}

// TravelCities is the fixed destination list, alphabetical by label. The
// points are city centres, never anyone's location.
var TravelCities = []TravelCity{
	{"ahmedabad", "Ahmedabad", 23.02, 72.57}, {"bengaluru", "Bengaluru", 12.97, 77.59},
	{"chandigarh", "Chandigarh", 30.73, 76.78}, {"chennai", "Chennai", 13.08, 80.27},
	{"delhi", "Delhi", 28.61, 77.21}, {"dubai", "Dubai", 25.20, 55.27},
	{"goa", "Goa", 15.50, 73.83}, {"hyderabad", "Hyderabad", 17.39, 78.49},
	{"indore", "Indore", 22.72, 75.86}, {"jaipur", "Jaipur", 26.91, 75.79},
	{"kochi", "Kochi", 9.93, 76.27}, {"kolkata", "Kolkata", 22.57, 88.36},
	{"london", "London", 51.51, -0.13}, {"lucknow", "Lucknow", 26.85, 80.95},
	{"mumbai", "Mumbai", 19.08, 72.88}, {"mysuru", "Mysuru", 12.30, 76.64},
	{"new_york", "New York", 40.71, -74.01}, {"pune", "Pune", 18.52, 73.86},
	{"singapore", "Singapore", 1.35, 103.82}, {"sydney", "Sydney", -33.87, 151.21},
	{"toronto", "Toronto", 43.65, -79.38}, {"visakhapatnam", "Visakhapatnam", 17.69, 83.22},
}

func travelCity(code string) (TravelCity, bool) {
	for _, c := range TravelCities {
		if c.Code == code {
			return c, true
		}
	}
	return TravelCity{}, false
}

// TravelTrip is the caller's active trip.
type TravelTrip struct {
	City     TravelCity `json:"city"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   time.Time  `json:"ends_at"`
}

// TravelState is GET/PUT/DELETE /v1/dating/travel.
type TravelState struct {
	// Active is the trip in effect; omitted when there is none.
	Active *TravelTrip `json:"active,omitempty"`
	// Available: the caller holds a pass, so they may start a trip.
	Available bool         `json:"available"`
	Cities    []TravelCity `json:"cities"`
	MaxDays   int          `json:"max_days"`
}

// GetTravel returns the caller's travel state.
func (s *Service) GetTravel(ctx context.Context, userID uuid.UUID) (*TravelState, error) {
	if !s.mechanics.Travel {
		return nil, ErrMechanicDisabled
	}
	out := &TravelState{Available: s.holdsPass(ctx, userID), Cities: TravelCities, MaxDays: MaxTravelDays}
	t, err := s.store.ActiveTrip(ctx, userID)
	if err != nil {
		return nil, err
	}
	if t != nil {
		c, _ := travelCity(t.CityCode)
		if c.Code == "" {
			c = TravelCity{Code: t.CityCode, Label: t.CityLabel}
		}
		out.Active = &TravelTrip{City: c, StartsAt: t.StartsAt.UTC(), EndsAt: t.EndsAt.UTC()}
	}
	return out, nil
}

// StartTravel starts (or replaces) the caller's trip.
func (s *Service) StartTravel(ctx context.Context, userID uuid.UUID, cityCode string, days int) (*TravelState, error) {
	if !s.mechanics.Travel {
		return nil, ErrMechanicDisabled
	}
	c, ok := travelCity(cityCode)
	if !ok {
		return nil, ErrInvalidCity
	}
	if days < 1 || days > MaxTravelDays {
		return nil, ErrInvalidTravelDays
	}
	if !s.holdsPass(ctx, userID) {
		return nil, ErrTravelRequiresPass
	}
	if err := s.requireInteractiveProfile(ctx, userID); err != nil {
		return nil, err
	}
	if err := s.store.StartTrip(ctx, userID, c.Code, c.Label, c.lat, c.lng, days); err != nil {
		return nil, err
	}
	s.travelMoved(ctx, userID)
	return s.GetTravel(ctx, userID)
}

// EndTravel ends the caller's trip.
func (s *Service) EndTravel(ctx context.Context, userID uuid.UUID) (*TravelState, error) {
	if !s.mechanics.Travel {
		return nil, ErrMechanicDisabled
	}
	if err := s.store.EndTrip(ctx, userID); err != nil {
		return nil, err
	}
	s.travelMoved(ctx, userID)
	return s.GetTravel(ctx, userID)
}

// travelMoved drops every cached deck the move changes: the traveller's own
// and every deck they sit in.
func (s *Service) travelMoved(ctx context.Context, userID uuid.UUID) {
	s.InvalidatePulseCache(ctx, userID)
	s.InvalidateDecksForCandidate(ctx, userID)
}

// viewerProfile is the viewer's profile as discovery uses it: during an
// active trip its point and city are the destination's.
func (s *Service) viewerProfile(ctx context.Context, viewerID uuid.UUID) (*store.Profile, error) {
	p, err := s.store.GetProfile(ctx, viewerID)
	if err != nil || p == nil {
		return p, err
	}
	t, terr := s.store.ActiveTrip(ctx, viewerID)
	if terr != nil || t == nil {
		return p, nil
	}
	cp := *p
	lat, lng, city := t.Latitude, t.Longitude, t.CityLabel
	cp.Latitude, cp.Longitude, cp.City = &lat, &lng, &city
	return &cp, nil
}
