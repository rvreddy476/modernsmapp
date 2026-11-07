package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// US-M1: a deactivated, deletion-scheduled or soft-deleted account
// (store.User.Unavailable) must read exactly like a missing one to everyone
// but its owner and verified service callers, by id, by handle and on every
// sub-resource read keyed by :userId.

func newUnavailableFixture() *fakeUserReader {
	f := newPrivacyFixture()
	f.users[pvOwnerID].Unavailable = true
	return f
}

// What a missing account answers; an unavailable one must answer the same bytes.
func missingUserBody(t *testing.T, r *gin.Engine) string {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/users/"+uuid.NewString(), nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing user: status %d, want 404", w.Code)
	}
	return w.Body.String()
}

func TestUnavailableUser_ReadsAsMissingExceptToOwnerAndServiceCaller(t *testing.T) {
	tk := newPVTokens(t)
	r := newPrivacyRouter(newUnavailableFixture(), tk.verifier)
	notFound := missingUserBody(t, r)

	for route, path := range userObjectRoutes {
		for _, tc := range pvCases {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				for k, v := range tc.headers(tk) {
					req.Header.Set(k, v)
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if tc.full {
					// The owner and a service-token holder still see the account.
					if w.Code != http.StatusOK {
						t.Fatalf("status %d, want 200 for %s", w.Code, tc.name)
					}
					return
				}
				if w.Code != http.StatusNotFound {
					t.Fatalf("a deactivated account was served to %s: status %d, want 404", tc.name, w.Code)
				}
				if w.Body.String() != notFound {
					t.Fatalf("the 404 differs from a missing account's, so deactivation is detectable:\n got %s\nwant %s",
						w.Body.String(), notFound)
				}
			})
		}
	}
}

// The sub-resource guard: a terminal handler stands in for links, About,
// pins, portfolio, reputation and endorsements, which need Postgres.
func servableGuardRouter(reader userReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &Handler{users: reader, pageAdmins: map[string]bool{}}
	r := gin.New()
	r.GET("/v1/users/:userId/links", h.requireServableUser, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return r
}

func serveLinks(r *gin.Engine, id string, headers map[string]string) int {
	req := httptest.NewRequest(http.MethodGet, "/v1/users/"+id+"/links", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestRequireServableUser(t *testing.T) {
	stranger := map[string]string{"X-User-Id": pvStrangerID.String(), "X-Internal-Service-Key": pvInternalKey}
	owner := map[string]string{"X-User-Id": pvOwnerID.String()}

	hidden := servableGuardRouter(newUnavailableFixture())
	if code := serveLinks(hidden, pvOwnerID.String(), stranger); code != http.StatusNotFound {
		t.Fatalf("stranger reading a deactivated account's links: status %d, want 404", code)
	}
	if code := serveLinks(hidden, pvOwnerID.String(), nil); code != http.StatusNotFound {
		t.Fatalf("anonymous read of a deactivated account's links: status %d, want 404", code)
	}
	if code := serveLinks(hidden, pvOwnerID.String(), owner); code != http.StatusNoContent {
		t.Fatalf("owner reading their own links while deactivated: status %d, want the handler", code)
	}

	visible := servableGuardRouter(newPrivacyFixture())
	if code := serveLinks(visible, pvOwnerID.String(), stranger); code != http.StatusNoContent {
		t.Fatalf("stranger reading an active account's links: status %d, want the handler", code)
	}
	if code := serveLinks(visible, uuid.NewString(), stranger); code != http.StatusNotFound {
		t.Fatalf("links of a missing account: status %d, want 404", code)
	}
	// A malformed id is the handler's own 400, not the guard's business.
	if code := serveLinks(visible, "not-a-uuid", stranger); code != http.StatusNoContent {
		t.Fatalf("malformed id: status %d, want it passed through to the handler", code)
	}
}

// Every :userId read of another account's data is behind the guard.
func TestSubResourceReadsAreGuarded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := newPrivacyRouter(newUnavailableFixture(), nil)
	for _, p := range []struct{ method, path string }{
		{http.MethodGet, "/channels"},
		{http.MethodGet, "/links"},
		{http.MethodGet, "/about"},
		{http.MethodGet, "/about/work"},
		{http.MethodGet, "/reputation"},
		{http.MethodGet, "/endorsements"},
		{http.MethodPost, "/endorse"},
		{http.MethodGet, "/compatibility"},
		{http.MethodGet, "/pins"},
		{http.MethodGet, "/portfolio"},
		{http.MethodPost, "/qr/scan"},
	} {
		req := httptest.NewRequest(p.method, "/v1/users/"+pvOwnerID.String()+p.path, nil)
		req.Header.Set("X-User-Id", pvStrangerID.String())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req) // an unguarded route reaches a nil service and panics
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: status %d, want 404 for a deactivated account", p.method, p.path, w.Code)
		}
	}
}

func TestCheckInternalKeyConfig(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if err := CheckInternalKeyConfig("", env(map[string]string{"APP_ENV": "production"})); err != ErrInternalKeyRequired {
		t.Fatalf("production without the key: got %v, want ErrInternalKeyRequired", err)
	}
	if err := CheckInternalKeyConfig("  ", env(map[string]string{"ENVIRONMENT": "prod"})); err != ErrInternalKeyRequired {
		t.Fatalf("production with a blank key: got %v, want ErrInternalKeyRequired", err)
	}
	if err := CheckInternalKeyConfig("k", env(map[string]string{"APP_ENV": "production"})); err != nil {
		t.Fatalf("production with the key: %v", err)
	}
	if err := CheckInternalKeyConfig("", env(map[string]string{"APP_ENV": "dev"})); err != nil {
		t.Fatalf("dev without the key must still start: %v", err)
	}
	if err := CheckInternalKeyConfig("", env(nil)); err != nil {
		t.Fatalf("unset environment must still start: %v", err)
	}
}
