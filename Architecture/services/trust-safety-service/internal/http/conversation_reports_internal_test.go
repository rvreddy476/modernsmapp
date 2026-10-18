package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Chat-parity fix pass 2: message-service files a whole-conversation report
// here (DM: the other person as "user"; group: the group as "conversation").

func postConversationReport(r *gin.Engine, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, ConversationReportPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", "k")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func conversationReportBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func idList(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = uuid.NewString()
	}
	return out
}

func TestConversationReportRouteFilesGroupReport(t *testing.T) {
	f := &fakeMessageReports{}
	r := messageReportRouter(f)
	reporter, conv := uuid.New(), uuid.New()
	ids := idList(20)
	w := postConversationReport(r, conversationReportBody(t, map[string]any{
		"reporter_id": reporter.String(), "entity_type": "conversation", "entity_id": conv.String(),
		"conversation_id": conv.String(), "conversation_type": "group", "reason": "violence",
		"note": "threats", "message_ids": ids, "idempotency_key": "chat-conv-report:a:b:c",
	}), nil)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"created":true`) {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if len(f.got) != 1 {
		t.Fatalf("%d filings", len(f.got))
	}
	in := f.got[0]
	// The key shares the message route's namespace, so the fallback and
	// this route dedupe against each other.
	if in.ReporterID != reporter || in.EntityType != service.ReportEntityConversation || in.EntityRef != conv.String() ||
		in.Reason != "violence" || in.Details != "threats" || in.SourceEventID != "message-service:chat-conv-report:a:b:c" {
		t.Fatalf("input %+v", in)
	}
	ev := in.Evidence
	if ev == nil || ev.Source != service.EvidenceSourceMessageService || ev.ConversationID != conv.String() ||
		ev.ConversationType != "group" || len(ev.MessageIDs) != 20 || ev.MessageIDs[0] != ids[0] {
		t.Fatalf("evidence %+v", ev)
	}
}

func TestConversationReportRouteFilesDirectReportAsUser(t *testing.T) {
	f := &fakeMessageReports{}
	r := messageReportRouter(f)
	reporter, other, conv := uuid.New(), uuid.New(), uuid.New()
	w := postConversationReport(r, conversationReportBody(t, map[string]any{
		"reporter_id": reporter.String(), "entity_type": "user", "entity_id": other.String(),
		"conversation_id": conv.String(), "conversation_type": "direct", "reason": "harassment",
		"message_ids": []string{},
	}), nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	in := f.got[0]
	if in.EntityType != "user" || in.EntityRef != other.String() || in.Evidence.ConversationID != conv.String() || in.SourceEventID != "" {
		t.Fatalf("input %+v evidence %+v", in, in.Evidence)
	}
}

func TestConversationReportRouteRetryIsSuccess(t *testing.T) {
	for _, err := range []error{postgres.ErrActiveReportExists, postgres.ErrDuplicateSourceEvent} {
		r := messageReportRouter(&fakeMessageReports{err: err})
		conv := uuid.NewString()
		w := postConversationReport(r, conversationReportBody(t, map[string]any{
			"reporter_id": uuid.NewString(), "entity_type": "conversation", "entity_id": conv,
			"conversation_id": conv, "conversation_type": "group", "reason": "spam", "idempotency_key": "k",
		}), nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"created":false`) {
			t.Fatalf("%v: status %d body %s", err, w.Code, w.Body.String())
		}
	}
}

func TestConversationReportRouteFilerRefusalIs400(t *testing.T) {
	r := messageReportRouter(&fakeMessageReports{err: fmt.Errorf("%w: invalid reason: bored", service.ErrInvalidReport)})
	conv := uuid.NewString()
	w := postConversationReport(r, conversationReportBody(t, map[string]any{
		"reporter_id": uuid.NewString(), "entity_type": "conversation", "entity_id": conv,
		"conversation_id": conv, "conversation_type": "group", "reason": "bored",
	}), nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid reason") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestConversationReportRouteRefusesUserCaller(t *testing.T) {
	f := &fakeMessageReports{}
	r := messageReportRouter(f)
	conv := uuid.NewString()
	body := conversationReportBody(t, map[string]any{
		"reporter_id": uuid.NewString(), "entity_type": "conversation", "entity_id": conv,
		"conversation_id": conv, "conversation_type": "group", "reason": "spam",
	})
	for _, header := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
		w := postConversationReport(r, body, map[string]string{header: uuid.NewString()})
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "USER_CALLER_REFUSED") {
			t.Fatalf("%s: status %d body %s", header, w.Code, w.Body.String())
		}
	}
	if len(f.got) != 0 {
		t.Fatal("a refused caller reached the filer")
	}
}

func TestConversationReportRouteRejectsMalformedBodies(t *testing.T) {
	f := &fakeMessageReports{}
	r := messageReportRouter(f)
	reporter, conv := uuid.NewString(), uuid.NewString()
	base := func(over map[string]any) string {
		m := map[string]any{"reporter_id": reporter, "entity_type": "conversation", "entity_id": conv,
			"conversation_id": conv, "conversation_type": "group", "reason": "spam"}
		for k, v := range over {
			m[k] = v
		}
		return conversationReportBody(t, m)
	}
	for name, body := range map[string]string{
		"not json":             `{`,
		"bad reporter":         base(map[string]any{"reporter_id": "x"}),
		"message entity":       base(map[string]any{"entity_type": "message"}),
		"group names another":  base(map[string]any{"entity_id": uuid.NewString()}),
		"self report":          base(map[string]any{"entity_type": "user", "entity_id": reporter}),
		"bad conversation":     base(map[string]any{"conversation_id": "x"}),
		"no conversation type": base(map[string]any{"conversation_type": ""}),
		"21 message ids":       base(map[string]any{"message_ids": idList(21)}),
		"bad message id":       base(map[string]any{"message_ids": []string{"x"}}),
		"ids not an array":     base(map[string]any{"message_ids": "x"}),
		"long idempotency":     base(map[string]any{"idempotency_key": strings.Repeat("k", 151)}),
	} {
		if w := postConversationReport(r, body, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s", name, w.Code, w.Body.String())
		}
	}
	if len(f.got) != 0 {
		t.Fatalf("malformed bodies reached the filer: %d", len(f.got))
	}
}

// A user cannot file a conversation report on the public route: the
// membership check lives in message-service, so only its internal call may.
func TestPublicReportRefusesConversationEntity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := New(service.New(nil, nil))
	r := gin.New()
	r.POST("/v1/reports", h.FileReport)
	req := httptest.NewRequest(http.MethodPost, "/v1/reports",
		strings.NewReader(`{"entity_type":"conversation","entity_id":"`+uuid.NewString()+`","reason":"spam"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}
