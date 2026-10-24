package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// Security sweep, 21 Oct 2026 (High #1, #2, #9): /relationship, /mutuals and
// /relationships/batch took the ACTOR from the query or body, so any signed-in
// user could ask "does <victim> block me?" — the one answer the relationship
// contract promises the blocked party never sees.
//
// Edge traffic is recognised by the gateway's X-Graph-Write-Source stamp, set
// on every /v1/graph request after stripping a client's own value. Through the
// edge the actor is X-User-Id and an explicit id may only repeat it; without
// the stamp the caller is an in-cluster service (dating, feed, live) that
// still names the actor, as before.

func edgeGet(path, userID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(GraphWriteSourceHeader, "api-gateway")
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
	return req
}

func edgeBatch(body map[string]any, userID string) *http.Request {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/graph/relationships/batch", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(GraphWriteSourceHeader, "api-gateway")
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
	return req
}

func errorCode(t *testing.T, resp *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp.Body.String())
	}
	return env.Error.Code
}

func TestEdgeReadsCannotNameAnotherActor(t *testing.T) {
	attacker, victim, other := uuid.New(), uuid.New(), uuid.New()
	// The victim blocks the attacker: the fact the attacker must never learn.
	r := relationshipRouter(seedBlocks(victim, attacker, blockCase{viewerBlocksTarget: true}))

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"relationship", edgeGet("/v1/graph/relationship?user_id="+victim.String()+"&other_id="+attacker.String(), attacker.String())},
		{"mutuals", edgeGet("/v1/graph/mutuals?user_id="+victim.String()+"&other_id="+other.String(), attacker.String())},
		{"batch", edgeBatch(map[string]any{"viewer_id": victim.String(), "target_ids": []string{attacker.String()}}, attacker.String())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, tc.req)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", resp.Code, resp.Body.String())
			}
			if code := errorCode(t, resp); code != "VIEWER_BOUND" {
				t.Fatalf("code %q, want VIEWER_BOUND", code)
			}
			if bytes.Contains(resp.Body.Bytes(), []byte("blocked")) {
				t.Fatalf("the refusal must not mention a block: %s", resp.Body.String())
			}
		})
	}
}

func TestEdgeReadsNeedASignedInViewer(t *testing.T) {
	victim, other := uuid.New(), uuid.New()
	r := relationshipRouter(seedBlocks(victim, other, blockCase{}))

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"relationship", edgeGet("/v1/graph/relationship?user_id="+victim.String()+"&other_id="+other.String(), "")},
		{"mutuals", edgeGet("/v1/graph/mutuals?user_id="+victim.String()+"&other_id="+other.String(), "")},
		{"batch", edgeBatch(map[string]any{"viewer_id": victim.String(), "target_ids": []string{other.String()}}, "")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, tc.req)
			if resp.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401: %s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestEdgeReadsUseTheSignedInViewer(t *testing.T) {
	viewer, target := uuid.New(), uuid.New()
	r := relationshipRouter(seedBlocks(viewer, target, blockCase{}))

	// The explicit id may repeat the viewer, or be left out entirely.
	for _, path := range []string{
		"/v1/graph/relationship?user_id=" + viewer.String() + "&other_id=" + target.String(),
		"/v1/graph/relationship?other_id=" + target.String(),
	} {
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, edgeGet(path, viewer.String()))
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", path, resp.Code, resp.Body.String())
		}
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, edgeBatch(map[string]any{"target_ids": []string{target.String()}}, viewer.String()))
	if resp.Code != http.StatusOK {
		t.Fatalf("batch without viewer_id: status %d: %s", resp.Code, resp.Body.String())
	}
}

func TestServiceReadsStillNameTheActor(t *testing.T) {
	// No gateway stamp: an in-cluster caller (dating, feed, live) behind the
	// internal key names the actor, exactly as before the fix.
	actor, target := uuid.New(), uuid.New()
	r := relationshipRouter(seedBlocks(actor, target, blockCase{}))

	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodGet,
		"/v1/graph/relationship?user_id="+actor.String()+"&other_id="+target.String(), nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	raw, _ := json.Marshal(map[string]any{"viewer_id": actor.String(), "target_ids": []string{target.String()}})
	req := httptest.NewRequest(http.MethodPost, "/v1/graph/relationships/batch", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("batch: status %d: %s", resp.Code, resp.Body.String())
	}
}

// Social audit, 24 Oct 2026: /connections/mutual-counts took viewer_id from
// the body, so anyone could read mutual-connection counts for another person,
// private accounts included. Through the edge it answers for the caller only.
func TestEdgeMutualCountsCannotNameAnotherViewer(t *testing.T) {
	attacker, victim, target := uuid.New(), uuid.New(), uuid.New()
	r := relationshipRouter(seedBlocks(victim, target, blockCase{}))

	raw, _ := json.Marshal(map[string]any{"viewer_id": victim.String(), "target_ids": []string{target.String()}})
	req := httptest.NewRequest(http.MethodPost, "/v1/graph/connections/mutual-counts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(GraphWriteSourceHeader, "api-gateway")
	req.Header.Set("X-User-Id", attacker.String())

	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", resp.Code, resp.Body.String())
	}
	if code := errorCode(t, resp); code != "VIEWER_BOUND" {
		t.Fatalf("code %q, want VIEWER_BOUND", code)
	}
}
