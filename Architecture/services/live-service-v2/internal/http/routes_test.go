package http

// User-facing routes (1 Oct 2026): status codes and wire fields the web
// and Android lanes code against, over the memory store.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/service"
	"github.com/atpost/live-service-v2/internal/store/postgres"
	"github.com/atpost/live-service-v2/internal/storetest"
)

type relGraph struct{ blocked map[uuid.UUID]bool }

func (g relGraph) Relationship(_ context.Context, viewer, _ uuid.UUID) (service.Relationship, error) {
	return service.Relationship{Blocked: g.blocked[viewer]}, nil
}

type stubLK struct{}

func (stubLK) CreateRoom(context.Context, string) error { return nil }
func (stubLK) IssuePublisherToken(context.Context, string, string, time.Duration) (string, error) {
	return "pub", nil
}
func (stubLK) IssueViewerToken(context.Context, string, string, time.Duration) (string, error) {
	return "view", nil
}
func (stubLK) StartEgressToS3(context.Context, string, string) (string, error) { return "EG", nil }
func (stubLK) StopEgress(context.Context, string) error                        { return nil }
func (stubLK) ServerURL() string                                               { return "ws://lk" }
func (stubLK) DeleteRoom(context.Context, string) error                        { return nil }
func (stubLK) RemoveParticipant(context.Context, string, string) error         { return nil }
func (stubLK) ListParticipants(context.Context, string) ([]livekit.Participant, error) {
	return nil, livekit.ErrRoomNotFound
}

const rigKey = "rig-internal-key"

type userRig struct {
	r       *gin.Engine
	store   *storetest.MemStore
	pilot   uuid.UUID
	blocked map[uuid.UUID]bool
}

func newUserRig(t *testing.T, internalKey string) *userRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := storetest.New()
	pilot := uuid.New()
	blocked := map[uuid.UUID]bool{}
	svc := service.New(store, stubLK{}, relGraph{blocked: blocked}, nil, service.Config{PilotUserIDs: []uuid.UUID{pilot}})
	h := New(svc)
	if internalKey != "" {
		h.WithInternalKey(internalKey)
	}
	r := gin.New()
	h.RegisterRoutes(r)
	return &userRig{r: r, store: store, pilot: pilot, blocked: blocked}
}

func (u *userRig) call(method, path string, user uuid.UUID, body any, hdr map[string]string) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", rigKey)
	if user != uuid.Nil {
		req.Header.Set("X-User-Id", user.String())
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	u.r.ServeHTTP(rec, req)
	return rec
}

func data(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("not an envelope: %s", rec.Body.String())
	}
	return env.Data
}

// TestCreatePaidStreamRefused: paid is not a product yet, so create refuses
// it with 422 VALIDATION_ERROR (any case, any padding) and stores nothing.
func TestCreatePaidStreamRefused(t *testing.T) {
	u := newUserRig(t, rigKey)
	for _, vis := range []string{"paid", " PAID "} {
		rec := u.call(http.MethodPost, "/v1/livestream/streams", u.pilot, map[string]string{"title": "x", "visibility": vis}, nil)
		if rec.Code != http.StatusUnprocessableEntity || errCode(rec) != "VALIDATION_ERROR" {
			t.Fatalf("create %q: %d %s", vis, rec.Code, rec.Body.String())
		}
	}
	if n := len(u.store.Streams); n != 0 {
		t.Fatalf("a refused paid stream was stored (%d rows)", n)
	}
	for _, vis := range []string{"public", "followers"} {
		rec := u.call(http.MethodPost, "/v1/livestream/streams", u.pilot, map[string]string{"title": "x", "visibility": vis}, nil)
		if rec.Code != http.StatusCreated || data(t, rec)["visibility"] != vis {
			t.Fatalf("create %q: %d %s", vis, rec.Code, rec.Body.String())
		}
	}
}

