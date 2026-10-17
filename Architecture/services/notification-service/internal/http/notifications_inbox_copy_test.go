package http

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

var updateContracts = flag.Bool("update-contracts", false, "rewrite testdata/contracts golden files")

const inboxContractPath = "testdata/contracts/notifications_inbox.json"

var (
	inboxRecipient = uuid.MustParse("00000000-0000-4000-8000-0000000000a1")
	inboxActorB    = uuid.MustParse("00000000-0000-4000-8000-0000000000b2")
	inboxGoneActor = uuid.MustParse("00000000-0000-4000-8000-0000000000c3")
)

// inboxFixtureRows are the representative rows the golden contract shows:
// an actor type with a hydrated name, a system type with no actor, a call,
// and an actor whose profile no longer resolves ("Someone").
func inboxFixtureRows() []scylla.Notification {
	base := time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)
	// Fixed time-UUIDs: gocql.UUIDFromTime randomises the clock sequence,
	// which would make the golden file churn on every run.
	tsByRow := []string{
		"52ef5c00-ca0d-11f1-8000-000000000001",
		"2f2c1600-ca0d-11f1-8000-000000000002",
		"0b68d000-ca0d-11f1-8000-000000000003",
		"e7a58a00-ca0c-11f1-8000-000000000004",
	}
	row := func(i int, typ string, actor uuid.UUID, entityType, entity, link string) scylla.Notification {
		at := base.Add(-time.Duration(i) * time.Minute)
		ts, err := gocql.ParseUUID(tsByRow[i])
		if err != nil {
			panic(err)
		}
		return scylla.Notification{
			UserID:         inboxRecipient,
			Bucket:         202610,
			TS:             ts,
			NotificationID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(typ)),
			Type:           typ,
			ActorUserID:    actor,
			EntityType:     entityType,
			EntityID:       uuid.MustParse(entity),
			DeepLink:       link,
			CreatedAt:      at,
		}
	}
	return []scylla.Notification{
		row(0, "comment_reaction", inboxActorB, "comment", "00000000-0000-4000-8000-000000000101", "/post/00000000-0000-4000-8000-000000000100?comment=00000000-0000-4000-8000-000000000101"),
		row(1, "order_delivered", uuid.Nil, "order", "00000000-0000-4000-8000-000000000201", "/shop/orders/00000000-0000-4000-8000-000000000201"),
		row(2, "incoming_video_call", inboxActorB, "call", "00000000-0000-4000-8000-000000000301", "/call/00000000-0000-4000-8000-000000000301"),
		row(3, "follow", inboxGoneActor, "user", "00000000-0000-4000-8000-0000000000c3", "/profile/00000000-0000-4000-8000-0000000000c3"),
	}
}

// newInboxCopyRouter serves the list over stub rows and a stub
// identity-profile that knows Call UserB but not the deleted account.
func newInboxCopyRouter(t *testing.T, rows []scylla.Notification) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	profiles := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			inboxActorB.String(): map[string]string{"username": "call_b", "display_name": "Call UserB"},
		})
	}))
	t.Cleanup(profiles.Close)
	svc := service.New(nil, nil)
	svc.SetInboxPageReader(&stubInbox{rows: rows})
	svc.SetProfileServiceURL(profiles.URL)
	r := gin.New()
	r.GET("/v1/notifications", New(svc, nil).GetNotifications)
	return r
}

func fetchInboxRows(t *testing.T, r *gin.Engine) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/notifications?limit=20", nil)
	req.Header.Set("X-User-Id", inboxRecipient.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env.Data
}

func TestInboxRowsCarryTitleAndIcon(t *testing.T) {
	rows := fetchInboxRows(t, newInboxCopyRouter(t, inboxFixtureRows()))
	want := []struct{ title, icon string }{
		{"Call UserB reacted to your comment", "social"},
		{"Your order was delivered", "order"},
		{"Video call from Call UserB", "call"},
		{"Someone followed you", "social"},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i]["title"] != w.title || rows[i]["icon"] != w.icon {
			t.Errorf("row %d (%v): title/icon = %v/%v, want %q/%q",
				i, rows[i]["type"], rows[i]["title"], rows[i]["icon"], w.title, w.icon)
		}
	}
}

func TestInboxRowsNeverShipEmptyTitle(t *testing.T) {
	var rows []scylla.Notification
	for i, typ := range []string{"brand_new_type", "", "commerce_order_created", "doorstep.whatever"} {
		at := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC).Add(-time.Duration(i) * time.Minute)
		rows = append(rows, scylla.Notification{Bucket: 202610, TS: gocql.UUIDFromTime(at), Type: typ})
	}
	for _, row := range fetchInboxRows(t, newInboxCopyRouter(t, rows)) {
		title, _ := row["title"].(string)
		icon, _ := row["icon"].(string)
		if title == "" || icon == "" {
			t.Errorf("type %v shipped title %q icon %q", row["type"], title, icon)
		}
	}
}

// TestInboxContractGolden pins testdata/contracts/notifications_inbox.json
// to what the handler actually returns, so the fixture clients copy cannot
// drift. Regenerate with: go test ./internal/http -run InboxContractGolden -update-contracts
func TestInboxContractGolden(t *testing.T) {
	rows := fetchInboxRows(t, newInboxCopyRouter(t, inboxFixtureRows()))
	got, err := json.MarshalIndent(map[string]any{"data": rows}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *updateContracts {
		if err := os.MkdirAll(filepath.Dir(inboxContractPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(inboxContractPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(inboxContractPath)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update-contracts)", err)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Errorf("inbox contract drifted from %s:\n%s", inboxContractPath, got)
	}
}

func TestReplayPayloadCarriesTitleAndIcon(t *testing.T) {
	raw, err := encodeReplayPayload(scylla.Notification{Type: "order_delivered"})
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Payload["title"] != "Your order was delivered" || env.Payload["icon"] != "order" {
		t.Errorf("replay payload title/icon = %v/%v", env.Payload["title"], env.Payload["icon"])
	}
}
