package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestListActiveCities_SeedsPresent(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	cs, err := s.ListActiveCities(context.Background())
	if err != nil {
		t.Fatalf("list cities: %v", err)
	}
	if len(cs) < 3 {
		t.Fatalf("expected the 3 seeded cities; got %d", len(cs))
	}
	want := map[string]bool{"Bengaluru": false, "Mumbai": false, "Delhi": false}
	for _, c := range cs {
		if _, ok := want[c.Name]; ok {
			want[c.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("seed city %q missing", name)
		}
	}
}

func TestGetCity_NotFound(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	_, err := s.GetCity(context.Background(), uuid.New())
	if !errors.Is(err, ErrCityNotFound) {
		t.Fatalf("expected ErrCityNotFound; got %v", err)
	}
}

func TestCountZones_AtLeastOnePerSeedCity(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	n, err := s.CountZones(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n < 3 {
		t.Fatalf("expected >= 3 seeded zones; got %d", n)
	}
}
