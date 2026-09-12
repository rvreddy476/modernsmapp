package http

import (
	"net/http"
	"strings"
	"testing"
)

// Watch history (Tube "You" page, 2026-09-12).
//
// continue-watching shows only what is unfinished, limit-only, so a viewer
// could never see what they had finished and never page past the first
// shelf. History is the full record, paged, and clearable in one call. The
// two static routes are read from the REAL router (the technique
// video_series_routes_test.go uses) so this cannot pass by someone writing
// a handler nobody wired up.

func TestWatchHistoryRoutesAreRegistered(t *testing.T) {
	routes := registeredRoutesWithPrefix(t, "/v1/videos")
	for _, want := range []string{
		"GET /v1/videos/history",
		"DELETE /v1/videos/history",
	} {
		if !routes[want] {
			t.Errorf("%s is not registered. Registered routes under /v1/videos: %v", want, keysOf(routes))
		}
	}
}

// History is the viewer's own, so an anonymous caller must be refused at
// the handler, before anything reaches the (nil) service.
//
// The 401 also proves "history" was not taken as a video id: GET
// /v1/videos/:videoId parses the id BEFORE it looks at the caller, so had
// the param route won the match the answer would be 400 INVALID_ID.
func TestWatchHistoryRequiresIdentity(t *testing.T) {
	cases := []struct{ method, path string }{
		{http.MethodGet, "/v1/videos/history"},
		{http.MethodDelete, "/v1/videos/history"},
		{http.MethodGet, "/v1/videos/history?limit=5"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := performVideoSeriesRequest(t, tc.method, tc.path, nil)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s with no X-User-Id: status=%d want 401 (body %s)",
					tc.method, tc.path, w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "INVALID_ID") {
				t.Fatalf("%s %s reached a :videoId handler: %s", tc.method, tc.path, w.Body.String())
			}
		})
	}
}

// Page limits: default 20, and anything above 100 is clamped to 100 rather
// than ignored, so a client asking for 500 gets the largest page we serve
// instead of silently getting the default.
func TestPageLimitClampsTo100(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", 20},
		{"abc", 20},
		{"0", 20},
		{"-3", 20},
		{"5", 5},
		{"100", 100},
		{"101", 100},
		{"500", 100},
	}
	for _, tc := range cases {
		if got := pageLimit(tc.raw, 20, 100); got != tc.want {
			t.Errorf("pageLimit(%q) = %d want %d", tc.raw, got, tc.want)
		}
	}
}

// Bookmarks take the same type filter as by-author; an unknown value is a
// 400, not a silently unfiltered page. Identity is checked first.
func TestBookmarksRejectUnknownType(t *testing.T) {
	w := performVideoSeriesRequest(t, http.MethodGet, "/v1/posts/bookmarks?type=movie", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous bookmarks: status=%d want 401", w.Code)
	}
	w = performVideoSeriesRequest(t, http.MethodGet, "/v1/posts/bookmarks?type=movie",
		map[string]string{"X-User-Id": "0b6c7f6e-5b7a-4c1a-9c2f-3d4e5f6a7b8c"})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "INVALID_CONTENT_TYPE") {
		t.Fatalf("type=movie: status=%d body=%s want 400 INVALID_CONTENT_TYPE", w.Code, w.Body.String())
	}
}
