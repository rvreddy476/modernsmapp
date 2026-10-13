package itest

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

func TestPartnerWorkspaceWithoutCityOrZones(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	pro := uuid.New()
	if _, err := p.Exec(ctx, `INSERT INTO doorstep.professionals (id,user_id,display_name,status,city_code) VALUES ($1,$2,'Workspace test','draft',NULL)`, pro, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO doorstep.pro_skills (pro_id,skill_code,status) VALUES ($1,'deep_cleaning','pending')`, pro); err != nil {
		t.Fatal(err)
	}
	s := store.New(p)
	_, prices, err := s.ProPricing(ctx, pro, time.Now())
	if err != nil {
		t.Fatalf("prices for unassigned city: %v", err)
	}
	if len(prices) == 0 {
		t.Fatal("declared services must remain priceable before city assignment")
	}
	for _, service := range prices {
		for _, item := range service.Items {
			if item.SuggestedPricePaise != nil {
				t.Fatal("must not invent a city-specific suggested price")
			}
		}
	}
	area, err := s.SetArea(ctx, pro, []uuid.UUID{}, 17.4, 78.6, 8000)
	if err != nil || area == nil || area.RadiusM != 8000 || len(area.ZoneIDs) != 0 {
		t.Fatalf("save uncovered location: %+v %v", area, err)
	}
	facts, err := s.ProFacts(ctx, pro, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if facts.HasServiceArea {
		t.Fatal("location without enabled zones must not make a partner bookable")
	}
	if _, err := s.SetArea(ctx, pro, []uuid.UUID{uuid.New()}, 17.4, 78.6, 8000); err != store.ErrBadReference {
		t.Fatalf("invalid zone accepted: %v", err)
	}
}
