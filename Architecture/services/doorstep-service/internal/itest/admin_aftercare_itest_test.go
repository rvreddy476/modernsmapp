package itest

import (
	"context"
	"fmt"
	doorstephttp "github.com/atpost/doorstep-service/internal/http"
	"github.com/atpost/doorstep-service/internal/model"
	"testing"
	"time"
)

func TestAdminAftercareAuditSafetyAndComputeOnly(t *testing.T) {
	br, b, _, c := newVisitRig(t)
	br.exec(t, `UPDATE doorstep.categories SET family='BEAUTY_SALON',extras_policy='catalogue_addons_only' WHERE id=$1`, br.world.anyCategory)
	code, raw := br.user(c, "POST", "/bookings/"+b.String()+"/sos", `{"note":"Safety concern"}`)
	want(t, "salon SOS", code, raw, 201)
	var incident model.Incident
	data(t, raw, &incident)
	if !incident.ProAutoSuspended {
		t.Fatal("salon SOS did not suspend")
	}
	if br.one(t, `SELECT status FROM doorstep.professionals WHERE id=$1`, br.pros[0].id) != "suspended" {
		t.Fatal("professional still active")
	}
	path := "/incidents/" + incident.ID.String()
	code, raw = br.adminCall("POST", path+"/acknowledge", doorstephttp.PermIncidentsRead, "")
	want(t, "scope refusal", code, raw, 403)
	code, raw = br.adminCall("POST", path+"/acknowledge", doorstephttp.PermIncidentsAct, "")
	want(t, "ack", code, raw, 200)
	code, raw = br.adminCall("POST", path+"/resolve", doorstephttp.PermIncidentsAct, `{"resolution":"Concern reviewed with both parties","lift_suspension":true}`)
	want(t, "resolve and lift", code, raw, 200)
	if br.one(t, `SELECT status FROM doorstep.professionals WHERE id=$1`, br.pros[0].id) != "approved" {
		t.Fatal("automatic suspension not lifted")
	}
	if br.one(t, `SELECT count(*) FROM doorstep.admin_audit_log WHERE entity='incident' AND entity_id=$1`, incident.ID.String()).(int64) != 2 {
		t.Fatal("incident decisions not audited")
	}
	code, raw = br.user(c, "POST", "/tickets", fmt.Sprintf(`{"booking_id":%q,"category":"quality","subject":"Review this visit","body":"Please help"}`, b))
	want(t, "ticket", code, raw, 201)
	var ticket model.Ticket
	data(t, raw, &ticket)
	code, raw = br.adminCall("POST", "/tickets/"+ticket.ID.String()+"/status", doorstephttp.PermTicketsAct, `{"status":"closed","note":"Handled with customer"}`)
	want(t, "close", code, raw, 200)
	code, raw = br.adminCall("POST", "/tickets/"+ticket.ID.String()+"/status", doorstephttp.PermTicketsAct, `{"status":"open"}`)
	want(t, "closed cannot reopen", code, raw, 409)
	br, b, u, c, facts := startVisitForExtras(t)
	visitPhotos(t, br, u, b, "after", facts.MinAfter)
	visitCall(t, br, u, b, "finish", "", 200)
	code, raw = br.user(c, "GET", "/bookings/"+b.String(), "")
	want(t, "end OTP", code, raw, 200)
	var booking model.Booking
	data(t, raw, &booking)
	visitCall(t, br, u, b, "complete", fmt.Sprintf(`{"otp":%q}`, *booking.EndOTP), 200)
	code, raw = br.user(c, "POST", "/bookings/"+b.String()+"/rating", `{"stars":5}`)
	want(t, "rating", code, raw, 201)
	var rating model.Rating
	data(t, raw, &rating)
	code, raw = br.adminCall("POST", "/ratings/"+rating.ID.String()+"/hide", doorstephttp.PermRatingsModerate, `{"reason":"Duplicated test rating reviewed"}`)
	want(t, "hide", code, raw, 200)
	if br.one(t, `SELECT rating_count FROM doorstep.professionals WHERE id=$1`, br.pros[0].id).(int32) != 0 {
		t.Fatal("hidden rating still counts")
	}
	yesterday := br.now.Add(-24 * time.Hour)
	br.exec(t, `UPDATE doorstep.earning_lines SET created_at=$2,amount_paise=CASE WHEN kind='job' THEN 100000 ELSE -20000 END WHERE booking_id=$1 AND kind IN ('job','commission')`, b, yesterday)
	if _, err := br.st.ComputeVisitSettlements(context.Background(), br.now); err != nil {
		t.Fatal(err)
	}
	if _, err := br.st.ComputeVisitSettlements(context.Background(), br.now); err != nil {
		t.Fatal(err)
	}
	if br.one(t, `SELECT count(*) FROM doorstep.settlements WHERE pro_id=$1`, br.pros[0].id).(int64) != 1 {
		t.Fatal("duplicate settlement")
	}
	if br.one(t, `SELECT net_paise FROM doorstep.settlements WHERE pro_id=$1`, br.pros[0].id).(int64) != 80000 {
		t.Fatal("incorrect settlement")
	}
	if br.one(t, `SELECT status FROM doorstep.settlements WHERE pro_id=$1`, br.pros[0].id) != "computed" {
		t.Fatal("payouts must stay off")
	}
	if br.one(t, `SELECT count(*) FROM doorstep.outbox_events WHERE event_type='doorstep.pro.settlement_computed'`).(int64) != 1 {
		t.Fatal("settlement event missing or duplicated")
	}
	for _, queue := range []struct{ path, perm string }{{"incidents", doorstephttp.PermIncidentsRead}, {"tickets", doorstephttp.PermTicketsAct}, {"ratings", doorstephttp.PermRatingsModerate}, {"settlements", doorstephttp.PermSettlementsRead}} {
		code, raw = br.adminCall("GET", "/"+queue.path, queue.perm, "")
		want(t, queue.path, code, raw, 200)
	}
}
