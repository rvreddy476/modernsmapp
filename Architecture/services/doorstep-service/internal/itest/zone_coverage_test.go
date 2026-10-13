package itest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

func TestAdminZonesDrivePartnerAndCustomerCoverage(t *testing.T) {
	db := pool(t)
	ctx := context.Background()
	s := store.New(db)
	actor := store.Actor{UserID: uuid.New(), Permission: "doorstep:config.write"}
	enabled, disabled := true, false
	// Separate from seed-city coverage, so this test cannot affect existing bookings.
	boundary := json.RawMessage(`{"type":"Polygon","coordinates":[[[74.6,24.5],[74.8,24.5],[74.8,24.7],[74.6,24.7],[74.6,24.5]]]}`)
	makeZone := func(name string, active bool) *model.AdminZone {
		t.Helper()
		z, e := s.CreateZone(ctx, actor, model.AdminZoneInput{CityCode: "HYD", Name: name, Slug: "zone-" + uuid.NewString(), Boundary: boundary, Active: &active})
		if e != nil {
			t.Fatal(e)
		}
		return z
	}
	first := makeZone("A coverage test", false)
	legacy, outside := uuid.New(), uuid.New()
	for _, row := range []struct {
		id  uuid.UUID
		lat float64
	}{{legacy, 24.6}, {outside, 25.6}} {
		if _, e := db.Exec(ctx, `INSERT INTO doorstep.professionals(id,user_id,display_name,status,home_point,service_radius_m) VALUES($1,$2,'Legacy coverage test','draft',ST_SetSRID(ST_MakePoint(74.7,$3),4326)::geography,5000)`, row.id, uuid.New(), row.lat); e != nil {
			t.Fatal(e)
		}
	}
	contains := func(id uuid.UUID) bool {
		t.Helper()
		zones, e := s.CityZones(ctx, "HYD")
		if e != nil {
			t.Fatal(e)
		}
		for _, z := range zones {
			if z.ID == id {
				return true
			}
		}
		return false
	}
	if contains(first.ID) {
		t.Fatal("disabled zone offered to partner")
	}
	if hit, e := s.LocateZone(ctx, 24.6, 74.7); e != nil || hit != nil {
		t.Fatal("disabled zone opened customer coverage", e)
	}
	if _, e := s.UpdateZone(ctx, actor, first.ID, model.AdminZonePatch{Active: &enabled}); e != nil {
		t.Fatal(e)
	}
	if !contains(first.ID) {
		t.Fatal("enabled admin zone missing from partner list")
	}
	var city *string
	if e := db.QueryRow(ctx, `SELECT city_code FROM doorstep.professionals WHERE id=$1`, legacy).Scan(&city); e != nil || city == nil || *city != "HYD" {
		t.Fatal("enabled zone did not resolve legacy city", e)
	}
	if e := db.QueryRow(ctx, `SELECT city_code FROM doorstep.professionals WHERE id=$1`, outside).Scan(&city); e != nil || city != nil {
		t.Fatal("zone assigned an outside partner", e)
	}
	// New saved areas resolve city before zone reference validation as well.
	newPro := uuid.New()
	if _, e := db.Exec(ctx, `INSERT INTO doorstep.professionals(id,user_id,display_name,status) VALUES($1,$2,'New coverage test','draft')`, newPro, uuid.New()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetArea(ctx, newPro, []uuid.UUID{first.ID}, 24.6, 74.7, 5000, "Test area"); e != nil {
		t.Fatal("new area could not resolve coverage city", e)
	}
	hit, e := s.LocateZone(ctx, 24.6, 74.7)
	if e != nil || hit == nil || hit.Zone.ID != first.ID {
		t.Fatal("customer cannot resolve enabled zone", e)
	}
	second := makeZone("B overlapping coverage test", true)
	// Previously saved areas gain enabled coverage without rewriting approved settings.
	autoPro := uuid.New()
	if _, e = db.Exec(ctx, `INSERT INTO doorstep.professionals(id,user_id,display_name,status,city_code) VALUES($1,$2,'Automatic coverage test','draft','HYD')`, autoPro, uuid.New()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetArea(ctx, autoPro, nil, 24.6, 74.7, 1000, "Saved location"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeProStatus(ctx, actor, autoPro, store.StatusChange{Action: "professional.approve", From: []string{"draft"}, To: "approved", Today: time.Now()}); e != nil {
		t.Fatal(e)
	}
	autoArea, e := s.ProArea(ctx, autoPro)
	if e != nil || len(autoArea.ZoneIDs) != 2 {
		t.Fatal("saved radius did not match enabled zones", e)
	}
	var explicit int
	if e = db.QueryRow(ctx, `SELECT count(*) FROM doorstep.pro_zones WHERE pro_id=$1`, autoPro).Scan(&explicit); e != nil || explicit != 0 {
		t.Fatal("coverage read modified saved zone selections", e)
	}
	autoCandidates, e := s.SlotCandidates(ctx, store.CandidateQuery{City: "HYD", ZoneID: first.ID, Lat: 24.65, Lng: 74.7, Skill: "ac_service", ProIDs: []uuid.UUID{autoPro}, From: time.Now(), To: time.Now().Add(time.Hour)})
	if e != nil || len(autoCandidates) != 1 || !autoCandidates[0].InZone || autoCandidates[0].WithinRadius {
		t.Fatal("automatic zone bypassed partner travel radius", e)
	}
	pro := uuid.New()
	if _, e = db.Exec(ctx, `INSERT INTO doorstep.professionals(id,user_id,display_name,status,city_code) VALUES($1,$2,'Coverage test','draft','HYD')`, pro, uuid.New()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetArea(ctx, pro, []uuid.UUID{second.ID}, 24.6, 74.7, 5000, "Test area"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeProStatus(ctx, actor, pro, store.StatusChange{Action: "professional.approve", From: []string{"draft"}, To: "approved", Today: time.Now()}); e != nil {
		t.Fatal(e)
	}
	candidates, e := s.SlotCandidates(ctx, store.CandidateQuery{City: "HYD", ZoneID: first.ID, Lat: 24.6, Lng: 74.7, Skill: "ac_service", ProIDs: []uuid.UUID{pro}, From: time.Now(), To: time.Now().Add(24 * time.Hour)})
	if e != nil || len(candidates) != 1 || !candidates[0].InZone || !candidates[0].WithinRadius {
		t.Fatal("overlapping zone hid eligible partner", e)
	}
	if _, e = s.UpdateZone(ctx, actor, second.ID, model.AdminZonePatch{Active: &disabled}); e != nil {
		t.Fatal(e)
	}
	facts, e := s.ProFacts(ctx, pro, time.Now())
	if e != nil || facts.HasServiceArea {
		t.Fatal("disabled selected zone still counted as available", e)
	}
	candidates, e = s.SlotCandidates(ctx, store.CandidateQuery{City: "HYD", ZoneID: first.ID, Lat: 24.6, Lng: 74.7, Skill: "ac_service", ProIDs: []uuid.UUID{pro}, From: time.Now(), To: time.Now().Add(24 * time.Hour)})
	if e != nil || len(candidates) != 1 || candidates[0].InZone {
		t.Fatal("disabled zone accepted new jobs", e)
	}
	// Switching to a different enabled zone still requires review for approved partners.
	if _, e = s.SetArea(ctx, pro, []uuid.UUID{first.ID}, 24.6, 74.7, 5000, "Test area"); e != nil {
		t.Fatal(e)
	}
	saved, e := s.ProArea(ctx, pro)
	if e != nil || len(saved.ZoneIDs) != 0 {
		t.Fatal("unreviewed zone change went live", e)
	}
	requests, e := s.PartnerChanges(ctx, &pro)
	if e != nil || len(requests) != 1 {
		t.Fatal("area review request missing", e)
	}
	if e = s.DecidePartnerChange(ctx, actor, requests[0].ID, true, nil); e != nil {
		t.Fatal(e)
	}
	saved, e = s.ProArea(ctx, pro)
	if e != nil || len(saved.ZoneIDs) != 1 || saved.ZoneIDs[0] != first.ID {
		t.Fatal("approved zone selection not persisted", e)
	}
	facts, e = s.ProFacts(ctx, pro, time.Now())
	if e != nil || !facts.HasServiceArea {
		t.Fatal("approved active zone not reflected in readiness", e)
	}
	if _, e = s.UpdateZone(ctx, actor, first.ID, model.AdminZonePatch{Active: &disabled}); e != nil {
		t.Fatal(e)
	}
	if hit, e := s.LocateZone(ctx, 24.6, 74.7); e != nil || hit != nil {
		t.Fatal("disabled zones still serviceable", e)
	}
	if f, e := s.ProFacts(ctx, autoPro, time.Now()); e != nil || f.HasServiceArea {
		t.Fatal("disabled zones remained in automatic coverage", e)
	}
}
