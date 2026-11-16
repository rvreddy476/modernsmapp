//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// P2 breadth — discovery / event / platform domains: search, notifications,
// creator analytics, suggestions, feature flags and the AI moderation check,
// each through the gateway. Event-driven cross-service assertions (e.g. "post →
// Eventually appears in search index / notifications") are a deeper follow-up;
// these guard reachability, the read path and each route's contract.

// searchResults is GET /v1/search's legacy shape (search-service
// UniversalSearch): both keys are always present.
type searchResults struct {
	Users json.RawMessage `json:"users"`
	Posts json.RawMessage `json:"posts"`
}

func wantSearchResults(t *testing.T, env *Envelope, what string) {
	t.Helper()
	var res searchResults
	if !wantData(t, env, what, http.StatusOK, &res) {
		return
	}
	if res.Users == nil || res.Posts == nil {
		t.Errorf("%s: data must carry both \"users\" and \"posts\", got %s", what, string(env.Data))
	}
}

// TestE2E_Search_Anonymous: a signed-out viewer has no block relationships,
// so search answers them from the index alone (search-service
// resolveBlockScope) and needs no other service.
func TestE2E_Search_Anonymous(t *testing.T) {
	gw, ctx := gatewayForFlow(t)
	anonymous := NewHTTPClient(gw, uuid.Nil)

	wantSearchResults(t, anonymous.MustDo(t, ctx, "GET", "/v1/search?q=test", nil), "anonymous GET /v1/search?q=test")

	// A query is required, and the type filter is an allowlist.
	wantRefusal(t, anonymous.MustDo(t, ctx, "GET", "/v1/search?q=", nil),
		"GET /v1/search with an empty query", http.StatusBadRequest, "BAD_REQUEST")
	wantRefusal(t, anonymous.MustDo(t, ctx, "GET", "/v1/search?q=test&type=nonsense", nil),
		"GET /v1/search with an unknown type", http.StatusBadRequest, "BAD_REQUEST")
}

// TestE2E_Search_Query: a signed-in viewer's search. search-service first
// reads the viewer's blocks and mutes from graph-service and FAILS CLOSED
// (503 BLOCK_STATE_UNAVAILABLE) when it cannot, so this flow needs
// graph-service in the stack; without it the 503 is the product working as
// designed and the stack being short a service.
func TestE2E_Search_Query(t *testing.T) {
	gw, ctx := gatewayForFlow(t)
	viewer := NewHTTPClient(gw, uuid.New())

	env := viewer.MustDo(t, ctx, "GET", "/v1/search?q=test", nil)
	if env.Status == http.StatusServiceUnavailable && envelopeCode(env) == "BLOCK_STATE_UNAVAILABLE" {
		t.Fatalf("signed-in GET /v1/search?q=test: 503 BLOCK_STATE_UNAVAILABLE: search-service could not read the viewer's blocks from graph-service (GRAPH_SERVICE_URL); start graph-service (and user-service, whose tables it needs) with this stack")
	}
	wantSearchResults(t, env, "signed-in GET /v1/search?q=test")
}

func TestE2E_Notification_List(t *testing.T) { get200(t, "/v1/notifications") }

// TestE2E_Analytics_Overview: the creator dashboard overview lives at
// /v1/analytics/dashboard/overview (analytics-service dashboard.go). A
// creator with no content reads zeros, not an error; a signed-out caller is
// 401.
func TestE2E_Analytics_Overview(t *testing.T) {
	gw, ctx := gatewayForFlow(t)
	creator := NewHTTPClient(gw, uuid.New())
	anonymous := NewHTTPClient(gw, uuid.Nil)

	var overview struct {
		TotalViews       *int64   `json:"total_views"`
		TotalWatchTimeMS *int64   `json:"total_watch_time_ms"`
		AvgCQS           *float64 `json:"avg_cqs"`
		TotalLikes       *int64   `json:"total_likes"`
		TotalShares      *int64   `json:"total_shares"`
	}
	env := creator.MustDo(t, ctx, "GET", "/v1/analytics/dashboard/overview?period=7d", nil)
	if wantData(t, env, "GET /v1/analytics/dashboard/overview", http.StatusOK, &overview) {
		if overview.TotalViews == nil || overview.TotalWatchTimeMS == nil || overview.AvgCQS == nil ||
			overview.TotalLikes == nil || overview.TotalShares == nil {
			t.Errorf("overview: a total is missing, got %s", string(env.Data))
		} else if *overview.TotalViews != 0 || *overview.TotalWatchTimeMS != 0 || *overview.TotalLikes != 0 || *overview.TotalShares != 0 {
			t.Errorf("overview for a creator with no content: want zeros, got %s", string(env.Data))
		}
	}

	wantRefusal(t, anonymous.MustDo(t, ctx, "GET", "/v1/analytics/dashboard/overview", nil),
		"anonymous GET /v1/analytics/dashboard/overview", http.StatusUnauthorized, "UNAUTHORIZED")
}

func TestE2E_Suggestion_List(t *testing.T) { get200(t, "/v1/suggestions") }
func TestE2E_FeatureFlag_Me(t *testing.T)  { get200(t, "/v1/flags/me") }

// TestE2E_AI_Moderation hits the (stub) moderation classifier: a check names
// the content (content_type + content_id) and may carry its text; the verdict
// for benign text is "allow", and asking again about the same content returns
// the recorded verdict. A check that does not name its content is a 400.
func TestE2E_AI_Moderation(t *testing.T) {
	gw, ctx := gatewayForFlow(t)
	c := NewHTTPClient(gw, uuid.New())

	type verdict struct {
		ContentType string    `json:"content_type"`
		ContentID   uuid.UUID `json:"content_id"`
		Action      string    `json:"action"`
		TextScore   *float32  `json:"text_score"`
	}
	contentID := uuid.New()
	body := map[string]any{
		"content_type": "post",
		"content_id":   contentID.String(),
		"text":         "e2e benign sample text",
	}

	var first verdict
	if !wantData(t, c.MustDo(t, ctx, "POST", "/v1/ai/moderation/check", body), "POST /v1/ai/moderation/check", http.StatusOK, &first) {
		t.FailNow()
	}
	if first.ContentType != "post" || first.ContentID != contentID || first.Action != "allow" {
		t.Errorf("verdict: got type=%q id=%s action=%q, want post, %s and allow", first.ContentType, first.ContentID, first.Action, contentID)
	}
	if first.TextScore == nil {
		t.Errorf("verdict for a check that carried text has no text_score")
	}

	var again verdict
	if wantData(t, c.MustDo(t, ctx, "POST", "/v1/ai/moderation/check", body), "POST /v1/ai/moderation/check again", http.StatusOK, &again) {
		if again.ContentID != contentID || again.Action != first.Action {
			t.Errorf("second verdict: got id=%s action=%q, want the first one (%s, %q)", again.ContentID, again.Action, contentID, first.Action)
		}
	}

	wantRefusal(t, c.MustDo(t, ctx, "POST", "/v1/ai/moderation/check", map[string]any{"text": "e2e benign sample text"}),
		"POST /v1/ai/moderation/check without content_type and content_id", http.StatusBadRequest, "INVALID_REQUEST")
}
