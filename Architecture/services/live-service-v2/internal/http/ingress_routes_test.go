package http

// Going live from streaming software (1 Oct 2026): the wire the web codes
// against — status codes, error codes and the two fixtures under
// testdata/contracts/live.
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run TestEncoderContract
//
// regenerates the fixtures; review the diff.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/service"
	"github.com/atpost/live-service-v2/internal/store/postgres"
	"github.com/atpost/live-service-v2/internal/storetest"
)

// fakeStreamKey is the key the fixtures show. It is not a real key.
const fakeStreamKey = "sk_FAKE_fixture_key_0001"

// ingressLK is stubLK with an ingress service.
type ingressLK struct {
	stubLK
	mu      sync.Mutex
	held    map[string]*livekit.Ingress
	creates []livekit.IngressRequest
	seq     int
	err     error
}

func (l *ingressLK) CreateRTMPIngress(_ context.Context, req livekit.IngressRequest) (*livekit.Ingress, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	l.seq++
	ing := &livekit.Ingress{ID: "IN_fixture" + strings.Repeat("x", l.seq-1), URL: "rtmps://example.rtmp.livekit.cloud/x", StreamKey: fakeStreamKey}
	if l.seq > 1 {
		ing.StreamKey = fakeStreamKey + "_" + strings.Repeat("r", l.seq-1)
	}
	l.held[ing.ID] = ing
	l.creates = append(l.creates, req)
	cp := *ing
	return &cp, nil
}

func (l *ingressLK) GetIngress(_ context.Context, id string) (*livekit.Ingress, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	if ing, ok := l.held[id]; ok {
		cp := *ing
		return &cp, nil
	}
	return nil, nil
}

func (l *ingressLK) DeleteIngress(_ context.Context, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	delete(l.held, id)
	return nil
}

func newEncoderRig(t *testing.T) (*userRig, *ingressLK) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := storetest.New()
	pilot := uuid.New()
	blocked := map[uuid.UUID]bool{}
	lk := &ingressLK{held: map[string]*livekit.Ingress{}}
	svc := service.New(store, lk, relGraph{blocked: blocked}, nil, service.Config{PilotUserIDs: []uuid.UUID{pilot}})
	h := New(svc).WithInternalKey(rigKey)
	r := gin.New()
	h.RegisterRoutes(r)
	return &userRig{r: r, store: store, pilot: pilot, blocked: blocked}, lk
}

// TestCreateStreamSourceOnTheWire: source is accepted on create, returned
// on the row, and an unknown value is 422 VALIDATION_ERROR.
func TestCreateStreamSourceOnTheWire(t *testing.T) {
	u, _ := newEncoderRig(t)
	rec := u.call(http.MethodPost, "/v1/livestream/streams", u.pilot, map[string]string{"title": "x"}, nil)
	if rec.Code != http.StatusCreated || data(t, rec)["source"] != "device" {
		t.Fatalf("default source: %d %s", rec.Code, rec.Body.String())
	}
	rec = u.call(http.MethodPost, "/v1/livestream/streams", u.pilot, map[string]string{"title": "x", "source": "encoder"}, nil)
	if rec.Code != http.StatusCreated || data(t, rec)["source"] != "encoder" || data(t, rec)["has_ingress"] != false {
		t.Fatalf("encoder source: %d %s", rec.Code, rec.Body.String())
	}
	n := len(u.store.Streams)
	rec = u.call(http.MethodPost, "/v1/livestream/streams", u.pilot, map[string]string{"title": "x", "source": "rtmp"}, nil)
	if rec.Code != http.StatusUnprocessableEntity || errCode(rec) != "VALIDATION_ERROR" {
		t.Fatalf("unknown source: %d %s", rec.Code, rec.Body.String())
	}
	if len(u.store.Streams) != n {
		t.Fatal("a refused source stored a stream")
	}
}

