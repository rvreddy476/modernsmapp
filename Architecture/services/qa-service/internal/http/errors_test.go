package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/atpost/qa-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func serveErr(err error, code int, name string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	respondServiceError(c, err, code, name)
	return w
}

// L1: a raw database error never reaches the client; refusals keep their
// status and their own text.
func TestRespondServiceErrorHidesInternalDetail(t *testing.T) {
	raw := errors.New(`ERROR: new row for relation "answer_requests" violates check constraint "answer_requests_status_check" (SQLSTATE 23514)`)
	w := serveErr(fmt.Errorf("create answer request: %w", raw), http.StatusInternalServerError, "CREATE_FAILED")
	if w.Code != http.StatusInternalServerError || errorCode(t, w) != "CREATE_FAILED" {
		t.Fatalf("status=%d code=%q", w.Code, errorCode(t, w))
	}
	for _, leak := range []string{"SQLSTATE", "answer_requests", "constraint", "relation"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("the response leaks %q: %s", leak, w.Body.String())
		}
	}

	for _, c := range []struct {
		err    error
		status int
		code   string
		text   string
	}{
		{errors.New("invalid: title is required"), 400, "INVALID_REQUEST", "title is required"},
		{errors.New("forbidden: nope"), 403, "FORBIDDEN", "nope"},
		{errors.New("not_found: question not found"), 404, "NOT_FOUND", "question not found"},
		{fmt.Errorf("%w: already asked", store.ErrConflict), 409, "CONFLICT", "already asked"},
		{errors.New("rate_limited: slow down"), 429, "RATE_LIMITED", "slow down"},
		{store.ErrOwnContent, 403, "FORBIDDEN", "cannot vote on your own content"},
		{fmt.Errorf("get question: %w", pgx.ErrNoRows), 404, "NOT_FOUND", "not found"},
	} {
		w := serveErr(c.err, http.StatusInternalServerError, "X_FAILED")
		if w.Code != c.status || errorCode(t, w) != c.code || !strings.Contains(w.Body.String(), c.text) {
			t.Fatalf("%v: status=%d code=%q body=%s, want %d %s", c.err, w.Code, errorCode(t, w), w.Body.String(), c.status, c.code)
		}
	}
}

// Every handler answers internal failures through respondServiceError: no
// handler builds a client message from err.Error() on a 500.
func TestNoHandlerSendsRawErrorText(t *testing.T) {
	files := []string{"handler_answers.go", "handler_comments.go", "handler_community.go", "handler_engagement.go", "handler_feeds.go",
		"handler_moderation.go", "handler_profile.go", "handler_questions.go", "handler_search.go", "handler_topics.go", "handler_votes.go"}
	for _, f := range files {
		src := readSource(t, f)
		for i, line := range strings.Split(src, "\n") {
			if strings.Contains(line, "StatusInternalServerError") && strings.Contains(line, "err.Error()") {
				t.Fatalf("%s:%d sends raw error text: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// M3: an icon is platform media or nothing.
func TestPlatformMediaIcon(t *testing.T) {
	id := uuid.New()
	for raw, want := range map[string]string{
		"":                                       "",
		id.String():                              "/v1/media/" + id.String() + "/serve",
		"/v1/media/" + id.String() + "/serve":    "/v1/media/" + id.String() + "/serve",
		"/v1/media/" + id.String() + "/serve/sm": "/v1/media/" + id.String() + "/serve/sm",
	} {
		got, ok := platformMediaIcon(raw)
		if !ok || got != want {
			t.Fatalf("icon %q = %q,%v, want %q", raw, got, ok, want)
		}
	}
	for _, bad := range []string{
		"https://evil.example/pixel.png",
		"//evil.example/v1/media/" + id.String() + "/serve",
		"https://evil.example/v1/media/" + id.String() + "/serve",
		"/v1/media/" + uuid.Nil.String() + "/serve",
		uuid.Nil.String(),
		"/v1/media/" + id.String() + "/serve?x=https://evil",
		"javascript:alert(1)",
	} {
		if _, ok := platformMediaIcon(bad); ok {
			t.Fatalf("icon %q accepted", bad)
		}
	}
}

// M3: no public topic create; H2: the approval routes need a signed-in user.
func TestPublicTopicCreateRetiredAndApprovalNeedsUser(t *testing.T) {
	rg := newAdminTokenRig(t)
	w := adminServe(rg.r, http.MethodPost, "/v1/qa/topics", `{"name":"free-followers","is_featured":true,"icon_url":"https://evil.example/x.png"}`, gatewayUser(uuid.New()))
	if w.Code != http.StatusGone || errorCode(t, w) != CodeTopicCreateMoved {
		t.Fatalf("public topic create: status=%d code=%q, want 410 %s", w.Code, errorCode(t, w), CodeTopicCreateMoved)
	}
	for _, action := range []string{"approve", "reject"} {
		hdr := gatewayUser(uuid.New())
		delete(hdr, "X-User-ID")
		path := "/v1/qa/communities/" + uuid.NewString() + "/questions/" + uuid.NewString() + "/" + action
		if w := adminServe(rg.r, http.MethodPost, path, `{}`, hdr); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a user: status=%d, want 401", action, w.Code)
		}
	}
}

// M3: a token-path topic write refuses a foreign icon before the store.
func TestAdminTopicRefusesForeignIcon(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceQA, []string{PermTopicsManage}, rg.actor.String())
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, InternalAdminPrefix + "/topics"},
		{http.MethodPatch, InternalAdminPrefix + "/topics/" + uuid.NewString()},
	} {
		w := adminServe(rg.r, c.method, c.path, `{"reason":"r","name":"n","icon_url":"https://evil.example/p.png"}`, bearer(tok))
		if w.Code != http.StatusBadRequest || errorCode(t, w) != CodeTopicIconInvalid {
			t.Fatalf("%s %s: status=%d code=%q", c.method, c.path, w.Code, errorCode(t, w))
		}
	}
	if len(rg.st.actors) != 0 {
		t.Fatal("a refused topic reached the store")
	}
	w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/topics", `{"reason":"r","name":"n","icon_url":"`+uuid.NewString()+`","is_featured":true}`, bearer(tok))
	if w.Code != http.StatusOK || len(rg.st.topics) != 1 || !strings.HasPrefix(rg.st.topics[0].IconURL, "/v1/media/") || !rg.st.topics[0].IsFeatured {
		t.Fatalf("create: status=%d topics=%+v", w.Code, rg.st.topics)
	}
	if w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/topics", `{"reason":"r"}`, bearer(tok)); w.Code != http.StatusBadRequest {
		t.Fatalf("create without a name: status=%d, want 400", w.Code)
	}
}

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
