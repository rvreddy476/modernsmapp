package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// seedProvider is a small helper that ensures the categories seed has run
// (BootstrapSchema does that) and inserts a deterministic provider for the
// given category.
func seedProvider(t *testing.T, s *Store, setuID, category, name string, states []string) uuid.UUID {
	t.Helper()
	id, err := s.UpsertProvider(context.Background(), UpsertProviderInput{
		SetuBillerID:       setuID,
		CategoryID:         category,
		Name:               name,
		States:             states,
		BillFetchSupported: true,
		CustomerParamsJSON: []byte(`[{"id":"consumer_number","name":"Consumer Number","required":true}]`),
	})
	if err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	return id
}

func TestUpsertProvider_RoundTrip(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	id := seedProvider(t, s, "BSESRJ-test", "electricity", "BSES Rajdhani", []string{"DL"})

	got, err := s.GetProvider(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SetuBillerID != "BSESRJ-test" || got.Name != "BSES Rajdhani" {
		t.Fatalf("provider mismatch: %+v", got)
	}
	if len(got.States) != 1 || got.States[0] != "DL" {
		t.Fatalf("states mismatch: %+v", got.States)
	}
}

func TestUpsertProvider_IsIdempotent(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	a := seedProvider(t, s, "AIRTEL-test", "mobile_postpaid", "Airtel", nil)
	b := seedProvider(t, s, "AIRTEL-test", "mobile_postpaid", "Airtel Postpaid", nil)
	if a != b {
		t.Fatalf("expected same id on second upsert, got a=%s b=%s", a, b)
	}
	got, _ := s.GetProvider(context.Background(), b)
	if got.Name != "Airtel Postpaid" {
		t.Fatalf("expected updated name, got %q", got.Name)
	}
}

func TestListCategories_SeedsAreSorted(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	cats, err := s.ListCategories(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(cats) < 9 {
		t.Fatalf("expected >= 9 seeded categories; got %d", len(cats))
	}
	// Validate they're returned in sort_order ascending.
	last := -1
	for _, c := range cats {
		if c.SortOrder < last {
			t.Fatalf("categories not in sort order: %+v", cats)
		}
		last = c.SortOrder
	}
}

func TestListProviders_StateFilterAllowsNational(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	seedProvider(t, s, "DL-only", "electricity", "DL Only", []string{"DL"})
	seedProvider(t, s, "national-1", "electricity", "National", nil)

	out, err := s.ListProviders(context.Background(), "electricity", "KA", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// DL-only should be filtered out for state=KA; national should appear.
	foundNational := false
	for _, p := range out {
		if p.SetuBillerID == "DL-only" {
			t.Fatalf("DL-only should not appear for state=KA")
		}
		if p.SetuBillerID == "national-1" {
			foundNational = true
		}
	}
	if !foundNational {
		t.Fatalf("expected national provider to appear")
	}
}

func TestGetProvider_NotFound(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	_, err := s.GetProvider(context.Background(), uuid.New())
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("expected ErrProviderNotFound; got %v", err)
	}
}

func TestGetCategory_NotFound(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	_, err := s.GetCategory(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrCategoryNotFound) {
		t.Fatalf("expected ErrCategoryNotFound; got %v", err)
	}
}
