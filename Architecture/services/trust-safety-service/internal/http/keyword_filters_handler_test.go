package http

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Self-service keyword filters — handler-level contract tests. The service
// under test has no store wired (service.New(nil, nil)), which is exactly
// what makes these fast: every assertion here is about behaviour that must
// happen BEFORE any store access — validation, identity, and scope checks.

func newKeywordTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(service.New(nil, nil))
	h.RegisterRoutes(r)
	return r
}

func doJSON(t *testing.T, r *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Error == nil {
		t.Fatalf("no error envelope in %q", w.Body.String())
	}
	return envelope.Error.Code
}

// ─── PUT validation table ─────────────────────────────────────────────────────

func TestPutMyKeywordFilters_Validation(t *testing.T) {
	r := newKeywordTestRouter()
	me := uuid.New().String()

	longWord := strings.Repeat("x", service.MaxKeywordRunes+1)
	tooMany := make([]string, service.MaxUserKeywords+1)
	for i := range tooMany {
		tooMany[i] = "w" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	tooManyJSON, _ := json.Marshal(map[string][]string{"keywords": tooMany})

	cases := []struct {
		name     string
		body     string
		wantCode int
		wantErr  string
	}{
		{"empty keyword", `{"keywords":[""]}`, 400, "INVALID_KEYWORD"},
		{"whitespace-only keyword", `{"keywords":["   "]}`, 400, "INVALID_KEYWORD"},
		{"bare hash", `{"keywords":["#"]}`, 400, "INVALID_KEYWORD"},
		{"over-long keyword", `{"keywords":["` + longWord + `"]}`, 400, "INVALID_KEYWORD"},
		{"control character", `{"keywords":["bad\u0007word"]}`, 400, "INVALID_KEYWORD"},
		{"over the 50-keyword cap", string(tooManyJSON), 400, "TOO_MANY_KEYWORDS"},
		{"malformed json", `{"keywords": "not-a-list"}`, 400, "BAD_REQUEST"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doJSON(t, r, "PUT", "/v1/users/me/keyword-filters", tc.body,
				map[string]string{"X-User-Id": me})
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.wantCode, w.Body.String())
			}
			if got := errorCode(t, w); got != tc.wantErr {
				t.Fatalf("error code = %q, want %q", got, tc.wantErr)
			}
		})
	}
}

func TestPutMyKeywordFilters_RequiresIdentity(t *testing.T) {
	r := newKeywordTestRouter()
	w := doJSON(t, r, "PUT", "/v1/users/me/keyword-filters", `{"keywords":["spoilers"]}`, nil)
	if w.Code != 401 {
		t.Fatalf("PUT with no X-User-Id = %d, want 401", w.Code)
	}
}

// ─── Normalisation semantics (service-level, no store needed) ────────────────

func TestNormalizeKeywords_Semantics(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"trim + lowercase", []string{"  SpOiLeRs  "}, []string{"spoilers"}},
		{"leading hash stripped", []string{"#GameOfThrones"}, []string{"gameofthrones"}},
		{"dedupe after normalisation", []string{"Cats", "#cats", "cats "}, []string{"cats"}},
		{"interior whitespace collapsed", []string{"a    b"}, []string{"a b"}},
		{"unicode kept", []string{"Crème"}, []string{"crème"}},
		{"order preserved", []string{"b", "a", "c"}, []string{"b", "a", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := service.NormalizeKeywords(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// ─── Admin endpoint scope enforcement ────────────────────────────────────────

// A non-admin writing a filter under someone ELSE's scope_id must be refused.
// scope_id used to be caller-supplied and unvalidated.
func TestAddKeywordFilter_CrossUserScopeIDIsForbidden(t *testing.T) {
	r := newKeywordTestRouter()
	caller := uuid.New().String()
	victim := uuid.New().String()

	w := doJSON(t, r, "POST", "/v1/keyword-filters",
		`{"scope":"user","scope_id":"`+victim+`","keyword":"planted"}`,
		map[string]string{"X-User-Id": caller})
	if w.Code != 403 {
		t.Fatalf("cross-user scope_id = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
}

func TestAddKeywordFilter_NonAdminPlatformScopeIsForbidden(t *testing.T) {
	r := newKeywordTestRouter()
	caller := uuid.New().String()

	w := doJSON(t, r, "POST", "/v1/keyword-filters",
		`{"scope":"platform","keyword":"everyone-sees-this"}`,
		map[string]string{"X-User-Id": caller})
	if w.Code != 403 {
		t.Fatalf("non-admin platform scope = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
}

// GET /v1/keyword-filters had no auth at all: anyone could enumerate any
// user's private filter list by guessing scope_id.
func TestGetKeywordFilters_RequiresIdentity(t *testing.T) {
	r := newKeywordTestRouter()
	w := doJSON(t, r, "GET", "/v1/keyword-filters", "", nil)
	if w.Code != 401 {
		t.Fatalf("GET with no X-User-Id = %d, want 401", w.Code)
	}
}

func TestGetKeywordFilters_NonAdminCannotReadOtherScopes(t *testing.T) {
	r := newKeywordTestRouter()
	caller := uuid.New().String()
	other := uuid.New().String()

	for _, path := range []string{
		"/v1/keyword-filters?scope=platform",
		"/v1/keyword-filters?scope=user&scope_id=" + other,
	} {
		w := doJSON(t, r, "GET", path, "", map[string]string{"X-User-Id": caller})
		if w.Code != 403 {
			t.Fatalf("%s = %d, want 403 (body %s)", path, w.Code, w.Body.String())
		}
	}
}

func TestGetMyKeywordFilters_RequiresIdentity(t *testing.T) {
	r := newKeywordTestRouter()
	w := doJSON(t, r, "GET", "/v1/users/me/keyword-filters", "", nil)
	if w.Code != 401 {
		t.Fatalf("GET me with no X-User-Id = %d, want 401", w.Code)
	}
}
