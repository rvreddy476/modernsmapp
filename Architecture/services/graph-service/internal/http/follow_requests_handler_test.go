package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Private accounts — the follow-request routes bound to the REAL RegisterRoutes
// wiring, with the write-source guard strict. The service is nil: every case
// here is rejected by middleware or by request parsing before the handler
// would touch it, which is exactly the boundary these tests pin. Store-backed
// behaviour lives in the integration suite.

func followRequestsRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(nil).WithCanonicalWriteSource(true, nil)
	h.RegisterRoutes(r)
	return r
}

func doFollowReq(r *gin.Engine, method, path, source, userID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if source != "" {
		req.Header.Set(GraphWriteSourceHeader, source)
	}
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

func anApprovedWriter(t *testing.T) string {
	t.Helper()
	for source := range allowedGraphWriteSources {
		return source
	}
	t.Fatal("no approved graph writers configured")
	return ""
}

const (
	validUser  = "2d598287-eee7-40b4-a7f5-b46b9412e4e7"
	otherUser  = "5f2a3c1e-9b7d-4e6a-8c0f-1d2e3f4a5b6c"
	badUserRaw = "not-a-uuid"
)

// Every mutating follow-request route is a graph WRITE and must name an
// approved writer, exactly like the connection-request routes it mirrors.
func TestFollowRequestWritesRequireApprovedWriter(t *testing.T) {
	r := followRequestsRouter()
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/graph/follow-requests"},
		{http.MethodPost, "/v1/graph/follow-requests/" + otherUser + "/accept"},
		{http.MethodPost, "/v1/graph/follow-requests/" + otherUser + "/decline"},
		{http.MethodDelete, "/v1/graph/follow-requests/" + otherUser},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp := doFollowReq(r, tc.method, tc.path, "", validUser, `{"user_id":"`+otherUser+`"}`)
			if resp.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403 for an unattributed write", resp.Code)
			}
			if !strings.Contains(resp.Body.String(), "UNRECOGNISED_GRAPH_WRITER") {
				t.Errorf("body does not explain the refusal: %s", resp.Body.String())
			}
		})
	}
}

// The incoming inbox is a READ: no writer attribution, but it is viewer-bound
// and refuses a missing/invalid X-User-Id before consulting anything.
func TestIncomingFollowRequestsIsViewerBound(t *testing.T) {
	r := followRequestsRouter()
	for _, uid := range []string{"", badUserRaw} {
		resp := doFollowReq(r, http.MethodGet, "/v1/graph/follow-requests/incoming?limit=5", "", uid, "")
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("X-User-Id=%q: status %d, want 401", uid, resp.Code)
		}
	}
}

// Attributed writes with malformed input are rejected at parse time with the
// same codes the rest of the graph API uses.
func TestFollowRequestParseErrors(t *testing.T) {
	r := followRequestsRouter()
	src := anApprovedWriter(t)

	resp := doFollowReq(r, http.MethodPost, "/v1/graph/follow-requests", src, badUserRaw, `{"user_id":"`+otherUser+`"}`)
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("bad X-User-Id: status %d, want 401", resp.Code)
	}
	resp = doFollowReq(r, http.MethodPost, "/v1/graph/follow-requests", src, validUser, `{"user_id":"nope"}`)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "INVALID_ID") {
		t.Errorf("bad target: status %d body %s, want 400 INVALID_ID", resp.Code, resp.Body.String())
	}
	resp = doFollowReq(r, http.MethodPost, "/v1/graph/follow-requests", src, validUser, `{}`)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "INVALID_REQUEST") {
		t.Errorf("missing user_id: status %d body %s, want 400 INVALID_REQUEST", resp.Code, resp.Body.String())
	}
	resp = doFollowReq(r, http.MethodPost, "/v1/graph/follow-requests/nope/accept", src, validUser, "")
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "INVALID_ID") {
		t.Errorf("bad requesterId: status %d body %s, want 400 INVALID_ID", resp.Code, resp.Body.String())
	}
	resp = doFollowReq(r, http.MethodDelete, "/v1/graph/follow-requests/nope", src, validUser, "")
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "INVALID_ID") {
		t.Errorf("bad targetId: status %d body %s, want 400 INVALID_ID", resp.Code, resp.Body.String())
	}
}

// The internal content gate accepts only the two actions it exists for and
// refuses malformed ids outright — a vanished id would read as "allowed".
func TestCanInternalValidation(t *testing.T) {
	r := followRequestsRouter()
	cases := []struct {
		name, body, wantCode string
		status           int
	}{
		{"unknown action", `{"viewer_id":"` + validUser + `","action":"message","target_ids":[]}`, "INVALID_REQUEST", http.StatusBadRequest},
		{"bad viewer", `{"viewer_id":"x","action":"view_posts","target_ids":[]}`, "INVALID_ID", http.StatusBadRequest},
		{"bad target", `{"viewer_id":"` + validUser + `","action":"comment","target_ids":["x"]}`, "INVALID_ID", http.StatusBadRequest},
		{"missing action", `{"viewer_id":"` + validUser + `"}`, "INVALID_REQUEST", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doFollowReq(r, http.MethodPost, "/v1/internal/graph/can", "", "", tc.body)
			if resp.Code != tc.status || !strings.Contains(resp.Body.String(), tc.wantCode) {
				t.Fatalf("status %d body %s, want %d %s", resp.Code, resp.Body.String(), tc.status, tc.wantCode)
			}
		})
	}
	// Too many targets is refused rather than truncated.
	ids := make([]string, 0, 101)
	for i := 0; i < 101; i++ {
		ids = append(ids, `"`+otherUser+`"`)
	}
	resp := doFollowReq(r, http.MethodPost, "/v1/internal/graph/can", "", "",
		`{"viewer_id":"`+validUser+`","action":"view_posts","target_ids":[`+strings.Join(ids, ",")+`]}`)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "BATCH_TOO_LARGE") {
		t.Fatalf("101 targets: status %d body %s, want 400 BATCH_TOO_LARGE", resp.Code, resp.Body.String())
	}
}
