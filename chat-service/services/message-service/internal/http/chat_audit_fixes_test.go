package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/service"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Chat backend audit 2026-10-30: the shared error mapper (C1, C2, L7), the
// edit / schedule caps and body limits (H2, H4) and the server timeouts (L1),
// pinned at the routes clients call.

const driverText = "pgx: connection reset by peer on 10.0.3.7:5432"

// auditStub answers every route below with err (nil: success).
type auditStub struct {
	ChatService // nil: any other method panics
	err         error
	calls       int
}

func (s *auditStub) GetMessages(context.Context, uuid.UUID, uuid.UUID, *scylla.MessageCursor, int) ([]service.MessageResponse, *scylla.MessageCursor, error) {
	s.calls++
	return nil, nil, s.err
}
func (s *auditStub) SendMessage(context.Context, uuid.UUID, uuid.UUID, string, string, *uuid.UUID, *uuid.UUID, *service.ReplyRef, string) (*service.MessageResponse, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &service.MessageResponse{}, nil
}
func (s *auditStub) EditMessage(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, time.Time, string) (*scylla.Message, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &scylla.Message{}, nil
}
func (s *auditStub) PinMessageSvc(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*store.ConversationPin, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &store.ConversationPin{}, nil
}
func (s *auditStub) CreateScheduledMessage(context.Context, uuid.UUID, uuid.UUID, string, string, *uuid.UUID, time.Time) (*store.ScheduledMessage, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &store.ScheduledMessage{}, nil
}
func (s *auditStub) MarkRead(context.Context, uuid.UUID, uuid.UUID, string) error {
	s.calls++
	return s.err
}

// auditRouter is the real route table behind a stand-in for the auth
// middleware.
func auditRouter(svc ChatService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(userIDKey, uuid.NewString()); c.Next() })
	New(svc, nil).RegisterRoutes(r)
	return r
}

type errBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func doAudit(t *testing.T, r *gin.Engine, method, path, body string) (*httptest.ResponseRecorder, errBody) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "k-"+uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var eb errBody
	_ = json.Unmarshal(w.Body.Bytes(), &eb)
	return w, eb
}

func TestServiceErrorsMapToStatusAndCodeAtTheRoutes(t *testing.T) {
	conv, msg := uuid.NewString(), uuid.NewString()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	routes := []struct {
		name, method, path, body string
	}{
		{"get messages", http.MethodGet, "/v1/chat/conversations/" + conv + "/messages", ""},
		{"send", http.MethodPost, "/v1/chat/conversations/" + conv + "/messages", `{"type":"text","text":"hi"}`},
		{"edit", http.MethodPatch, "/v1/chat/conversations/" + conv + "/messages/" + msg, `{"text":"new","ts":"` + ts + `"}`},
		{"pin", http.MethodPost, "/v1/chat/conversations/" + conv + "/pins", `{"message_id":"` + msg + `"}`},
		{"schedule", http.MethodPost, "/v1/chat/conversations/" + conv + "/scheduled", `{"type":"text","content":"later","send_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`},
		{"mark read", http.MethodPost, "/v1/chat/conversations/" + conv + "/read", `{"message_id":"` + msg + `"}`},
	}
	errs := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrNotMember, http.StatusNotFound, "NOT_FOUND"},
		{fmt.Errorf("wrapped: %w", service.ErrNotMember), http.StatusNotFound, "NOT_FOUND"},
		{service.ErrMessageNotFound, http.StatusNotFound, "NOT_FOUND"},
		{fmt.Errorf("%w: links are not allowed", service.ErrRequestFirstMessageInvalid), http.StatusUnprocessableEntity, "INVALID_REQUEST_MESSAGE"},
		{service.ErrAwaitingRequestAcceptance, http.StatusConflict, "AWAITING_REQUEST_ACCEPTANCE"},
		{service.ErrMessagingNotAllowed, http.StatusForbidden, "MESSAGING_NOT_ALLOWED"},
		{service.ErrMatchClosed, http.StatusConflict, "MATCH_CLOSED"},
		{service.ErrMatchOnHold, http.StatusConflict, "MATCH_ON_HOLD"}, // dating audit H2: reversible review hold
		{service.ErrNotPermittedRole, http.StatusForbidden, "FORBIDDEN"},
		{service.ErrIdempotencyInProgress, http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS"},
		{fmt.Errorf("%w: text exceeds 2000 characters", service.ErrInvalidMessageContent), http.StatusBadRequest, "BAD_REQUEST"},
		{fmt.Errorf("%w: folder name is required", service.ErrInvalidInput), http.StatusBadRequest, "INVALID_REQUEST"},
		{store.ErrPinLimitReached, http.StatusBadRequest, "INVALID_REQUEST"},
		{fmt.Errorf("%w: invitation is declined", service.ErrConflict), http.StatusConflict, "CONFLICT"},
		{errors.New(driverText), http.StatusServiceUnavailable, "UNAVAILABLE"},
	}
	for _, rt := range routes {
		for _, e := range errs {
			stub := &auditStub{err: e.err}
			w, eb := doAudit(t, auditRouter(stub), rt.method, rt.path, rt.body)
			if stub.calls != 1 {
				t.Fatalf("%s: the service was not reached (%d %s)", rt.name, w.Code, w.Body.String())
			}
			if w.Code != e.status || eb.Error.Code != e.code {
				t.Errorf("%s / %v: %d %s, want %d %s", rt.name, e.err, w.Code, eb.Error.Code, e.status, e.code)
			}
			if strings.Contains(w.Body.String(), "pgx") || strings.Contains(w.Body.String(), "10.0.3.7") {
				t.Errorf("%s: driver text reached the client: %s", rt.name, w.Body.String())
			}
		}
	}
	// The not-member body is the parity shape: no echo of the service text.
	_, eb := doAudit(t, auditRouter(&auditStub{err: service.ErrNotMember}), http.MethodGet, routes[0].path, "")
	if eb.Error.Message != "not found" {
		t.Fatalf("not-member message = %q, want the parity 'not found'", eb.Error.Message)
	}
}

