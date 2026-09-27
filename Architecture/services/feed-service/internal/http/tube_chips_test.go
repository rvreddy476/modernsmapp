package http

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MTube (2026-09-27): `sort` and `chip` on /v1/feed/videos, `chip` on
// /v1/feed/videos/:postId/related. An unknown value is a 400 with a named
// code, decided BEFORE the service is touched — the handler is built with a
// nil service on purpose: reaching it would panic.

func TestTubeChips_UnknownValuesAreRefusedBeforeTheService(t *testing.T) {
	h := New(nil)
	postID := uuid.New().String()
	cases := []struct {
		name     string
		target   string
		related  bool
		wantCode string
	}{
		{"videos: unknown sort", "/v1/feed/videos?sort=trending", false, "INVALID_SORT"},
		{"videos: unknown chip", "/v1/feed/videos?chip=viral", false, "INVALID_CHIP"},
		{"videos: topic chip belongs to related only", "/v1/feed/videos?chip=topic:music", false, "INVALID_CHIP"},
		{"videos: sort checked even with a category", "/v1/feed/videos?category=music&sort=x", false, "INVALID_SORT"},
		{"videos: category still validated first", "/v1/feed/videos?category=Not%20A%20Slug&chip=fresh", false, "INVALID_CATEGORY"},
		{"related: unknown chip", "/v1/feed/videos/" + postID + "/related?chip=viral", true, "INVALID_CHIP"},
		{"related: new_to_you has no meaning under a seed", "/v1/feed/videos/" + postID + "/related?chip=new_to_you", true, "INVALID_CHIP"},
		{"related: topic slug too short", "/v1/feed/videos/" + postID + "/related?chip=topic:a", true, "INVALID_CHIP"},
		{"related: topic slug with an underscore", "/v1/feed/videos/" + postID + "/related?chip=topic:hip_hop", true, "INVALID_CHIP"},
		{"related: topic slug with a symbol", "/v1/feed/videos/" + postID + "/related?chip=topic:mu$ic", true, "INVALID_CHIP"},
		{"related: empty topic", "/v1/feed/videos/" + postID + "/related?chip=topic:", true, "INVALID_CHIP"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, w := tubeTestContext(tc.target)
			handle := h.GetLongVideoFeed
			if tc.related {
				c.Params = gin.Params{{Key: "postId", Value: postID}}
				handle = h.GetRelatedVideos
			}
			handle(c)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
			}
			if code := errorCode(t, w); code != tc.wantCode {
				t.Fatalf("error code = %q, want %s", code, tc.wantCode)
			}
		})
	}
}

// The parser itself: every accepted spelling, and what it normalises to.
func TestTubeFilterParams_AcceptedValues(t *testing.T) {
	cases := []struct {
		query              string
		wantSort, wantChip string
		wantZero           bool
	}{
		{"", "recent", "", true},
		{"?sort=recent", "recent", "", true},
		{"?sort=popular", "popular", "", false},
		{"?sort=POPULAR", "popular", "", false},
		{"?chip=fresh", "recent", "fresh", false},
		{"?chip=seen", "recent", "seen", false},
		{"?chip=new_to_you", "recent", "new_to_you", false},
		{"?chip=%20Fresh%20", "recent", "fresh", false},
		{"?chip=fresh&sort=popular", "popular", "fresh", false},
	}
	for _, tc := range cases {
		c, w := tubeTestContext("/v1/feed/videos" + tc.query)
		f, ok := tubeFilterParams(c, "")
		if !ok || w.Code != http.StatusOK {
			t.Fatalf("%q: refused (ok=%v status=%d)", tc.query, ok, w.Code)
		}
		if f.Sort != tc.wantSort || f.Chip != tc.wantChip || f.IsZero() != tc.wantZero {
			t.Fatalf("%q: filter=%+v zero=%v, want sort=%s chip=%s zero=%v", tc.query, f, f.IsZero(), tc.wantSort, tc.wantChip, tc.wantZero)
		}
	}
	// A category alone is a non-zero filter: it takes the collected path.
	c, _ := tubeTestContext("/v1/feed/videos")
	if f, ok := tubeFilterParams(c, "music"); !ok || f.IsZero() || f.Category != "music" {
		t.Fatalf("category-only filter = %+v (ok=%v), want a non-zero filter carrying the category", f, ok)
	}
}
