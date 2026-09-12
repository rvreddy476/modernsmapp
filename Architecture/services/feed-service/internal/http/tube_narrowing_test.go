package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// following_only and subscribed_only are different narrowings (the follow
// graph versus channel subscriptions) with different orderings; a request
// carrying both is refused as 400 INVALID_REQUEST before the service is
// touched. The handler is built with a nil service on purpose: reaching
// it would panic.

func tubeTestContext(target string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", target, nil)
	c.Request.Header.Set("X-User-Id", uuid.New().String())
	return c, w
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", w.Body.String(), err)
	}
	return body.Error.Code
}

func TestTubeNarrowing_BothFlagsIsInvalidRequest(t *testing.T) {
	h := New(nil)
	for name, handle := range map[string]gin.HandlerFunc{
		"watch":  h.GetVideoFeed,
		"videos": h.GetLongVideoFeed,
	} {
		t.Run(name, func(t *testing.T) {
			c, w := tubeTestContext("/v1/feed/" + name + "?following_only=true&subscribed_only=true")
			handle(c)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if code := errorCode(t, w); code != "INVALID_REQUEST" {
				t.Fatalf("error code = %q, want INVALID_REQUEST", code)
			}
		})
	}
}

// Either flag alone parses as itself; the check must not refuse the
// ordinary tabs.
func TestTubeNarrowing_SingleFlagsParse(t *testing.T) {
	cases := []struct {
		query                         string
		wantFollowing, wantSubscribed bool
	}{
		{"", false, false},
		{"?following_only=true", true, false},
		{"?subscribed_only=true", false, true},
		{"?subscribed_only=1", false, false}, // only the literal "true" counts, as for following_only
	}
	for _, tc := range cases {
		c, w := tubeTestContext("/v1/feed/watch" + tc.query)
		followingOnly, subscribedOnly, ok := tubeNarrowing(c)
		if !ok || w.Code != http.StatusOK {
			t.Fatalf("%q: refused (ok=%v status=%d)", tc.query, ok, w.Code)
		}
		if followingOnly != tc.wantFollowing || subscribedOnly != tc.wantSubscribed {
			t.Fatalf("%q: following=%v subscribed=%v, want %v %v", tc.query, followingOnly, subscribedOnly, tc.wantFollowing, tc.wantSubscribed)
		}
	}
}
