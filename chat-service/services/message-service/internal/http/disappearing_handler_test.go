package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/chat-message-service/internal/service"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PUT /conversations/:id/settings {"disappear_after_ms"} validates the timer
// before anything is written, routes it to the conversation-wide setter, and
// returns the conversation value; DELETE .../pins/:pinId is 404 for a
// non-member or another conversation's pin.

type stubSettingsService struct {
	ChatService // nil: unreached methods panic
	timerCalls  []int64
	timerErr    error
	updates     int
	unpinErr    error
	unpinCalls  [][2]uuid.UUID // conversation, pin
}

func (s *stubSettingsService) GetSettings(_ context.Context, userID, convID uuid.UUID) (*store.ConversationSettings, error) {
	zero := int64(0)
	return &store.ConversationSettings{ConversationID: convID, UserID: userID, DisappearAfterMs: &zero, Theme: "default", AlertTone: "default"}, nil
}

func (s *stubSettingsService) UpdateSettings(context.Context, *store.ConversationSettings) error {
	s.updates++
	return nil
}

func (s *stubSettingsService) SetDisappearingTimer(_ context.Context, _, convID uuid.UUID, afterMs int64) (*service.DisappearingTimer, error) {
	s.timerCalls = append(s.timerCalls, afterMs)
	if s.timerErr != nil {
		return nil, s.timerErr
	}
	return &service.DisappearingTimer{ConversationID: convID, DisappearAfterMs: afterMs}, nil
}

func (s *stubSettingsService) UnpinMessageSvc(_ context.Context, _, convID, pinID uuid.UUID) error {
	s.unpinCalls = append(s.unpinCalls, [2]uuid.UUID{convID, pinID})
	return s.unpinErr
}

func settingsRouter(svc ChatService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc, nil)
	auth := func(c *gin.Context) { c.Set(userIDKey, uuid.New().String()) }
	r.PUT("/conversations/:id/settings", auth, h.UpdateConversationSettings)
	r.DELETE("/conversations/:id/pins/:pinId", auth, h.UnpinMessage)
	return r
}

func putSettings(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/conversations/"+uuid.New().String()+"/settings", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSettingsRejectsUnsupportedTimersBeforeWriting(t *testing.T) {
	for _, body := range []string{`{"disappear_after_ms": 3600000}`, `{"disappear_after_ms": -1}`, `{"disappear_after_ms": 86400001, "is_muted": true}`} {
		svc := &stubSettingsService{}
		w := putSettings(t, settingsRouter(svc), body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", body, w.Code)
		}
		if len(svc.timerCalls) != 0 || svc.updates != 0 {
			t.Fatalf("%s: nothing may be written (timer %v, updates %d)", body, svc.timerCalls, svc.updates)
		}
	}
}

func TestSettingsSetsTheConversationTimerAndReturnsIt(t *testing.T) {
	for _, v := range []int64{0, 86400000, 604800000, 7776000000} {
		svc := &stubSettingsService{}
		w := putSettings(t, settingsRouter(svc), `{"disappear_after_ms": `+jsonInt(v)+`}`)
		if w.Code != http.StatusOK {
			t.Fatalf("%d: status %d body %s", v, w.Code, w.Body.String())
		}
		if len(svc.timerCalls) != 1 || svc.timerCalls[0] != v {
			t.Fatalf("%d: conversation timer calls %v", v, svc.timerCalls)
		}
		var env struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if string(env.Data["disappear_after_ms"]) != jsonInt(v) {
			t.Fatalf("%d: response disappear_after_ms = %s", v, env.Data["disappear_after_ms"])
		}
	}
	// Absent (or null) leaves the timer alone.
	svc := &stubSettingsService{}
	if w := putSettings(t, settingsRouter(svc), `{"is_muted": true}`); w.Code != http.StatusOK || len(svc.timerCalls) != 0 {
		t.Fatalf("absent timer must not be touched: %d %v", w.Code, svc.timerCalls)
	}
}

func TestSettingsTimerForbiddenForPlainGroupMember(t *testing.T) {
	svc := &stubSettingsService{timerErr: service.ErrNotPermittedRole}
	w := putSettings(t, settingsRouter(svc), `{"disappear_after_ms": 86400000, "is_muted": true}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", w.Code)
	}
	if svc.updates != 0 {
		t.Fatal("a refused timer change must not save the other settings")
	}
}

func TestUnpinIsScopedToTheRouteConversation(t *testing.T) {
	convID, pinID := uuid.New(), uuid.New()
	for _, err := range []error{service.ErrNotMember, service.ErrPinNotFound} {
		svc := &stubSettingsService{unpinErr: err}
		req := httptest.NewRequest(http.MethodDelete, "/conversations/"+convID.String()+"/pins/"+pinID.String(), nil)
		w := httptest.NewRecorder()
		settingsRouter(svc).ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%v: status %d, want 404", err, w.Code)
		}
		if len(svc.unpinCalls) != 1 || svc.unpinCalls[0] != [2]uuid.UUID{convID, pinID} {
			t.Fatalf("the route conversation must reach the service: %v", svc.unpinCalls)
		}
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
