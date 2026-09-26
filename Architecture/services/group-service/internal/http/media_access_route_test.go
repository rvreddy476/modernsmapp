package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

/*
The internal media-access routes exist at the path media-service's
NewHTTPGroupAuthorizer calls (media-service/internal/delivery/authz.go), and
a malformed question is refused with allowed:false rather than ignored.
*/
func TestMediaAccessRoutesAreRegisteredWhereMediaServiceAsks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil).RegisterRoutes(r)
	want := map[string]string{
		"POST /v1/internal/groups/media-access":       ".MediaAccess",
		"POST /v1/internal/groups/media-access/batch": ".MediaAccessBatch",
	}
	found := map[string]string{}
	for _, ri := range r.Routes() {
		found[ri.Method+" "+ri.Path] = ri.Handler
	}
	for key, handler := range want {
		h, ok := found[key]
		if !ok {
			t.Errorf("%s is not registered — media-service's group authority gets a 404 and every group attachment is denied", key)
			continue
		}
		if !strings.Contains(h, handler) {
			t.Errorf("%s served by %s, want %s", key, h, handler)
		}
	}
}

func TestMediaAccessRefusesMalformedQuestions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil).RegisterRoutes(r)

	cases := []struct {
		name, path, body string
		wantStatus       int
	}{
		{"not json", "/v1/internal/groups/media-access", `nope`, http.StatusBadRequest},
		{"bad viewer id", "/v1/internal/groups/media-access", `{"viewer_id":"x","media_id":"57946436-c63f-470e-9743-7f4d2a7a49e2"}`, http.StatusForbidden},
		{"bad media id", "/v1/internal/groups/media-access", `{"viewer_id":"2d598287-eee7-40b4-a7f5-b46b9412e4e7","media_id":"x"}`, http.StatusForbidden},
		{"empty batch", "/v1/internal/groups/media-access/batch", `{"viewer_id":"2d598287-eee7-40b4-a7f5-b46b9412e4e7","media_ids":[]}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != c.wantStatus {
			t.Errorf("%s: status %d, want %d (%s)", c.name, w.Code, c.wantStatus, w.Body.String())
		}
		var out struct {
			Allowed json.RawMessage `json:"allowed"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Errorf("%s: body is not JSON: %s", c.name, w.Body.String())
			continue
		}
		if string(out.Allowed) == "true" {
			t.Errorf("%s: a malformed question was allowed", c.name)
		}
	}
}
