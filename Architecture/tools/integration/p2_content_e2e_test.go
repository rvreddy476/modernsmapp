//go:build integration

package integration

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// P2 breadth — content domains, through the gateway.
//
// Launch is four apps (Social, Shorts, Long video, Commerce). Groups,
// Communities, Q&A and Memories are built and their services run, but each is
// closed at the edge by its own gateway flag, default closed (api-gateway
// main.go, dormantProductsFromEnv and serveDormantProductGate):
//
//	/v1/groups       GROUPS_PUBLIC_ENABLED
//	/v1/communities  COMMUNITIES_PUBLIC_ENABLED
//	/v1/qa           QA_PUBLIC_ENABLED
//	/v1/memories     MEMORIES_PUBLIC_ENABLED
//
// A closed product answers 404 NOT_FOUND for every method on every path
// under its prefix, signed in or not, before any upstream is reached (404,
// not 503: an edge client must not learn a product exists behind the door),
// and GET /v1/features reports it enabled=false so clients hide it (founder
// decision, 24 Oct 2026). Broadcast channels have no gate: chat's Channels
// and Communities lanes ride on them, and chat is part of Social.
//
// openProductsEnv tells these flows which parked products the stack under
// test opened: a comma or space separated list of /v1/features keys (groups,
// communities, qa, memories), each meaning the gateway runs with that
// product's flag set to "true". Unset: all four are closed. The flows never
// guess: the closed flow of a product runs unless it is listed, the open one
// only when it is.
const openProductsEnv = "ATPOST_E2E_OPEN_PRODUCTS"

func productOpen(key string) bool {
	for _, k := range strings.FieldsFunc(os.Getenv(openProductsEnv), func(r rune) bool { return r == ',' || r == ' ' }) {
		if k == key {
			return true
		}
	}
	return false
}

// get200 asserts an authenticated read returns 200 through the gateway
// (routing and the read path of a service that is open here).
func get200(t *testing.T, path string) {
	t.Helper()
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	c := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if env := c.MustDo(t, ctx, "GET", path, nil); env.Status != 200 {
		t.Errorf("GET %s: want 200, got %d", path, env.Status)
	}
}

// wantFeature asserts what GET /v1/features says about one product. The
// answer is public and is the anonymous view, so it is read without a token.
func wantFeature(t *testing.T, ctx context.Context, gw, key string, enabled bool) {
	t.Helper()
	var features struct {
		Products map[string]struct {
			Enabled bool `json:"enabled"`
		} `json:"products"`
	}
	env := NewHTTPClient(gw, uuid.Nil).MustDo(t, ctx, "GET", "/v1/features", nil)
	if !wantData(t, env, "GET /v1/features", http.StatusOK, &features) {
		return
	}
	state, listed := features.Products[key]
	if !listed {
		t.Errorf("GET /v1/features does not list %q", key)
		return
	}
	if state.Enabled != enabled {
		t.Errorf("GET /v1/features: %s enabled=%t, want %t", key, state.Enabled, enabled)
	}
}

// parkedProductClosed asserts a parked product's closed door: 404 NOT_FOUND
// on its prefix, on the read the open flow uses and on a write, for a
// signed-in caller and an anonymous one, and enabled=false in /v1/features.
func parkedProductClosed(t *testing.T, key, prefix, read string) {
	t.Helper()
	gw, ctx := gatewayForFlow(t)
	if productOpen(key) {
		t.Skipf("%s lists %q: this stack runs with the product open", openProductsEnv, key)
	}
	user := NewHTTPClient(gw, uuid.New())
	anonymous := NewHTTPClient(gw, uuid.Nil)

	for _, tc := range []struct {
		who    string
		client *HTTPClient
		method string
		path   string
		body   any
	}{
		{"signed-in user", user, "GET", read, nil},
		{"signed-in user", user, "GET", prefix, nil},
		{"signed-in user", user, "POST", prefix, map[string]any{"name": "e2e"}},
		{"signed-in user", user, "GET", prefix + "/" + uuid.NewString(), nil},
		{"anonymous caller", anonymous, "GET", read, nil},
		{"anonymous caller", anonymous, "POST", prefix, map[string]any{"name": "e2e"}},
	} {
		env := tc.client.MustDo(t, ctx, tc.method, tc.path, tc.body)
		wantRefusal(t, env, tc.who+" "+tc.method+" "+tc.path, http.StatusNotFound, "NOT_FOUND")
	}
	wantFeature(t, ctx, gw, key, false)
}

// parkedProductOpen is the on-path for a stack that opened the product: the
// read answers 200 and /v1/features says enabled=true.
func parkedProductOpen(t *testing.T, key, read string) {
	t.Helper()
	gw, ctx := gatewayForFlow(t)
	if !productOpen(key) {
		t.Skipf("%s does not list %q: the product is closed on this stack", openProductsEnv, key)
	}
	get200(t, read)
	wantFeature(t, ctx, gw, key, true)
}

const (
	groupsRead      = "/v1/groups/discover"
	communitiesRead = "/v1/communities/discover"
	qaRead          = "/v1/qa/drafts/questions"
	memoriesRead    = "/v1/memories/collections"
)

func TestE2E_Group_ClosedAtLaunch(t *testing.T) {
	parkedProductClosed(t, "groups", "/v1/groups", groupsRead)
}
func TestE2E_Community_ClosedAtLaunch(t *testing.T) {
	parkedProductClosed(t, "communities", "/v1/communities", communitiesRead)
}
func TestE2E_QA_ClosedAtLaunch(t *testing.T) {
	parkedProductClosed(t, "qa", "/v1/qa", qaRead)
}
func TestE2E_Memories_ClosedAtLaunch(t *testing.T) {
	parkedProductClosed(t, "memories", "/v1/memories", memoriesRead)
}

func TestE2E_Group_Discover(t *testing.T)       { parkedProductOpen(t, "groups", groupsRead) }
func TestE2E_Community_Discover(t *testing.T)   { parkedProductOpen(t, "communities", communitiesRead) }
func TestE2E_QA_Drafts(t *testing.T)            { parkedProductOpen(t, "qa", qaRead) }
func TestE2E_Memories_Collections(t *testing.T) { parkedProductOpen(t, "memories", memoriesRead) }

// TestE2E_Channel_Discover: broadcast channels are part of the Social launch
// and are never gated: the discover read answers, and /v1/features says so.
func TestE2E_Channel_Discover(t *testing.T) {
	gw, ctx := gatewayForFlow(t)
	get200(t, "/v1/broadcast-channels/discover")
	wantFeature(t, ctx, gw, "broadcast_channels", true)
}
