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

	"github.com/atpost/qa-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// M10: the web client's spellings are accepted.
func TestAnswerRequestStatusAcceptsTheWebSpelling(t *testing.T) {
	cases := []struct{ status, response, want string }{
		{"accepted", "", "accepted"},
		{"declined", "", "declined"},
		{"", "accept", "accepted"},
		{"", "decline", "declined"},
		{"", " Accept ", "accepted"},
		{"declined", "accept", "declined"}, // canonical wins
		{"", "maybe", "maybe"},             // refused by the service
		{"", "", ""},
	}
	for _, c := range cases {
		if got := answerRequestStatus(c.status, c.response); got != c.want {
			t.Errorf("answerRequestStatus(%q, %q) = %q, want %q", c.status, c.response, got, c.want)
		}
	}
	if firstNonEmpty("a", "b") != "a" || firstNonEmpty(" ", "b") != "b" || firstNonEmpty("", "") != "" {
		t.Fatal("firstNonEmpty")
	}
}

func TestQuestionDraftRequestFoldsSnakeCase(t *testing.T) {
	var r questionDraftRequest
	if err := json.Unmarshal([]byte(`{"community_id":"c","topic_ids":["t"],"is_anonymous":true}`), &r); err != nil {
		t.Fatal(err)
	}
	r.normalize()
	if r.CommunityID == nil || *r.CommunityID != "c" || len(r.TopicIDs) != 1 || !r.IsAnonymous {
		t.Fatalf("snake_case fields dropped: %+v", r)
	}
	r = questionDraftRequest{}
	_ = json.Unmarshal([]byte(`{"communityId":"x","community_id":"c","topicIds":["a"],"topic_ids":["t"]}`), &r)
	r.normalize()
	if *r.CommunityID != "x" || r.TopicIDs[0] != "a" {
		t.Fatalf("camelCase must win: %+v", r)
	}
}

func serve(h gin.HandlerFunc, method, path, body string, header map[string]string, params gin.Params) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		c.Request.Header.Set(k, v)
	}
	c.Params = params
	h(c)
	return w
}

// M10: a response the server cannot store is a 400, never a 500 with SQL.
func TestRespondToAnswerRequestRefusesUnknownResponseWith400(t *testing.T) {
	h := New(service.New(nil, nil))
	w := serve(h.RespondToAnswerRequest, http.MethodPost, "/x", `{"response":"maybe"}`,
		map[string]string{"X-User-ID": uuid.NewString()}, gin.Params{{Key: "requestId", Value: uuid.NewString()}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

// The web's target_user_id is read; a missing or bad id is a 400.
func TestCreateAnswerRequestReadsTargetUserID(t *testing.T) {
	h := New(service.New(nil, nil))
	params := gin.Params{{Key: "questionId", Value: uuid.NewString()}}
	hdr := map[string]string{"X-User-ID": uuid.NewString()}
	if w := serve(h.CreateAnswerRequest, http.MethodPost, "/x", `{"target_user_id":"nope"}`, hdr, params); w.Code != http.StatusBadRequest {
		t.Fatalf("bad target_user_id: status %d", w.Code)
	}
	if w := serve(h.CreateAnswerRequest, http.MethodPost, "/x", `{}`, hdr, params); w.Code != http.StatusBadRequest {
		t.Fatalf("no target: status %d", w.Code)
	}
}

type countingLimiter struct {
	mu   sync.Mutex
	keys []string
	deny bool
}

func (l *countingLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys = append(l.keys, key)
	return !l.deny, nil
}

// M6: a signed-out search is counted by client address and refused with
// 429 past the limit; a signed-in search is not counted here.
func TestAllowSearchLimitsSignedOutCallers(t *testing.T) {
	lim := &countingLimiter{deny: true}
	h := New(service.New(nil, nil).WithRateLimiter(lim))
	w := serve(func(c *gin.Context) {
		if h.allowSearch(c) {
			c.Status(http.StatusOK)
		}
	}, http.MethodGet, "/v1/qa/search?q=ab", "", map[string]string{"X-Real-IP": "203.0.113.9"}, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("signed-out over the limit: status %d", w.Code)
	}
	if len(lim.keys) != 1 || !strings.HasSuffix(lim.keys[0], ":203.0.113.9") {
		t.Fatalf("keys %v", lim.keys)
	}

	lim.keys = nil
	w = serve(func(c *gin.Context) {
		if h.allowSearch(c) {
			c.Status(http.StatusOK)
		}
	}, http.MethodGet, "/v1/qa/search?q=ab", "", map[string]string{"X-User-ID": uuid.NewString()}, nil)
	if w.Code != http.StatusOK || len(lim.keys) != 0 {
		t.Fatalf("signed-in: status %d, counted %v", w.Code, lim.keys)
	}
}

func TestSearchBounds(t *testing.T) {
	if l, o := clampSearchPage(100, 10000); l != maxSearchLimit || o != maxSearchOffset {
		t.Fatalf("clamp = %d, %d", l, o)
	}
	if l, o := clampSearchPage(20, 40); l != 20 || o != 40 {
		t.Fatalf("clamp changed an in-bounds page: %d, %d", l, o)
	}
	long := strings.Repeat("é", maxSearchQueryLn)
	got := clipSearchQuery(long)
	if len(got) > maxSearchQueryLn || strings.ToValidUTF8(got, "") != got {
		t.Fatalf("clip produced %d bytes, valid=%v", len(got), strings.ToValidUTF8(got, "") == got)
	}
}
