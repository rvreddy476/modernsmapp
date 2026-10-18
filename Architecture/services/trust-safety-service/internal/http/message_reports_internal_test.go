package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Chat-parity CONTRACT §2.6: message-service files a message report here on
// a user's behalf; the public route accepts entity_type "message" too.

type fakeMessageReports struct {
	got []service.FileReportInput
	err error
}

func (f *fakeMessageReports) FileReportWith(_ context.Context, in service.FileReportInput) (*postgres.Report, error) {
	f.got = append(f.got, in)
	if f.err != nil {
		return nil, f.err
	}
	return &postgres.Report{ID: uuid.New(), ReporterID: in.ReporterID, EntityType: in.EntityType}, nil
}

func messageReportRouter(f messageReportFiler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := New(nil)
	h.messageReports = f
	r := gin.New()
	h.RegisterRoutes(r)
	return r
}

func postMessageReport(r *gin.Engine, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, MessageReportPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", "k")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestMessageReportRouteFilesOnBehalfOfUser(t *testing.T) {
	f := &fakeMessageReports{}
	r := messageReportRouter(f)
	reporter, conv, msg, media := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	body := `{"reporter_id":"` + reporter.String() + `","entity_id":"` + conv.String() + ":" + msg.String() +
		`","reason":"scam","note":"asked for money","evidence":{"text":"send 500","media_id":"` + media.String() +
		`","message_type":"text"},"idempotency_key":"req-1"}`
	w := postMessageReport(r, body, nil)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"created":true`) {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if len(f.got) != 1 {
		t.Fatalf("%d filings", len(f.got))
	}
	in := f.got[0]
	if in.ReporterID != reporter || in.EntityType != service.ReportEntityMessage || in.EntityRef != conv.String()+":"+msg.String() ||
		in.Reason != "scam" || in.Details != "asked for money" || in.SourceEventID != "message-service:req-1" {
		t.Fatalf("input %+v", in)
	}
	if in.Evidence == nil || in.Evidence.Source != service.EvidenceSourceMessageService || in.Evidence.ConversationID != conv.String() ||
		in.Evidence.Text != "send 500" || in.Evidence.MediaID != media.String() {
		t.Fatalf("evidence %+v", in.Evidence)
	}
}

func TestMessageReportRouteRetryIsSuccess(t *testing.T) {
	for _, err := range []error{postgres.ErrActiveReportExists, postgres.ErrDuplicateSourceEvent} {
		r := messageReportRouter(&fakeMessageReports{err: err})
		body := `{"reporter_id":"` + uuid.NewString() + `","entity_id":"` + uuid.NewString() + ":" + uuid.NewString() + `","reason":"spam"}`
		w := postMessageReport(r, body, nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"created":false`) {
			t.Fatalf("%v: status %d body %s", err, w.Code, w.Body.String())
		}
	}
}

func TestMessageReportRouteRefusesUserCaller(t *testing.T) {
	f := &fakeMessageReports{}
	r := messageReportRouter(f)
	body := `{"reporter_id":"` + uuid.NewString() + `","entity_id":"` + uuid.NewString() + ":" + uuid.NewString() + `","reason":"spam"}`
	for _, header := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
		w := postMessageReport(r, body, map[string]string{header: uuid.NewString()})
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "USER_CALLER_REFUSED") {
			t.Fatalf("%s: status %d body %s", header, w.Code, w.Body.String())
		}
	}
	if len(f.got) != 0 {
		t.Fatal("a refused caller reached the filer")
	}
}

func TestMessageReportRouteRejectsMalformedBodies(t *testing.T) {
	f := &fakeMessageReports{}
	r := messageReportRouter(f)
	pair := uuid.NewString() + ":" + uuid.NewString()
	for name, body := range map[string]string{
		"bad reporter":     `{"reporter_id":"x","entity_id":"` + pair + `","reason":"spam"}`,
		"nil reporter":     `{"reporter_id":"` + uuid.Nil.String() + `","entity_id":"` + pair + `","reason":"spam"}`,
		"bare message id":  `{"reporter_id":"` + uuid.NewString() + `","entity_id":"` + uuid.NewString() + `","reason":"spam"}`,
		"long idempotency": `{"reporter_id":"` + uuid.NewString() + `","entity_id":"` + pair + `","reason":"spam","idempotency_key":"` + strings.Repeat("k", 151) + `"}`,
		"not json":         `{`,
	} {
		if w := postMessageReport(r, body, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s", name, w.Code, w.Body.String())
		}
	}
	if len(f.got) != 0 {
		t.Fatalf("malformed bodies reached the filer: %d", len(f.got))
	}
}

// The public route accepts entity_type "message" and validates it before the
// store (a service with no database would panic past validation).
func TestPublicReportAcceptsAndValidatesMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := New(service.New(nil, nil))
	r := gin.New()
	r.POST("/v1/reports", h.FileReport)
	pair := uuid.NewString() + ":" + uuid.NewString()
	for name, tc := range map[string]struct{ body, want string }{
		"bare message id":   {`{"entity_type":"message","entity_id":"` + uuid.NewString() + `","reason":"spam"}`, "Invalid entity ID"},
		"evidence too long": {`{"entity_type":"message","entity_id":"` + pair + `","reason":"spam","evidence":{"text":"` + strings.Repeat("a", 2001) + `"}}`, "evidence text longer"},
		"bad media id":      {`{"entity_type":"message","entity_id":"` + pair + `","reason":"spam","evidence":{"media_id":"x"}}`, "media_id must be a UUID"},
		"evidence on post":  {`{"entity_type":"post","entity_id":"` + uuid.NewString() + `","reason":"spam","evidence":{"text":"x"}}`, "message reports only"},
		"bad reason":        {`{"entity_type":"message","entity_id":"` + pair + `","reason":"bored"}`, "invalid reason"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/reports", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", uuid.NewString())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s: status %d body %s, want 400 %q", name, w.Code, w.Body.String(), tc.want)
		}
	}
}
