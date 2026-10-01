package http

// Engagement rules that need no database: the throttles, the route table,
// and the founder's rule that a dislike count is sent NOWHERE — checked
// against every golden fixture a client copies.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestWindowLimiter(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	l := newWindowLimiter(3, time.Minute)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !l.allow("a") {
			t.Fatalf("event %d refused inside the limit", i+1)
		}
	}
	if l.allow("a") {
		t.Fatal("the fourth event in a minute was admitted")
	}
	if !l.allow("b") {
		t.Fatal("another key was refused")
	}
	now = now.Add(time.Minute)
	if !l.allow("a") {
		t.Fatal("a new window was refused")
	}
	var nilLimiter *windowLimiter
	if !nilLimiter.allow("x") {
		t.Fatal("a nil limiter refused")
	}
}

func TestShareOncePerWindow(t *testing.T) {
	lim := newEngagementLimits()
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	lim.shareOnce.now = func() time.Time { return now }
	if !lim.shareOnce.allow("u:1|p") || lim.shareOnce.allow("u:1|p") {
		t.Fatal("a second share inside the window was counted")
	}
	if !lim.shareOnce.allow("u:1|q") {
		t.Fatal("a share of another product was not counted")
	}
	now = now.Add(10 * time.Minute)
	if !lim.shareOnce.allow("u:1|p") {
		t.Fatal("a share after the window was not counted")
	}
}

func TestEngagementRoutesRegistered(t *testing.T) {
	r := gin.New()
	(&Handler{}).RegisterEngagementRoutes(r.Group("/v1/commerce"))
	got := map[string]bool{}
	for _, ri := range r.Routes() {
		got[ri.Method+" "+ri.Path] = true
	}
	for _, want := range []string{
		"GET /v1/commerce/products/:productId/delivery-estimate",
		"PUT /v1/commerce/products/:productId/reaction",
		"DELETE /v1/commerce/products/:productId/reaction",
		"POST /v1/commerce/products/:productId/share",
		"PUT /v1/commerce/reviews/:reviewId/vote",
		"DELETE /v1/commerce/reviews/:reviewId/vote",
	} {
		if !got[want] {
			t.Errorf("%s is not registered", want)
		}
	}
}

// dislikeKeyRe is any JSON KEY that mentions a dislike; dislikeValueRe is any
// key whose VALUE is the string "dislike".
var (
	dislikeKeyRe   = regexp.MustCompile(`(?i)"([a-z_]*dislike[a-z_]*)"\s*:`)
	dislikeValueRe = regexp.MustCompile(`"([a-z_]+)"\s*:\s*"dislike"`)
)

// The founder's rule (1 Oct 2026): the like count is public, a dislike is
// private to the person who cast it. So no fixture — the contract every
// client copies — may carry a dislike count or any other dislike key, and
// the string "dislike" may appear only as the caller's OWN viewer_reaction.
func TestNoDislikeCountInAnyFixture(t *testing.T) {
	sawOwn := false
	err := filepath.WalkDir(contractsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range dislikeKeyRe.FindAllStringSubmatch(string(b), -1) {
			t.Errorf("%s: a dislike key %q is on the wire", path, m[1])
		}
		for _, m := range dislikeValueRe.FindAllStringSubmatch(string(b), -1) {
			if m[1] != "viewer_reaction" {
				t.Errorf("%s: %q carries a dislike; only the caller's own viewer_reaction may", path, m[1])
			}
			sawOwn = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawOwn {
		t.Fatal("no fixture shows a viewer's own dislike; the scan is not looking at the reaction fixtures")
	}
}
