package http

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// Video credits (2026-10-23) golden contracts, copied by the web and Android
// clients:
//
//	testdata/contracts/notifications_inbox_credits.json      GET /v1/notifications rows of both credit types
//	testdata/contracts/notification_preferences_credits_request.json   PUT /v1/notifications/preferences/detailed body
//	testdata/contracts/notification_preferences_credits.json the response (GET has the same shape)
//
// Regenerate with: go test ./internal/http -run CreditsContract -update-contracts

const (
	creditsInboxContractPath    = "testdata/contracts/notifications_inbox_credits.json"
	creditsPrefsRequestPath     = "testdata/contracts/notification_preferences_credits_request.json"
	creditsPrefsResponsePath    = "testdata/contracts/notification_preferences_credits.json"
	creditsFixtureCreditID      = "00000000-0000-4000-8000-0000000cd001"
	creditsFixturePostID        = "00000000-0000-4000-8000-0000000cd002"
	creditsFixtureRecipientUUID = "00000000-0000-4000-8000-0000000000a1"
)

// creditInboxRows: the request (from Call UserB, the uploader, hydrated) and
// an acceptance from an account whose profile no longer resolves.
func creditInboxRows() []scylla.Notification {
	base := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	ts := []string{"52ef5c00-cf0d-11f1-8000-0000000000c1", "2f2c1600-cf0d-11f1-8000-0000000000c2"}
	row := func(i int, typ string, actor uuid.UUID, link string) scylla.Notification {
		id, err := gocql.ParseUUID(ts[i])
		if err != nil {
			panic(err)
		}
		recipient := inboxRecipient
		identity := creditsFixtureCreditID + ":" + recipient.String() + ":" + typ
		return scylla.Notification{
			UserID: recipient, Bucket: 202610, TS: id,
			NotificationID: scylla.DeterministicNotificationID(identity),
			Type:           typ, ActorUserID: actor, EntityType: service.CreditEntityType,
			EntityID: uuid.MustParse(creditsFixturePostID), DeepLink: link,
			CreatedAt: base.Add(-time.Duration(i) * time.Minute),
		}
	}
	return []scylla.Notification{
		row(0, service.CreditTypeRequest, inboxActorB, service.CreditRequestsDeepLink),
		row(1, service.CreditTypeAccepted, inboxGoneActor, service.UploadDeepLink("long_video", creditsFixturePostID)),
	}
}

func TestCreditsContractInboxRows(t *testing.T) {
	rows := fetchInboxRows(t, newInboxCopyRouter(t, creditInboxRows()))
	want := []struct{ title, icon, link string }{
		{"Call UserB credited you in a video", "social", "/credits/requests"},
		{"Someone accepted your credit", "social", "/tube/watch/" + creditsFixturePostID},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i]["title"] != w.title || rows[i]["icon"] != w.icon || rows[i]["deep_link"] != w.link {
			t.Errorf("row %d: %v", i, rows[i])
		}
	}
	assertGolden(t, creditsInboxContractPath, map[string]any{"data": rows})
}

func TestCreditsContractPreferences(t *testing.T) {
	reqBody := map[string]any{"push_credits": false, "inapp_credits": true}
	assertGolden(t, creditsPrefsRequestPath, reqBody)

	raw, err := os.ReadFile(creditsPrefsRequestPath)
	if err != nil {
		t.Fatal(err)
	}
	var req UpdateNotifPreferencesRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if req.PushCredits == nil || req.InappCredits == nil {
		t.Fatalf("request fixture does not bind push_credits / inapp_credits: %+v", req)
	}
	current := postgres.DefaultNotificationPreferences(creditsFixtureRecipientUUID)
	if !current.PushCredits || !current.InappCredits {
		t.Fatal("credits toggles must default on")
	}
	applyNotifPreferencesPatch(current, &req)
	if current.PushCredits || !current.InappCredits {
		t.Fatalf("patch not applied: push %v inapp %v", current.PushCredits, current.InappCredits)
	}
	current.UpdatedAt = time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	assertGolden(t, creditsPrefsResponsePath, map[string]any{"data": current})
}

// assertGolden compares v, indented, with the file at path (rewriting it
// under -update-contracts).
func assertGolden(t *testing.T, path string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *updateContracts {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update-contracts)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Errorf("contract drifted from %s:\n%s", path, got)
	}
}