func TestMessageTextCapsAndBodyLimitAtTheRoutes(t *testing.T) {
	conv, msg := uuid.NewString(), uuid.NewString()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	sendAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	overCap := strings.Repeat("é", 2001)
	huge := strings.Repeat("a", int(MaxMessageBodyBytes)+1)
	cases := []struct {
		name, method, path, body string
		status                   int
		code                     string
	}{
		{"edit over the 2000 cap", http.MethodPatch, "/v1/chat/conversations/" + conv + "/messages/" + msg,
			`{"text":"` + overCap + `","ts":"` + ts + `"}`, http.StatusBadRequest, "BAD_REQUEST"},
		{"schedule over the 2000 cap", http.MethodPost, "/v1/chat/conversations/" + conv + "/scheduled",
			`{"type":"text","content":"` + overCap + `","send_at":"` + sendAt + `"}`, http.StatusBadRequest, "BAD_REQUEST"},
		{"edit body over the limit", http.MethodPatch, "/v1/chat/conversations/" + conv + "/messages/" + msg,
			`{"text":"hi","ts":"` + ts + `","pad":"` + huge + `"}`, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE"},
		{"schedule body over the limit", http.MethodPost, "/v1/chat/conversations/" + conv + "/scheduled",
			`{"type":"text","content":"hi","send_at":"` + sendAt + `","pad":"` + huge + `"}`, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE"},
		{"send body over the limit", http.MethodPost, "/v1/chat/conversations/" + conv + "/messages",
			`{"type":"text","text":"hi","pad":"` + huge + `"}`, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE"},
	}
	for _, tc := range cases {
		stub := &auditStub{}
		w, eb := doAudit(t, auditRouter(stub), tc.method, tc.path, tc.body)
		if w.Code != tc.status || eb.Error.Code != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, w.Code, eb.Error.Code, tc.status, tc.code)
		}
		if stub.calls != 0 {
			t.Errorf("%s: a refused request reached the service", tc.name)
		}
	}
	// Control: exactly at the cap goes through.
	stub := &auditStub{}
	w, _ := doAudit(t, auditRouter(stub), http.MethodPatch, "/v1/chat/conversations/"+conv+"/messages/"+msg,
		`{"text":"`+strings.Repeat("é", 2000)+`","ts":"`+ts+`"}`)
	if w.Code != http.StatusOK || stub.calls != 1 {
		t.Fatalf("an edit at the cap: %d calls=%d %s", w.Code, stub.calls, w.Body.String())
	}
}

func TestServerHasTimeouts(t *testing.T) {
	srv := NewServer(":0", http.NewServeMux())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("server timeouts unset: header=%v read=%v write=%v idle=%v",
			srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
	}
	if srv.ReadHeaderTimeout > 10*time.Second {
		t.Fatalf("ReadHeaderTimeout %v is too long to stop a slow-header client", srv.ReadHeaderTimeout)
	}
}
