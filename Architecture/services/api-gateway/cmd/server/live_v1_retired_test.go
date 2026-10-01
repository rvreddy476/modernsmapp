package main

// NOTE: new files under cmd/server/ are git-ignored by the root `server` rule;
// this file must be added with `git add -f`.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

var liveV1Paths = []string{
	"/v1/live",
	"/v1/live/",
	"/v1/live/start",
	"/v1/live/streams",
	"/v1/live/streams/11111111-1111-4111-8111-111111111111/playback",
	"/v1/live/stream/abc/whip",
}

// Paths that share the letters but are not the v1 API. /v1/livestream is the
// v2 (LiveKit) stack and must keep proxying.
var notLiveV1Paths = []string{
	"/v1/livestream",
	"/v1/livestream/streams",
	"/v1/livestream/webhooks/livekit",
	"/v1/lives",
	"/v1/live-events",
	"/v1/memories",
}

func TestLiveV1RetiredAnswers410WithCode(t *testing.T) {
	for _, path := range liveV1Paths {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			res := httptest.NewRecorder()
			req := httptest.NewRequest(method, path, nil)
			if !serveLiveV1Retired(res, req) {
				t.Fatalf("%s %s: not handled", method, path)
			}
			if res.Code != http.StatusGone {
				t.Fatalf("%s %s: status %d, want 410", method, path, res.Code)
			}
			if ct := res.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("%s %s: content type %q", method, path, ct)
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s %s: body %q: %v", method, path, res.Body.String(), err)
			}
			if body.Error.Code != "LIVE_V1_RETIRED" {
				t.Fatalf("%s %s: code %q, want LIVE_V1_RETIRED", method, path, body.Error.Code)
			}
		}
	}
}

func TestLiveV1GateLeavesOtherPathsAlone(t *testing.T) {
	for _, path := range notLiveV1Paths {
		res := httptest.NewRecorder()
		if serveLiveV1Retired(res, httptest.NewRequest(http.MethodGet, path, nil)) {
			t.Fatalf("%s was caught by the v1 retirement gate", path)
		}
	}
}

// The route table must not proxy v1 at all, so nothing can reach the unused
// MediaMTX stack even if the gate were bypassed.
func TestLiveV1HasNoRoute(t *testing.T) {
	sawV2 := false
	for _, rd := range routeDefinitions() {
		if rd.prefix == liveV1Prefix {
			t.Fatalf("route table still proxies %s to %s", rd.prefix, rd.target)
		}
		if rd.prefix == "/v1/livestream" {
			sawV2 = true
		}
	}
	if !sawV2 {
		t.Fatal("route table lost /v1/livestream (v2)")
	}
}

// Through the production chain: v1 is 410 for signed-in and anonymous callers
// and never reaches an upstream; v2 still proxies.
func TestEdgeRetiresLiveV1AndKeepsLivestream(t *testing.T) {
	up := newRecordingUpstream(t)
	gw := edgeGateway(t, up, nil)
	token := edgeToken(t, "", "")

	for _, path := range liveV1Paths {
		for _, auth := range []string{"", "Bearer " + token} {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			res := httptest.NewRecorder()
			gw.ServeHTTP(res, req)
			if res.Code != http.StatusGone {
				t.Fatalf("POST %s (auth=%t): status %d, want 410", path, auth != "", res.Code)
			}
		}
	}
	if hits := up.take(); len(hits) != 0 {
		t.Fatalf("retired v1 paths reached an upstream: %+v", hits)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/livestream/streams", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	gw.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /v1/livestream/streams: status %d, want proxied 200", res.Code)
	}
	hits := up.take()
	if len(hits) != 1 || hits[0].path != "/v1/livestream/streams" {
		t.Fatalf("v2 upstream hits = %+v", hits)
	}
}