// TestInternalViewerRoute: exactly {"data":{"allowed":bool}}, internal key
// always required.
func TestInternalViewerRoute(t *testing.T) {
	u := newUserRig(t, rigKey)
	st := u.store.AddStream(u.pilot)
	viewer := uuid.New()
	path := "/v1/livestream/internal/streams/" + st.ID.String() + "/viewer?user_id=" + viewer.String()

	rec := u.call(http.MethodGet, path, uuid.Nil, nil, map[string]string{"X-Internal-Service-Key": "wrong"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", rec.Code)
	}
	rec = u.call(http.MethodGet, path, uuid.Nil, nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"data":{"allowed":true}}` {
		t.Fatalf("allowed: %d %q", rec.Code, rec.Body.String())
	}
	u.blocked[viewer] = true
	rec = u.call(http.MethodGet, path, uuid.Nil, nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"data":{"allowed":false}}` {
		t.Fatalf("blocked: %d %q", rec.Code, rec.Body.String())
	}
	rec = u.call(http.MethodGet, "/v1/livestream/internal/streams/"+st.ID.String()+"/viewer?user_id=nope", uuid.Nil, nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad user id: %d", rec.Code)
	}

	// No internal key configured: still refused (no open mode).
	open := newUserRig(t, "")
	st2 := open.store.AddStream(open.pilot)
	// An empty key presented to a service with an empty key must not match.
	rec = open.call(http.MethodGet, "/v1/livestream/internal/streams/"+st2.ID.String()+"/viewer?user_id="+viewer.String(), uuid.Nil, nil,
		map[string]string{"X-Internal-Service-Key": ""})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no key configured, empty key presented: %d", rec.Code)
	}
	rec = open.call(http.MethodGet, "/v1/livestream/internal/streams/"+st2.ID.String()+"/viewer?user_id="+viewer.String(), uuid.Nil, nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no key configured: %d", rec.Code)
	}
}

