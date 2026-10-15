package http

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// Reels category chips (TikTok parity T-18): `category` on /v1/feed/reels
// and its /v1/feed/flicks alias is validated exactly as on the Tube
// surfaces — a malformed value is 400 INVALID_CATEGORY, decided BEFORE the
// service is touched. The handler is built with a nil service on purpose:
// reaching it would panic. A well-formed id outside the taxonomy is an
// empty page, pinned at the service (reels_category_test.go).

func TestReelCategory_MalformedValueIsRefusedBeforeTheService(t *testing.T) {
	h := New(nil)
	routes := map[string]gin.HandlerFunc{
		"/v1/feed/reels":  h.GetReelFeed,
		"/v1/feed/flicks": h.GetFlickFeed,
	}
	values := []string{
		"Not%20A%20Slug",                     // inner whitespace
		"mu$ic",                              // a symbol
		"-music",                             // must start with a letter or digit
		"a234567890123456789012345678901234", // longer than 32 runes
		"%C3%A9t%C3%A9",                      // non-ASCII
	}
	for path, handle := range routes {
		for _, v := range values {
			t.Run(path+"?category="+v, func(t *testing.T) {
				c, w := tubeTestContext(path + "?category=" + v)
				handle(c)
				if w.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
				}
				if code := errorCode(t, w); code != "INVALID_CATEGORY" {
					t.Fatalf("error code = %q, want INVALID_CATEGORY", code)
				}
			})
		}
	}
}

func TestReelCategory_CursorIsStillValidatedFirst(t *testing.T) {
	h := New(nil)
	c, w := tubeTestContext("/v1/feed/reels?category=music&cursor=not-a-cursor")
	h.GetReelFeed(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if code := errorCode(t, w); code != "INVALID_CURSOR" {
		t.Fatalf("error code = %q, want INVALID_CURSOR", code)
	}
}
