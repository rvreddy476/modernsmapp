package postgres

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/atpost/commerce-service/database"
	"github.com/google/uuid"
)

func TestDeliveryAreaValidationAndGeometry(t *testing.T) {
	a := DeliveryArea{Name: "Test area", Latitude: 17.4, Longitude: 78.6, RadiusMeters: 5000}
	if !a.valid() || !a.Contains(17.4, 78.6) || a.Contains(18.4, 78.6) || a.Contains(math.NaN(), 78.6) {
		t.Fatal("invalid coverage geometry")
	}
	a.Enabled = true
	if a.valid() {
		t.Fatal("enabled without explicit payout")
	}
	payout := int64(7500)
	a.PayoutPaise = &payout
	if !a.valid() {
		t.Fatal("valid explicit payout rejected")
	}
	payout = 0
	if a.valid() {
		t.Fatal("zero payout accepted")
	}
	a.Latitude = 0
	a.Longitude = 179.99
	if !a.Contains(0, -179.99) {
		t.Fatal("antimeridian coverage broken")
	}
}

func deliveryCoverageTestStore(t *testing.T) *Store {
	s := deliveryTestStore(t)
	// The isolated delivery fixture excludes the unrelated merchant schema.
	// Provide its foreign-key parent, never a live seller or registration.
	_, err := s.db.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS sellers(id uuid primary key,user_id uuid,store_name text,email text,slug text)`)
	if err != nil {
		t.Fatal(err)
	}
	sql, err := database.Migrations.ReadFile("migrations/041_delivery_coverage.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(context.Background(), string(sql)); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDeliveryCoverageDBExplicitConfigAuditAndRevision(t *testing.T) {
	s := deliveryCoverageTestStore(t)
	ctx := context.Background()
	actor := uuid.New()
	area, err := s.SaveDeliveryArea(ctx, actor, DeliveryArea{Name: "QA area", Latitude: 17.4, Longitude: 78.6, RadiusMeters: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if area.Enabled || area.PayoutPaise != nil {
		t.Fatal("config silently activated or invented payout")
	}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			value := *area
			value.Name = "Renamed QA area"
			_, errs[i] = s.SaveDeliveryArea(ctx, actor, value)
		}(i)
	}
	wg.Wait()
	winners := 0
	for _, err := range errs {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrDeliveryConfigConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("revision race had %d winners", winners)
	}
	var count int
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM commerce_delivery_config_audit WHERE area_id=$1`, area.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("audit count %d: %v", count, err)
	}
	if _, err = s.db.Exec(ctx, `DELETE FROM commerce_delivery_config_audit WHERE area_id=$1`, area.ID); err == nil {
		t.Fatal("audit mutable")
	}
	links, err := s.DeliveryAreaSellers(ctx, area.ID)
	if err != nil || len(links) != 0 {
		t.Fatal("area implicitly enabled a seller")
	}
	seller := uuid.New()
	if err = s.SaveDeliveryAreaSeller(ctx, actor, DeliveryAreaSeller{AreaID: area.ID, SellerID: seller, Enabled: true}); !errors.Is(err, ErrDeliveryConfigInvalid) {
		t.Fatalf("unknown seller: %v", err)
	}
	_, err = s.db.Exec(ctx, `INSERT INTO sellers(id,user_id,store_name,email,slug) VALUES($1,$2,'Delivery QA','qa@example.invalid',$3)`, seller, uuid.New(), seller.String())
	if err != nil {
		t.Fatal(err)
	}
	input := DeliveryAreaSeller{AreaID: area.ID, SellerID: seller, Enabled: true}
	if err = s.SaveDeliveryAreaSeller(ctx, actor, input); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveDeliveryAreaSeller(ctx, actor, input); !errors.Is(err, ErrDeliveryConfigConflict) {
		t.Fatalf("stale seller config: %v", err)
	}
	links, err = s.DeliveryAreaSellers(ctx, area.ID)
	if err != nil || len(links) != 1 || !links[0].Enabled || links[0].Revision != 1 {
		t.Fatalf("links: %#v %v", links, err)
	}
}