// TestUserRouteCodesAndFields.
func TestUserRouteCodesAndFields(t *testing.T) {
	u := newUserRig(t, rigKey)
	viewer, stranger := uuid.New(), uuid.New()

	// Pilot gate.
	rec := u.call(http.MethodPost, "/v1/livestream/streams", stranger, map[string]string{"title": "x"}, nil)
	if rec.Code != http.StatusForbidden || errCode(rec) != "LIVE_NOT_ENABLED" {
		t.Fatalf("non-pilot create: %d %s", rec.Code, rec.Body.String())
	}
	rec = u.call(http.MethodPost, "/v1/livestream/streams", u.pilot, map[string]string{"title": "x"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("pilot create: %d %s", rec.Code, rec.Body.String())
	}
	sid := data(t, rec)["id"].(string)
	base := "/v1/livestream/streams/" + sid

	// Start answers starting, not live.
	rec = u.call(http.MethodPost, base+"/start", u.pilot, nil, nil)
	st := data(t, rec)["stream"].(map[string]any)
	if rec.Code != http.StatusOK || st["status"] != "starting" || st["ended_reason"] != nil || st["status_changed_at"] == nil {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	// Host track -> live (simulated through the store's transition).
	id := uuid.MustParse(sid)
	u.store.Streams[id].Status = postgres.StatusLive

	// Chat: viewer ok; banned -> CHAT_BANNED; live-banned -> LIVE_BANNED.
	rec = u.call(http.MethodPost, base+"/chat", viewer, map[string]string{"text": "hello"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body.String())
	}
	msgID := data(t, rec)["id"].(string)
	rec = u.call(http.MethodPost, base+"/bans", u.pilot, map[string]string{"user_id": viewer.String(), "reason": "spam"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ban: %d %s", rec.Code, rec.Body.String())
	}
	rec = u.call(http.MethodPost, base+"/chat", viewer, map[string]string{"text": "again"}, nil)
	if rec.Code != http.StatusForbidden || errCode(rec) != "CHAT_BANNED" {
		t.Fatalf("banned chat: %d %s", rec.Code, rec.Body.String())
	}
	rec = u.call(http.MethodGet, base+"/viewer-token", viewer, nil, nil)
	if rec.Code != http.StatusForbidden || errCode(rec) != "BANNED_FROM_STREAM" {
		t.Fatalf("banned token: %d %s", rec.Code, rec.Body.String())
	}
	lb := uuid.New()
	u.store.PlatformBans[lb] = postgres.PlatformBan{UserID: lb}
	rec = u.call(http.MethodPost, base+"/chat", lb, map[string]string{"text": "x"}, nil)
	if rec.Code != http.StatusForbidden || errCode(rec) != "LIVE_BANNED" {
		t.Fatalf("live-banned chat: %d %s", rec.Code, rec.Body.String())
	}

	// Bans list: host only here; {user_id, reason, created_at}.
	rec = u.call(http.MethodGet, base+"/bans", u.pilot, nil, nil)
	var bans struct {
		Data []map[string]any `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &bans)
	if rec.Code != http.StatusOK || len(bans.Data) != 1 || bans.Data[0]["user_id"] != viewer.String() ||
		bans.Data[0]["reason"] != "spam" || bans.Data[0]["created_at"] == nil {
		t.Fatalf("bans: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.call(http.MethodGet, base+"/bans", stranger, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("stranger listed bans: %d", rec.Code)
	}

	// Moderators and moderator_user_ids.
	mod := uuid.New()
	rec = u.call(http.MethodPut, base+"/moderators", u.pilot, map[string]any{"user_ids": []string{mod.String()}}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("moderators: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.call(http.MethodPut, base+"/moderators", stranger, map[string]any{"user_ids": []string{}}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("stranger set moderators: %d", rec.Code)
	}
	rec = u.call(http.MethodGet, base, mod, nil, nil)
	if ids, ok := data(t, rec)["moderator_user_ids"].([]any); !ok || len(ids) != 1 {
		t.Fatalf("moderator sees moderator_user_ids: %s", rec.Body.String())
	}
	rec = u.call(http.MethodGet, base, stranger, nil, nil)
	if _, ok := data(t, rec)["moderator_user_ids"]; ok {
		t.Fatalf("stranger sees moderator_user_ids: %s", rec.Body.String())
	}

	// Remove a message (the :messageId route next to /chat/pin etc.).
	other := uuid.New()
	rec = u.call(http.MethodPost, base+"/chat", other, map[string]string{"text": "spam spam"}, nil)
	m2 := data(t, rec)["id"].(string)
	if rec = u.call(http.MethodDelete, base+"/chat/"+m2, stranger, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("stranger removed: %d", rec.Code)
	}
	if rec = u.call(http.MethodDelete, base+"/chat/"+m2, mod, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("moderator remove: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.call(http.MethodDelete, base+"/chat/pin", u.pilot, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("the pin route still resolves: %d %s", rec.Code, rec.Body.String())
	}

	// Reports.
	rec = u.call(http.MethodPost, base+"/reports", stranger, map[string]any{"reason": "spam", "message_id": msgID}, nil)
	if rec.Code != http.StatusCreated || data(t, rec)["status"] != "open" {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String())
	}
	rec = u.call(http.MethodPost, base+"/reports", stranger, map[string]any{"reason": "spam", "message_id": msgID}, nil)
	if rec.Code != http.StatusConflict || errCode(rec) != "ALREADY_REPORTED" {
		t.Fatalf("duplicate report: %d %s", rec.Code, rec.Body.String())
	}
	rec = u.call(http.MethodPost, base+"/reports", stranger, map[string]any{"reason": "boring"}, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad reason: %d %s", rec.Code, rec.Body.String())
	}

	// Blocked viewer: the stream is not found.
	u.blocked[stranger] = true
	if rec = u.call(http.MethodGet, base, stranger, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("blocked viewer read the stream: %d", rec.Code)
	}
	if rec = u.call(http.MethodGet, base+"/chat", stranger, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("blocked viewer read the chat: %d", rec.Code)
	}

	// End: the row, with ended_reason.
	rec = u.call(http.MethodPost, base+"/end", u.pilot, nil, nil)
	d := data(t, rec)
	if rec.Code != http.StatusOK || d["status"] != "ended" || d["ended_reason"] != "host_ended" {
		t.Fatalf("end: %d %s", rec.Code, rec.Body.String())
	}
	if rec = u.call(http.MethodGet, base+"/viewer-token", mod, nil, nil); rec.Code != http.StatusConflict || errCode(rec) != "STREAM_NOT_LIVE" {
		t.Fatalf("token for an ended stream: %d %s", rec.Code, rec.Body.String())
	}
}
