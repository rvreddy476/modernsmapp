package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/service"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// GET /v1/livestream/streams?status= over httptest (MTube, 2026-09-27).
// The store is faked; filtering and paging are the service tests'. Pinned
// here: the default is the live listing and never touches the upcoming
// query, an unknown status is a 400 before any store call, and the
// scheduled rows' wire shape is the fixture the web copies.
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run TestStreamsStatus
//
// regenerates the fixture; review the diff.

var (
	fxStreamTime = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	fxCreator    = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	fxCover      = uuid.MustParse("55555555-5555-4555-8555-555555555555")
)

type statusStore struct {
	service.Store // nil: anything else panics
	live          []*postgres.LiveStream
	scheduled     []*postgres.LiveStream
	calls         map[string]int
}

func (f *statusStore) ListLive(context.Context, postgres.ListLiveParams) ([]*postgres.LiveStream, error) {
	f.calls["live"]++
	return f.live, nil
}

func (f *statusStore) ListScheduled(context.Context, postgres.ListScheduledParams) ([]*postgres.LiveStream, error) {
	f.calls["scheduled"]++
	return f.scheduled, nil
}

func fixtureStreams() (live, scheduled []*postgres.LiveStream) {
	started := fxStreamTime.Add(-20 * time.Minute)
	soon := fxStreamTime.Add(90 * time.Minute)
	tomorrow := fxStreamTime.Add(26 * time.Hour)
	live = []*postgres.LiveStream{{
		ID: uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"), CreatorUserID: fxCreator, LiveKitRoom: "stream_aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Title: "Friday build, live", Status: "live", Visibility: "public", Source: "device", StartedAt: &started, ViewerPeak: 42,
		CreatedAt: fxStreamTime.Add(-time.Hour), UpdatedAt: started, StatusChangedAt: started,
	}}
	scheduled = []*postgres.LiveStream{
		{
			ID: uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"), CreatorUserID: fxCreator, LiveKitRoom: "stream_bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
			Title: "Kafka AMA", Description: "Bring questions", CoverMediaID: &fxCover, Status: "scheduled", Visibility: "public", Source: "device",
			ScheduledAt: &soon, CreatedAt: fxStreamTime.Add(-48 * time.Hour), UpdatedAt: fxStreamTime.Add(-48 * time.Hour), StatusChangedAt: fxStreamTime.Add(-48 * time.Hour),
		},
		{
			ID: uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc"), CreatorUserID: fxCreator, LiveKitRoom: "stream_cccccccc-cccc-4ccc-8ccc-cccccccccccc",
			Title: "Weekend build", Status: "scheduled", Visibility: "public", Source: "device",
			ScheduledAt: &tomorrow, CreatedAt: fxStreamTime.Add(-24 * time.Hour), UpdatedAt: fxStreamTime.Add(-24 * time.Hour), StatusChangedAt: fxStreamTime.Add(-24 * time.Hour),
		},
	}
	return live, scheduled
}

func serveStreams(t *testing.T, query string) (*httptest.ResponseRecorder, *statusStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	live, scheduled := fixtureStreams()
	store := &statusStore{live: live, scheduled: scheduled, calls: map[string]int{}}
	r := gin.New()
	New(service.New(store, nil, nil, nil, service.Config{})).RegisterRoutes(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/livestream/streams"+query, nil))
	return w, store
}

func streamTitles(t *testing.T, body []byte) []string {
	t.Helper()
	var env struct {
		Data []postgres.LiveStream `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("not the envelope: %v\n%s", err, body)
	}
	out := make([]string, len(env.Data))
	for i, st := range env.Data {
		out[i] = st.Title
	}
	return out
}

func TestStreamsStatusDefaultIsTheLiveListing(t *testing.T) {
	plain, store := serveStreams(t, "")
	if plain.Code != http.StatusOK || store.calls["scheduled"] != 0 || store.calls["live"] != 1 {
		t.Fatalf("status=%d calls=%v body=%s", plain.Code, store.calls, plain.Body.String())
	}
	explicit, _ := serveStreams(t, "?status=live")
	if !bytes.Equal(plain.Body.Bytes(), explicit.Body.Bytes()) {
		t.Fatalf("?status=live differs from the default:\n%s\n%s", plain.Body.String(), explicit.Body.String())
	}
	if got := streamTitles(t, plain.Body.Bytes()); len(got) != 1 || got[0] != "Friday build, live" {
		t.Fatalf("default rows: %v", got)
	}
}

func TestStreamsStatusScheduledMatchesTheFixture(t *testing.T) {
	w, store := serveStreams(t, "?status=scheduled")
	if w.Code != http.StatusOK || store.calls["live"] != 0 || store.calls["scheduled"] != 1 {
		t.Fatalf("status=%d calls=%v body=%s", w.Code, store.calls, w.Body.String())
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, w.Body.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "contracts", "mtube", "livestreams_scheduled.json")
	if os.Getenv("UPDATE_CONTRACTS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture: %v (UPDATE_CONTRACTS=1 to create)", err)
	}
	var got, exp any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if err := json.Unmarshal(want, &exp); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	gotB, _ := json.Marshal(got)
	expB, _ := json.Marshal(exp)
	if !bytes.Equal(gotB, expB) {
		t.Fatalf("response differs from %s\n got: %s", path, pretty.String())
	}
}

func TestStreamsStatusAllIsLiveThenScheduled(t *testing.T) {
	w, _ := serveStreams(t, "?status=all")
	got := streamTitles(t, w.Body.Bytes())
	want := []string{"Friday build, live", "Kafka AMA", "Weekend build"}
	if w.Code != http.StatusOK || len(got) != len(want) {
		t.Fatalf("status=%d rows=%v", w.Code, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows=%v want %v", got, want)
		}
	}
}

func TestStreamsStatusUnknownIsRefusedBeforeTheStore(t *testing.T) {
	for _, q := range []string{"?status=ended", "?status=LIVE", "?status=upcoming"} {
		w, store := serveStreams(t, q)
		if w.Code != http.StatusBadRequest || len(store.calls) != 0 {
			t.Fatalf("%s: status=%d calls=%v body=%s", q, w.Code, store.calls, w.Body.String())
		}
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if env.Error.Code != "INVALID_REQUEST" {
			t.Fatalf("%s: code=%q body=%s", q, env.Error.Code, w.Body.String())
		}
	}
}
