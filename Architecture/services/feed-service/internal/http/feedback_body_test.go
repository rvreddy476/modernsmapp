package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// POST /v1/feed/feedback body validation — post_id XOR author_id, a known
// signal, never the viewer's own account. Every rejection is a 400
// INVALID_REQUEST and is answered before the service is touched (the
// handler below is built with a nil service on purpose).

func TestResolveFeedbackTarget(t *testing.T) {
	viewer, post, author := uuid.New(), uuid.New(), uuid.New()
	cases := []struct {
		name     string
		req      feedbackRequest
		wantCode string
		wantPost bool
		wantAuth bool
	}{
		{"post not_interested", feedbackRequest{PostID: post.String(), Signal: "not_interested"}, "", true, false},
		{"post interested", feedbackRequest{PostID: post.String(), Signal: "interested"}, "", true, false},
		{"author not_interested", feedbackRequest{AuthorID: author.String(), Signal: "not_interested"}, "", false, true},
		{"author interested", feedbackRequest{AuthorID: author.String(), Signal: "interested"}, "", false, true},
		{"neither id", feedbackRequest{Signal: "not_interested"}, "INVALID_REQUEST", false, false},
		{"both ids", feedbackRequest{PostID: post.String(), AuthorID: author.String(), Signal: "not_interested"}, "INVALID_REQUEST", false, false},
		{"own author id", feedbackRequest{AuthorID: viewer.String(), Signal: "not_interested"}, "INVALID_REQUEST", false, false},
		{"own author id, interested", feedbackRequest{AuthorID: viewer.String(), Signal: "interested"}, "INVALID_REQUEST", false, false},
		{"bad post id", feedbackRequest{PostID: "nope", Signal: "not_interested"}, "INVALID_REQUEST", false, false},
		{"bad author id", feedbackRequest{AuthorID: "nope", Signal: "not_interested"}, "INVALID_REQUEST", false, false},
		{"unknown signal", feedbackRequest{AuthorID: author.String(), Signal: "meh"}, "INVALID_REQUEST", false, false},
		{"unknown signal with post", feedbackRequest{PostID: post.String(), Signal: "see_less"}, "INVALID_REQUEST", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, code, msg := resolveFeedbackTarget(viewer, tc.req)
			if code != tc.wantCode {
				t.Fatalf("code = %q (%s), want %q", code, msg, tc.wantCode)
			}
			if code != "" && msg == "" {
				t.Fatal("a rejection must carry a message")
			}
			if (target.PostID != nil) != tc.wantPost || (target.AuthorID != nil) != tc.wantAuth {
				t.Fatalf("target = post:%v author:%v, want post:%v author:%v", target.PostID != nil, target.AuthorID != nil, tc.wantPost, tc.wantAuth)
			}
			if tc.wantPost && *target.PostID != post {
				t.Fatalf("post id round-trips, got %s", target.PostID)
			}
			if tc.wantAuth && *target.AuthorID != author {
				t.Fatalf("author id round-trips, got %s", target.AuthorID)
			}
		})
	}
}

func TestPostFeedback_RejectsBadBodiesWith400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viewer := uuid.New()
	other := uuid.New()
	cases := []struct {
		name string
		body string
	}{
		{"neither", `{"signal":"not_interested"}`},
		{"both", `{"post_id":"` + other.String() + `","author_id":"` + other.String() + `","signal":"not_interested"}`},
		{"own author", `{"author_id":"` + viewer.String() + `","signal":"not_interested"}`},
		{"unknown signal", `{"author_id":"` + other.String() + `","signal":"block"}`},
		{"missing signal", `{"author_id":"` + other.String() + `"}`},
		{"bad author id", `{"author_id":"x","signal":"not_interested"}`},
		{"not json", `nope`},
	}
	h := New(nil) // every case below must be rejected before h.svc is used
	r := gin.New()
	r.POST("/v1/feed/feedback", h.PostFeedback)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/feed/feedback", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User-Id", viewer.String())
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
			var resp struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v: %s", err, w.Body.String())
			}
			if resp.Error.Code != "INVALID_REQUEST" {
				t.Fatalf("code = %q, want INVALID_REQUEST: %s", resp.Error.Code, w.Body.String())
			}
		})
	}
}

func TestPostFeedback_UnauthenticatedIs401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := New(nil)
	r := gin.New()
	r.POST("/v1/feed/feedback", h.PostFeedback)
	r.GET("/v1/feed/feedback/authors", h.GetMutedAuthors)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/feed/feedback"},
		{http.MethodGet, "/v1/feed/feedback/authors"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"author_id":"`+uuid.NewString()+`","signal":"not_interested"}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without X-User-Id: status = %d, want 401", tc.method, tc.path, w.Code)
		}
	}
}
