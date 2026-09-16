package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func adminChangeRequests() map[string]func() *http.Request {
	id := uuid.NewString()
	mk := func(path, body string) func() *http.Request {
		return func() *http.Request {
			req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Scopes", "admin")
			return req
		}
	}
	return map[string]func() *http.Request{
		"report":    mk("/v1/reports/"+id, `{"status":"reviewing"}`),
		"appeal":    mk("/v1/appeals/"+id, `{"status":"under_review"}`),
		"grievance": mk("/v1/grievances/"+id, `{"status":"acknowledged"}`),
	}
}

// A human admin change with no verified actor is refused before the
// service is reached (the router has a nil service, so reaching it would
// panic): there is no "system-admin" stand-in.
func TestAdminChangesWithoutVerifiedActorAreRefused(t *testing.T) {
	r := datingGrievanceRouter()
	cases := map[string]func(*http.Request){
		"missing":   func(*http.Request) {},
		"malformed": func(req *http.Request) { req.Header.Set("X-User-Id", "system-admin") },
		"nil":       func(req *http.Request) { req.Header.Set("X-User-Id", uuid.Nil.String()) },
		"verified_differs": func(req *http.Request) {
			req.Header.Set("X-User-Id", uuid.NewString())
			req.Header.Set("X-Verified-User-Id", uuid.NewString())
		},
	}
	for target, build := range adminChangeRequests() {
		for name, mutate := range cases {
			t.Run(target+"/"+name, func(t *testing.T) {
				req := build()
				mutate(req)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), codeActorRequired) {
					t.Fatalf("status=%d body=%s, want 400 %s", w.Code, w.Body.String(), codeActorRequired)
				}
			})
		}
	}
}

func TestAdminChangesStillRequireAdminScope(t *testing.T) {
	r := datingGrievanceRouter()
	for target, build := range adminChangeRequests() {
		t.Run(target, func(t *testing.T) {
			req := build()
			req.Header.Set("X-Scopes", "moderator")
			req.Header.Set("X-User-Id", uuid.NewString())
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status=%d, want 403", w.Code)
			}
		})
	}
}

func TestGrievanceHistoryAndOverdueAreAdminOnly(t *testing.T) {
	r := datingGrievanceRouter()
	for _, path := range []string{"/v1/grievances/" + uuid.NewString() + "/history", "/v1/grievances?overdue=true"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-User-Id", uuid.NewString())
		req.Header.Set("X-Scopes", "moderator")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s status=%d, want 403", path, w.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/grievances?overdue=yes", nil)
	req.Header.Set("X-User-Id", uuid.NewString())
	req.Header.Set("X-Scopes", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("overdue=yes status=%d, want 400", w.Code)
	}
}
