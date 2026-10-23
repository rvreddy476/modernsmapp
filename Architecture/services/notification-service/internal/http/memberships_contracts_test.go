package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// Paid memberships (2026-10-23) golden contracts, copied by the web and
// Android clients:
//
//	testdata/contracts/notifications_inbox_memberships.json              GET /v1/notifications rows of every membership type
//	testdata/contracts/notification_preferences_memberships_request.json PUT /v1/notifications/preferences/detailed body
//	testdata/contracts/notification_preferences_memberships.json         the response (GET has the same shape)
//
// Regenerate with: go test ./internal/http -run MembershipsContract -update-contracts

const (
	membershipsInboxContractPath = "testdata/contracts/notifications_inbox_memberships.json"
	membershipsPrefsRequestPath  = "testdata/contracts/notification_preferences_memberships_request.json"
	membershipsPrefsResponsePath = "testdata/contracts/notification_preferences_memberships.json"
	membershipsFixtureSubID      = "00000000-0000-4000-8000-0000000ab001"
)

// membershipInboxRows: one row of each type. Call UserB (hydrated) is the
// member on the creator's notice and the creator on the member's notices; two
// rows come from an account whose profile no longer resolves, to pin the
// nameless wording.
func membershipInboxRows() []scylla.Notification {
	base := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	ts := []string{
		"52ef5c00-cf0d-11f1-8000-0000000ab0c1", "2f2c1600-cf0d-11f1-8000-0000000ab0c2",
		"0b68d000-cf0d-11f1-8000-0000000ab0c3", "e7a58a00-cf0c-11f1-8000-0000000ab0c4",
		"c3e24400-cf0c-11f1-8000-0000000ab0c5", "a01efe00-cf0c-11f1-8000-0000000ab0c6",
	}
	sub := uuid.MustParse(membershipsFixtureSubID)
	row := func(i int, typ string, actor uuid.UUID, link string) scylla.Notification {
		id, err := gocql.ParseUUID(ts[i])
		if err != nil {
			panic(err)
		}
		n := service.MembershipNotification{EventID: fmt.Sprintf("00000000-0000-4000-8000-0000000ab10%d", i), RecipientID: inboxRecipient, Type: typ}
		return scylla.Notification{
			UserID: inboxRecipient, Bucket: 202610, TS: id,
			NotificationID: scylla.DeterministicNotificationID(n.Identity()),
			Type:           typ, ActorUserID: actor, EntityType: service.MembershipEntityType,
			EntityID: sub, DeepLink: link,
			CreatedAt: base.Add(-time.Duration(i) * time.Minute),
		}
	}
	member := service.MembershipDeepLink(sub)
	return []scylla.Notification{
		row(0, service.MembershipTypeNewMember, inboxActorB, service.MembershipCreatorDeepLink),
		row(1, service.MembershipTypeJoined, inboxActorB, member),
		row(2, service.MembershipTypeRenewed, inboxGoneActor, member),
		row(3, service.MembershipTypeRenewalDue, inboxActorB, member),
		row(4, service.MembershipTypePaymentFailed, inboxGoneActor, member),
		row(5, service.MembershipTypeEnded, inboxActorB, member),
	}
}

var membershipInboxWant = []struct{ typ, title, link string }{
	{service.MembershipTypeNewMember, "Call UserB joined your membership", "/monetization/tiers"},
	{service.MembershipTypeJoined, "You're a member of Call UserB", "/memberships/" + membershipsFixtureSubID},
	{service.MembershipTypeRenewed, "Your membership with a creator was renewed", "/memberships/" + membershipsFixtureSubID},
	{service.MembershipTypeRenewalDue, "Your Call UserB membership ends soon — renew to keep access", "/memberships/" + membershipsFixtureSubID},
	{service.MembershipTypePaymentFailed, "Your membership payment to a creator didn't go through", "/memberships/" + membershipsFixtureSubID},
	{service.MembershipTypeEnded, "Your Call UserB membership has ended", "/memberships/" + membershipsFixtureSubID},
}

func TestMembershipsContractInboxRows(t *testing.T) {
	rows := fetchInboxRows(t, newInboxCopyRouter(t, membershipInboxRows()))
	if len(rows) != len(membershipInboxWant) {
		t.Fatalf("got %d rows, want %d", len(rows), len(membershipInboxWant))
	}
	for i, w := range membershipInboxWant {
		r := rows[i]
		if r["type"] != w.typ || r["title"] != w.title || r["icon"] != "account" || r["deep_link"] != w.link ||
			r["entity_type"] != "membership" || r["entity_id"] != membershipsFixtureSubID {
			t.Errorf("row %d: %v", i, r)
		}
	}
	assertGolden(t, membershipsInboxContractPath, map[string]any{"data": rows})
}

// Every membership row is listed under the system tab, and under no social tab.
func TestMembershipsInboxRowsAreInTheSystemTab(t *testing.T) {
	r := newInboxCopyRouter(t, membershipInboxRows())
	for category, want := range map[string]int{"system": len(membershipInboxWant), "mentions": 0, "activity": 0} {
		req := httptest.NewRequest(http.MethodGet, "/v1/notifications?limit=20&category="+category, nil)
		req.Header.Set("X-User-Id", inboxRecipient.String())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", category, w.Code, w.Body.String())
		}
		var env struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if len(env.Data) != want {
			t.Errorf("category %s: %d rows, want %d", category, len(env.Data), want)
		}
	}
}

func TestMembershipsContractPreferences(t *testing.T) {
	reqBody := map[string]any{"push_memberships": false, "inapp_memberships": true}
	assertGolden(t, membershipsPrefsRequestPath, reqBody)

	raw, err := os.ReadFile(membershipsPrefsRequestPath)
	if err != nil {
		t.Fatal(err)
	}
	var req UpdateNotifPreferencesRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if req.PushMemberships == nil || req.InappMemberships == nil {
		t.Fatalf("request fixture does not bind push_memberships / inapp_memberships: %+v", req)
	}
	current := postgres.DefaultNotificationPreferences(inboxRecipient.String())
	if !current.PushMemberships || !current.InappMemberships {
		t.Fatal("memberships toggles must default on")
	}
	applyNotifPreferencesPatch(current, &req)
	if current.PushMemberships || !current.InappMemberships {
		t.Fatalf("patch not applied: push %v inapp %v", current.PushMemberships, current.InappMemberships)
	}
	current.UpdatedAt = time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	assertGolden(t, membershipsPrefsResponsePath, map[string]any{"data": current})
}