// TestIngressRouteCodes: every answer of the two routes.
func TestIngressRouteCodes(t *testing.T) {
	u, lk := newEncoderRig(t)
	stranger := uuid.New()
	create := func(body map[string]string) string {
		t.Helper()
		rec := u.call(http.MethodPost, "/v1/livestream/streams", u.pilot, body, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
		}
		return "/v1/livestream/streams/" + data(t, rec)["id"].(string)
	}
	base := create(map[string]string{"title": "obs", "source": "encoder"})

	// 200, exactly the three fields, not cacheable.
	rec := u.call(http.MethodPost, base+"/ingress", u.pilot, nil, nil)
	first := data(t, rec)
	if rec.Code != http.StatusOK || len(first) != 3 || first["server_url"] != "rtmps://example.rtmp.livekit.cloud/x" ||
		first["stream_key"] != fakeStreamKey || first["ingress_id"] != "IN_fixture" {
		t.Fatalf("ingress: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("the key answer is cacheable: %q", rec.Header().Get("Cache-Control"))
	}
	// Idempotent.
	rec = u.call(http.MethodPost, base+"/ingress", u.pilot, nil, nil)
	if rec.Code != http.StatusOK || data(t, rec)["stream_key"] != fakeStreamKey || len(lk.creates) != 1 {
		t.Fatalf("second call: %d %s (creates %d)", rec.Code, rec.Body.String(), len(lk.creates))
	}

	// The host and moderators see has_ingress; nobody sees the key or the id
	// on a stream row.
	rec = u.call(http.MethodGet, base, u.pilot, nil, nil)
	if data(t, rec)["has_ingress"] != true || data(t, rec)["source"] != "encoder" {
		t.Fatalf("host row: %s", rec.Body.String())
	}
	rec = u.call(http.MethodGet, base, stranger, nil, nil)
	if _, told := data(t, rec)["has_ingress"]; told || data(t, rec)["source"] != "encoder" {
		t.Fatalf("stranger row: %s", rec.Body.String())
	}
	for _, who := range []uuid.UUID{u.pilot, stranger, uuid.Nil} {
		body := u.call(http.MethodGet, base, who, nil, nil).Body.String()
		for _, secret := range []string{fakeStreamKey, "IN_fixture", "stream_key", "ingress_id", "encoder_identity"} {
			if strings.Contains(body, secret) {
				t.Fatalf("a stream row carries %q: %s", secret, body)
			}
		}
	}

	// Not the host; outside the pilot; signed out.
	if rec = u.call(http.MethodPost, base+"/ingress", stranger, nil, nil); rec.Code != http.StatusForbidden || errCode(rec) != "LIVE_NOT_ENABLED" {
		t.Fatalf("stranger outside the pilot: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.call(http.MethodDelete, base+"/ingress", stranger, nil, nil); rec.Code != http.StatusForbidden || errCode(rec) != "FORBIDDEN" {
		t.Fatalf("stranger delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.call(http.MethodPost, base+"/ingress", uuid.Nil, nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", rec.Code)
	}
	if rec = u.call(http.MethodPost, "/v1/livestream/streams/"+uuid.NewString()+"/ingress", u.pilot, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing stream: %d", rec.Code)
	}
	if rec = u.call(http.MethodPost, "/v1/livestream/streams/nope/ingress", u.pilot, nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", rec.Code)
	}

	// A device stream: 422.
	dev := create(map[string]string{"title": "cam"})
	if rec = u.call(http.MethodPost, dev+"/ingress", u.pilot, nil, nil); rec.Code != http.StatusUnprocessableEntity || errCode(rec) != "VALIDATION_ERROR" {
		t.Fatalf("device stream: %d %s", rec.Code, rec.Body.String())
	}

	// Start: starting, no publisher token, source encoder.
	rec = u.call(http.MethodPost, base+"/start", u.pilot, nil, nil)
	started := data(t, rec)
	if rec.Code != http.StatusOK || started["publisher_token"] != "" || started["source"] != "encoder" ||
		started["room"] == "" || started["server_url"] != "ws://lk" ||
		started["stream"].(map[string]any)["status"] != "starting" || started["stream"].(map[string]any)["has_ingress"] != true {
		t.Fatalf("encoder start: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.call(http.MethodPost, dev+"/start", u.pilot, nil, nil); data(t, rec)["publisher_token"] != "pub" || data(t, rec)["source"] != "device" {
		t.Fatalf("device start: %s", rec.Body.String())
	}
	// Still allowed while starting; the host may watch.
	if rec = u.call(http.MethodPost, base+"/ingress", u.pilot, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("ingress while starting: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.call(http.MethodGet, base+"/viewer-token", u.pilot, nil, nil); rec.Code != http.StatusOK || data(t, rec)["token"] != "view" {
		t.Fatalf("host viewer token: %d %s", rec.Code, rec.Body.String())
	}

	// Reset key: delete, then a new key.
	rec = u.call(http.MethodDelete, base+"/ingress", u.pilot, nil, nil)
	if rec.Code != http.StatusOK || data(t, rec)["status"] != "deleted" {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.call(http.MethodGet, base, u.pilot, nil, nil); data(t, rec)["has_ingress"] != false {
		t.Fatalf("after delete: %s", rec.Body.String())
	}
	if rec = u.call(http.MethodDelete, base+"/ingress", u.pilot, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete twice: %d", rec.Code)
	}
	rec = u.call(http.MethodPost, base+"/ingress", u.pilot, nil, nil)
	if rec.Code != http.StatusOK || data(t, rec)["stream_key"] == fakeStreamKey || data(t, rec)["ingress_id"] == "IN_fixture" {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}

	// LiveKit down: 502 INGRESS_UNAVAILABLE with a message that says nothing
	// of the cause.
	lk.err = errors.New("dial tcp 10.0.0.9:7880: connection refused")
	rec = u.call(http.MethodPost, base+"/ingress", u.pilot, nil, nil)
	if rec.Code != http.StatusBadGateway || errCode(rec) != "INGRESS_UNAVAILABLE" || strings.Contains(rec.Body.String(), "10.0.0.9") {
		t.Fatalf("LiveKit down: %d %s", rec.Code, rec.Body.String())
	}
	rec = u.call(http.MethodDelete, base+"/ingress", u.pilot, nil, nil)
	if rec.Code != http.StatusBadGateway || errCode(rec) != "INGRESS_UNAVAILABLE" {
		t.Fatalf("delete with LiveKit down: %d %s", rec.Code, rec.Body.String())
	}
	lk.err = nil

	// A live-banned host: the start gate's code.
	u.store.PlatformBans[u.pilot] = postgres.PlatformBan{UserID: u.pilot}
	if rec = u.call(http.MethodPost, base+"/ingress", u.pilot, nil, nil); rec.Code != http.StatusForbidden || errCode(rec) != "LIVE_BANNED" {
		t.Fatalf("live-banned host: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.call(http.MethodPost, base+"/start", u.pilot, nil, nil); rec.Code != http.StatusForbidden || errCode(rec) != "LIVE_BANNED" {
		t.Fatalf("live-banned start: %d %s", rec.Code, rec.Body.String())
	}
	delete(u.store.PlatformBans, u.pilot)

	// On air and after a failed start: the SAME url and key (a host who
	// reloads mid-stream reads it again). Ended: 409.
	rec = u.call(http.MethodPost, base+"/ingress", u.pilot, nil, nil)
	current := data(t, rec)
	id := uuid.MustParse(strings.TrimPrefix(base, "/v1/livestream/streams/"))
	for _, status := range []string{postgres.StatusLive, postgres.StatusReconnecting, postgres.StatusFailed} {
		u.store.Streams[id].Status = status
		rec = u.call(http.MethodPost, base+"/ingress", u.pilot, nil, nil)
		got := data(t, rec)
		if rec.Code != http.StatusOK || got["stream_key"] != current["stream_key"] || got["server_url"] != current["server_url"] || got["ingress_id"] != current["ingress_id"] {
			t.Fatalf("%s: %d %s", status, rec.Code, rec.Body.String())
		}
	}
	u.store.Streams[id].Status = postgres.StatusEnded
	rec = u.call(http.MethodPost, base+"/ingress", u.pilot, nil, nil)
	if rec.Code != http.StatusConflict || errCode(rec) != "STREAM_STATE_CONFLICT" {
		t.Fatalf("ended: %d %s", rec.Code, rec.Body.String())
	}

	// server_url and stream_key are separate values: the URL is not the URL
	// with the key appended (OBS's Custom service takes two fields).
	url, _ := current["server_url"].(string)
	key, _ := current["stream_key"].(string)
	if url == "" || key == "" || strings.Contains(url, key) || strings.Contains(key, "://") {
		t.Fatalf("server_url %q / stream_key (%d chars) are not separate", url, len(key))
	}
}

// --- fixtures ---

var (
	fxEncoderStream = uuid.MustParse("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	// The room name's id is not the stream's.
	fxEncoderRoom = "stream_eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
)

func matchFixture(t *testing.T, name string, body []byte) {
	t.Helper()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		t.Fatalf("not JSON: %s", body)
	}
	path := filepath.Join("testdata", "contracts", "live", name)
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
	_ = json.Unmarshal(body, &got)
	if err := json.Unmarshal(want, &exp); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	gotB, _ := json.Marshal(got)
	expB, _ := json.Marshal(exp)
	if !bytes.Equal(gotB, expB) {
		t.Fatalf("response differs from %s\n got: %s", path, pretty.String())
	}
}

// TestEncoderContract pins the ingress answer (with a fake key) and an
// encoder stream row as its host reads it.
func TestEncoderContract(t *testing.T) {
	u, _ := newEncoderRig(t)
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	u.store.Streams[fxEncoderStream] = &postgres.LiveStream{
		ID: fxEncoderStream, CreatorUserID: u.pilot, LiveKitRoom: fxEncoderRoom,
		Title: "Launch day, from OBS", Description: "Streaming software", Status: postgres.StatusScheduled,
		Visibility: "public", Source: postgres.SourceEncoder, Orientation: postgres.OrientationLandscape,
		StatusChangedAt: at, CreatedAt: at, UpdatedAt: at,
	}
	base := "/v1/livestream/streams/" + fxEncoderStream.String()

	rec := u.call(http.MethodPost, base+"/ingress", u.pilot, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ingress: %d %s", rec.Code, rec.Body.String())
	}
	matchFixture(t, "encoder_ingress.json", rec.Body.Bytes())

	// The row: updated_at moved when the ingress was recorded; pin it.
	u.store.Streams[fxEncoderStream].UpdatedAt = at
	rec = u.call(http.MethodGet, base, u.pilot, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("row: %d %s", rec.Code, rec.Body.String())
	}
	// creator_user_id is random per run in this rig; pin it for the fixture.
	body := bytes.ReplaceAll(rec.Body.Bytes(), []byte(u.pilot.String()), []byte(fxCreator.String()))
	matchFixture(t, "encoder_stream_host.json", body)
}
