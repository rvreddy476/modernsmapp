package itest

import (
	"context"
	"fmt"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestAftercareConversationAndPrivacy(t *testing.T) {
	br, b, u, c := newVisitRig(t)
	path := "/bookings/" + b.String()
	code, raw := br.user(uuid.New(), "POST", path+"/messages", `{"body":"not allowed"}`)
	want(t, "outsider chat", code, raw, 404)
	code, raw = br.user(c, "POST", path+"/messages", `{"body":"Please ring the bell"}`)
	want(t, "customer message", code, raw, 201)
	code, raw = br.user(u, "GET", "/pro/jobs/"+b.String()+"/messages", "")
	want(t, "pro messages", code, raw, 200)
	var page model.MessagePage
	data(t, raw, &page)
	if len(page.Items) != 1 || !page.Open {
		t.Fatal(page)
	}
	code, raw = br.user(c, "POST", path+"/rating", `{"stars":5}`)
	want(t, "unfinished rating", code, raw, 409)
	code, raw = br.user(c, "PUT", "/trusted-contact", `{"name":"Family contact","phone":"+919876543210"}`)
	want(t, "trusted contact", code, raw, 200)
	sealed := br.one(t, `SELECT encode(phone_sealed,'hex') FROM doorstep.trusted_contacts WHERE user_id=$1`, c).(string)
	if sealed == "" {
		t.Fatal("unsealed contact")
	}
	code, raw = br.user(c, "POST", "/tickets", fmt.Sprintf(`{"booking_id":%q,"category":"quality","subject":"Please help with the visit","body":"I need support"}`, b))
	want(t, "ticket", code, raw, 201)
	var ticket model.Ticket
	data(t, raw, &ticket)
	code, raw = br.user(uuid.New(), "GET", "/tickets/"+ticket.ID.String(), "")
	want(t, "ticket privacy", code, raw, 404)
	code, raw = br.user(c, "POST", path+"/share", "")
	want(t, "share", code, raw, 201)
	var share model.ShareToken
	data(t, raw, &share)
	if len(share.Token) != 43 {
		t.Fatal("weak share token")
	}
	code, raw = br.user(u, "POST", "/pro/jobs/"+b.String()+"/unsafe-exit", `{"note":"Cannot continue safely"}`)
	want(t, "unsafe exit", code, raw, 201)
	if br.one(t, `SELECT status FROM doorstep.bookings WHERE id=$1`, b) != "pro_unavailable" {
		t.Fatal("unsafe exit left job active")
	}
	code, raw = br.user(c, "GET", path+"/messages", "")
	want(t, "previous conversation", code, raw, 200)
	data(t, raw, &page)
	if page.Open || len(page.Items) != 0 {
		t.Fatal("released professional conversation leaked", page)
	}
	if br.one(t, `SELECT count(*) FROM doorstep.share_tokens WHERE booking_id=$1 AND revoked_at IS NULL`, b).(int64) != 0 {
		t.Fatal("share not revoked")
	}
	br.now = br.now.Add(time.Second)
}

func TestSuspensionWorkerOpensCustomerChoice(t *testing.T) {
	br, b, _, c := newVisitRig(t)
	br.exec(t, `UPDATE doorstep.categories SET family='BEAUTY_SALON',extras_policy='catalogue_addons_only' WHERE id=$1`, br.world.anyCategory)
	code, raw := br.user(c, "POST", "/bookings/"+b.String()+"/sos", `{"note":"Safety concern"}`)
	want(t, "suspend", code, raw, 201)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); br.svc.RunWorkers(ctx, 10*time.Millisecond) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if br.one(t, `SELECT status FROM doorstep.bookings WHERE id=$1`, b) == "pro_unavailable" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("suspended professional's booking did not enter customer choice")
}
