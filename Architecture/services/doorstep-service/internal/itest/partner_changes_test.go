package itest

import (
	"context"
	"encoding/json"
	"github.com/atpost/doorstep-service/internal/prokyc"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestApprovedPartnerChangesNeedReview(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	id := uuid.New()
	s := store.New(p)
	if _, err := p.Exec(ctx, `INSERT INTO doorstep.professionals(id,user_id,display_name,status,city_code) VALUES($1,$2,'Original name','draft',NULL)`, id, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetArea(ctx, id, []uuid.UUID{}, 17.4, 78.6, 8000, "Uppal, Telangana"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceHours(ctx, id, []prokyc.Window{{Weekday: 1, Start: "09:00", End: "18:00"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeProStatus(ctx, store.Actor{UserID: uuid.New(), Permission: "doorstep:pros.approve"}, id, store.StatusChange{Action: "professional.approve", From: []string{"draft"}, To: "approved", Today: time.Now()}); err != nil {
		t.Fatal(err)
	}
	name := "Reviewed name"
	if _, err := s.UpdateProfile(ctx, id, &name, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateProfile(ctx, id, &name, nil); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	changed := "Another name"
	if _, err := s.UpdateProfile(ctx, id, &changed, nil); err != store.ErrConflict {
		t.Fatalf("overwrote pending: %v", err)
	}
	if _, err := s.SetArea(ctx, id, []uuid.UUID{}, 17.5, 78.5, 12000, "New working area"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceHours(ctx, id, []prokyc.Window{{Weekday: 2, Start: "10:00", End: "17:00"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPAN(ctx, id, []byte("sealed-secret-test"), "123A"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceSkills(ctx, id, []store.SkillDecl{{Code: "ac_service"}}); err != nil {
		t.Fatal(err)
	}
	skills, _ := s.ProSkills(ctx, id)
	if len(skills) != 0 {
		t.Fatal("unreviewed services went live")
	}
	current, _ := s.ProfessionalByID(ctx, id)
	if current.DisplayName != "Original name" {
		t.Fatal("unreviewed profile went live")
	}
	area, _ := s.ProArea(ctx, id)
	if area.AreaLabel != "Uppal, Telangana" || *area.HomeLat != 17.4 || area.RadiusM != 8000 {
		t.Fatalf("unreviewed area went live: %+v", area)
	}
	hours, _ := s.Hours(ctx, id)
	if len(hours) != 1 || hours[0].Weekday != 1 {
		t.Fatal("unreviewed hours went live")
	}
	requests, err := s.PartnerChanges(ctx, &id)
	if err != nil || len(requests) != 5 {
		t.Fatalf("queue: %+v %v", requests, err)
	}
	actor := store.Actor{UserID: uuid.New(), Permission: "doorstep:pros.approve"}
	for _, r := range requests {
		if strings.Contains(string(r.Payload), "pan_sealed") || strings.Contains(string(r.Payload), "sealed-secret") {
			t.Fatal("sealed PAN exposed")
		}
		approve := r.Kind != "hours"
		reason := "Please correct the requested hours"
		if err := s.DecidePartnerChange(ctx, actor, r.ID, approve, &reason); err != nil {
			t.Fatal(err)
		}
		if err := s.DecidePartnerChange(ctx, actor, r.ID, approve, &reason); err == nil {
			t.Fatal("duplicate approval applied")
		}
		var count int
		var payload []byte
		if err := p.QueryRow(ctx, `SELECT count(*) FROM doorstep.outbox_events WHERE event_type='doorstep.pro.change_reviewed' AND payload->'data'->>'change_id'=$1`, r.ID.String()).Scan(&count); err != nil || count != 1 {
			t.Fatalf("review notification must be atomic and unique: count=%d error=%v", count, err)
		}
		if err := p.QueryRow(ctx, `SELECT payload FROM doorstep.outbox_events WHERE event_type='doorstep.pro.change_reviewed' AND payload->'data'->>'change_id'=$1`, r.ID.String()).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			ProUserID uuid.UUID `json:"pro_user_id"`
			Data      struct {
				Decision string `json:"decision"`
			} `json:"data"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatal(err)
		}
		want := "rejected"
		if approve {
			want = "approved"
		}
		if envelope.ProUserID != current.UserID || envelope.Data.Decision != want {
			t.Fatal("review notification recipient or decision is wrong")
		}
		if strings.Contains(string(payload), reason) || strings.Contains(string(payload), "sealed-secret") || strings.Contains(string(payload), name) {
			t.Fatal("private review evidence leaked in notification")
		}
	}
	current, _ = s.ProfessionalByID(ctx, id)
	if current.DisplayName != name {
		t.Fatal("approved name not applied")
	}
	area, _ = s.ProArea(ctx, id)
	if area.AreaLabel != "New working area" || *area.HomeLng != 78.5 {
		t.Fatal("approved area not persisted")
	}
	hours, _ = s.Hours(ctx, id)
	if hours[0].Weekday != 1 {
		t.Fatal("rejected hours changed")
	}
	facts, err := s.ProFacts(ctx, id, time.Now())
	if err != nil || facts.HasServiceArea {
		t.Fatalf("coverage invented: %+v %v", facts, err)
	}
	var audits int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM doorstep.admin_audit_log WHERE actor_user_id=$1 AND action='professional.change_review'`, actor.UserID).Scan(&audits); err != nil || audits != 5 {
		t.Fatalf("audit=%d err=%v", audits, err)
	}
	skills, _ = s.ProSkills(ctx, id)
	if len(skills) != 1 || skills[0].Status != "verified" {
		t.Fatal("approved service change was not verified")
	}
	if _, err := s.ReplaceSkills(ctx, id, []store.SkillDecl{{Code: "salon_women"}}); err != nil {
		t.Fatal(err)
	}
	changes, _ := s.PartnerChanges(ctx, &id)
	for _, change := range changes {
		if change.Kind == "skills" && change.Status == "pending" {
			if err := s.DecidePartnerChange(ctx, actor, change.ID, true, nil); err == nil {
				t.Fatal("restricted skill approved without eligibility")
			}
		}
	}
	skills, _ = s.ProSkills(ctx, id)
	if len(skills) != 1 || skills[0].SkillCode != "ac_service" {
		t.Fatal("failed eligibility decision changed active skills")
	}
	other := uuid.New()
	mine, err := s.PartnerChanges(ctx, &other)
	if err != nil || len(mine) != 0 {
		t.Fatal("cross-partner request leak")
	}
}
