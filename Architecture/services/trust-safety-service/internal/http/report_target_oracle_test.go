package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Security walk 10 Nov 2026: a report must not be an existence oracle. A
// malformed target id is 400 before anything is read or written; a
// well-formed one is answered identically whether the target is a public
// post, a private post or nothing at all, because filing never consults the
// target (service/report_entities.go).

type recordingWriter struct {
	mu   sync.Mutex
	got  []postgres.Report
	fail error
}

func (w *recordingWriter) CreateReport(_ context.Context, r *postgres.Report) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fail != nil {
		return w.fail
	}
	w.got = append(w.got, *r)
	return nil
}

type underCap struct{}

func (underCap) CountReportsByReporterSince(context.Context, uuid.UUID, time.Time) (int, error) {
	return 0, nil
}

func reportRouter(w *recordingWriter) *gin.Engine {
	// No store, no post/user/media client: a filing that tried to look the
	// target up would panic on the nil store.
	h := New(service.New(nil, nil).WithReporterVolume(underCap{}).WithReportWriter(w))
	r := gin.New()
	r.POST("/v1/reports", h.FileReport)
	return r
}

func fileReport(r *gin.Engine, reporter uuid.UUID, entityType, entityID string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"entity_type": entityType, "entity_id": entityID, "reason": "spam"})
	req := httptest.NewRequest(http.MethodPost, "/v1/reports", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", reporter.String())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// shape is the response with the per-filing values (the new report's id,
// the target id the reporter sent, timestamps) blanked: what is left must
// not depend on the target.
func shape(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	if data, ok := env["data"].(map[string]any); ok {
		for _, k := range []string{"id", "entity_id", "created_at", "updated_at"} {
			if _, present := data[k]; present {
				data[k] = "<per-filing>"
			}
		}
	}
	if meta, ok := env["meta"].(map[string]any); ok {
		for k := range meta {
			meta[k] = "<per-request>"
		}
	}
	out, _ := json.Marshal(env)
	return string(out)
}

func TestReportAnswersTheSameForAnyWellFormedTarget(t *testing.T) {
	w := &recordingWriter{}
	r := reportRouter(w)
	reporter := uuid.New()
	// Stand-ins: the service cannot tell these apart, which is the point.
	publicPost, privatePost, noSuchPost := uuid.New(), uuid.New(), uuid.New()

	var shapes []string
	for _, id := range []uuid.UUID{publicPost, privatePost, noSuchPost} {
		rec := fileReport(r, reporter, "post", id.String())
		if rec.Code != http.StatusOK {
			t.Fatalf("post %s: status %d, want 200 (%s)", id, rec.Code, rec.Body)
		}
		shapes = append(shapes, shape(t, rec))
	}
	for i := 1; i < len(shapes); i++ {
		if shapes[i] != shapes[0] {
			t.Fatalf("the answer depends on the target:\n%s\n%s", shapes[0], shapes[i])
		}
	}
	// Recorded, every one, open: nothing is dropped (a dropped report would
	// show up later as a missing row in the reporter's own list or as a
	// missing 409 on a repeat).
	if len(w.got) != 3 {
		t.Fatalf("reports written = %d, want 3", len(w.got))
	}
	for _, rep := range w.got {
		if rep.Status != "open" {
			t.Fatalf("report on %s stored as %q, want open", rep.EntityID, rep.Status)
		}
	}

	// The same holds for the other UUID-addressed types.
	for _, typ := range []string{"user", "comment", "sound"} {
		a := fileReport(r, reporter, typ, uuid.NewString())
		b := fileReport(r, reporter, typ, uuid.NewString())
		if a.Code != http.StatusOK || b.Code != http.StatusOK || shape(t, a) != shape(t, b) {
			t.Fatalf("%s: answers differ: %d %s / %d %s", typ, a.Code, a.Body, b.Code, b.Body)
		}
	}
}

func TestReportRefusesJunkTargetIDsBeforeWriting(t *testing.T) {
	w := &recordingWriter{}
	r := reportRouter(w)
	id := uuid.New()
	junk := []string{
		"not-a-uuid",
		"urn:uuid:" + id.String(),
		"{" + id.String() + "}",
		strings.ReplaceAll(id.String(), "-", ""),
		uuid.Nil.String(),
		id.String() + "0",
		"",
		"11111111-1111-1111-1111-11111111111g",
	}
	var first string
	for _, typ := range []string{"post", "user", "comment", "sound"} {
		for _, bad := range junk {
			rec := fileReport(r, uuid.New(), typ, bad)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s %q: status %d, want 400 (%s)", typ, bad, rec.Code, rec.Body)
			}
			if bad == "" {
				continue // binding:"required" answers before the service
			}
			if !strings.Contains(rec.Body.String(), "Invalid entity ID") {
				t.Fatalf("%s %q: body %s, want Invalid entity ID", typ, bad, rec.Body)
			}
			if first == "" {
				first = shape(t, rec)
			} else if got := shape(t, rec); got != first {
				t.Fatalf("%s %q: malformed ids answer differently:\n%s\n%s", typ, bad, first, got)
			}
		}
	}
	for _, bad := range []string{
		"urn:uuid:" + id.String() + ":" + uuid.NewString(),
		"{" + id.String() + "}:" + uuid.NewString(),
		strings.ReplaceAll(id.String(), "-", "") + ":" + uuid.NewString(),
	} {
		if rec := fileReport(r, uuid.New(), "message", bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("message %q: status %d, want 400", bad, rec.Code)
		}
	}
	if len(w.got) != 0 {
		t.Fatalf("junk targets were written: %d", len(w.got))
	}
	// Canonical ids, either case and padded, still file.
	for _, ok := range []string{strings.ToUpper(id.String()), "  " + id.String() + " "} {
		if rec := fileReport(r, uuid.New(), "post", ok); rec.Code != http.StatusOK {
			t.Fatalf("canonical %q: status %d, want 200 (%s)", ok, rec.Code, rec.Body)
		}
	}
}
