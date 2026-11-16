//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
)

// Live. Two facts of the current product:
//
//   - v1 (`/v1/live`, MediaMTX through live-service) was retired on 1 Oct
//     2026. The gateway answers 410 LIVE_V1_RETIRED for the whole prefix
//     (api-gateway main.go, serveLiveV1Retired) and has no route to
//     live-service any more.
//   - v2 (`/v1/livestream`, LiveKit through live-service-v2) is the only live
//     stack. Anyone may read; going live is behind LIVE_ACCESS_MODE, "pilot"
//     by default: only the users on live-service-v2's LIVE_PILOT_USER_IDS may
//     create or start a stream, everyone else gets 403 LIVE_NOT_ENABLED.
//
// The full go-live → viewer-join journey needs a LiveKit server and is not
// covered here; these flows stop where LiveKit starts.

// livePilotUserEnv carries the id of one user the stack under test lists in
// live-service-v2's LIVE_PILOT_USER_IDS. Unset: the pilot-host flow skips.
const livePilotUserEnv = "ATPOST_E2E_LIVE_PILOT_USER"

type liveEligibility struct {
	Mode      string `json:"mode"`
	Eligible  bool   `json:"eligible"`
	PilotOnly bool   `json:"pilot_only"`
}

type liveStream struct {
	ID            uuid.UUID `json:"id"`
	CreatorUserID uuid.UUID `json:"creator_user_id"`
	Title         string    `json:"title"`
	Status        string    `json:"status"`
	EndedReason   *string   `json:"ended_reason"`
}

// TestE2E_Live_V1Retired guards the retirement: every method on every path
// under /v1/live is 410 LIVE_V1_RETIRED, signed in or not, and never reaches
// an upstream.
func TestE2E_Live_V1Retired(t *testing.T) {
	gw, ctx := gatewayForFlow(t)
	user := NewHTTPClient(gw, uuid.New())
	anonymous := NewHTTPClient(gw, uuid.Nil)

	for _, tc := range []struct {
		who    string
		client *HTTPClient
		method string
		path   string
		body   any
	}{
		{"signed-in user", user, "GET", "/v1/live/streams", nil},
		{"signed-in user", user, "POST", "/v1/live/streams", map[string]any{"title": "e2e"}},
		{"signed-in user", user, "GET", "/v1/live", nil},
		{"signed-in user", user, "GET", "/v1/live/streams/" + uuid.NewString(), nil},
		{"anonymous caller", anonymous, "GET", "/v1/live/streams", nil},
	} {
		env := tc.client.MustDo(t, ctx, tc.method, tc.path, tc.body)
		wantRefusal(t, env, tc.who+" "+tc.method+" "+tc.path, http.StatusGone, "LIVE_V1_RETIRED")
	}
}

// TestE2E_LiveV2_Streams guards the v2 read path through the gateway: the
// live-now listing answers a signed-in viewer and an anonymous one with a
// list, and an unknown status filter is a 400, not an unfiltered list.
func TestE2E_LiveV2_Streams(t *testing.T) {
	gw, ctx := gatewayForFlow(t)
	user := NewHTTPClient(gw, uuid.New())
	anonymous := NewHTTPClient(gw, uuid.Nil)

	for who, c := range map[string]*HTTPClient{"signed-in user": user, "anonymous caller": anonymous} {
		var rows []json.RawMessage
		wantData(t, c.MustDo(t, ctx, "GET", "/v1/livestream/streams", nil),
			who+" GET /v1/livestream/streams", http.StatusOK, &rows)
	}

	wantRefusal(t, user.MustDo(t, ctx, "GET", "/v1/livestream/streams?status=nonsense", nil),
		"GET /v1/livestream/streams?status=nonsense", http.StatusBadRequest, "INVALID_REQUEST")

	// A stream that does not exist is a 404, not an empty 200.
	wantRefusal(t, user.MustDo(t, ctx, "GET", "/v1/livestream/streams/"+uuid.NewString(), nil),
		"GET /v1/livestream/streams/<unknown id>", http.StatusNotFound, "NOT_FOUND")
}

// TestE2E_LiveV2_PilotGate guards the launch gate on going live: a user who
// is not on the pilot list is told so by the eligibility read and is refused
// when creating a stream, with 403 LIVE_NOT_ENABLED. An anonymous caller is
// 401 on both.
func TestE2E_LiveV2_PilotGate(t *testing.T) {
	gw, ctx := gatewayForFlow(t)
	outsider := NewHTTPClient(gw, uuid.New()) // a fresh id is never on the list
	anonymous := NewHTTPClient(gw, uuid.Nil)

	var elig liveEligibility
	if wantData(t, outsider.MustDo(t, ctx, "GET", "/v1/livestream/eligibility", nil),
		"GET /v1/livestream/eligibility", http.StatusOK, &elig) {
		if elig.Mode != "pilot" {
			t.Errorf("eligibility: mode is %q, want %q (LIVE_ACCESS_MODE is pilot unless set)", elig.Mode, "pilot")
		}
		if elig.Eligible || !elig.PilotOnly {
			t.Errorf("eligibility for a user off the pilot list: got eligible=%t pilot_only=%t, want false and true", elig.Eligible, elig.PilotOnly)
		}
	}

	wantRefusal(t, outsider.MustDo(t, ctx, "POST", "/v1/livestream/streams", map[string]any{"title": "e2e outsider"}),
		"user off the pilot list on POST /v1/livestream/streams", http.StatusForbidden, "LIVE_NOT_ENABLED")

	wantRefusal(t, anonymous.MustDo(t, ctx, "GET", "/v1/livestream/eligibility", nil),
		"anonymous GET /v1/livestream/eligibility", http.StatusUnauthorized, "UNAUTHORIZED")
	wantRefusal(t, anonymous.MustDo(t, ctx, "POST", "/v1/livestream/streams", map[string]any{"title": "e2e anonymous"}),
		"anonymous POST /v1/livestream/streams", http.StatusUnauthorized, "UNAUTHORIZED")
}

// TestE2E_LiveV2_PilotHost is the open side of the same gate, for a stack
// whose live-service-v2 lists the user in LIVE_PILOT_USER_IDS:
//
//	eligible → create (201, scheduled) → host reads it → a stranger cannot
//	end it → host ends it (ended, host_ended) → it cannot be started again
//
// It stops before start: start opens a LiveKit room, and this stack has no
// LiveKit server.
func TestE2E_LiveV2_PilotHost(t *testing.T) {
	gw, ctx := gatewayForFlow(t)
	raw := os.Getenv(livePilotUserEnv)
	if raw == "" {
		t.Skipf("going live is pilot-only; list a user id in live-service-v2's LIVE_PILOT_USER_IDS and set %s to the same id to run the host path", livePilotUserEnv)
	}
	hostID, err := uuid.Parse(raw)
	if err != nil || hostID == uuid.Nil {
		t.Fatalf("%s must be a user id (UUID)", livePilotUserEnv)
	}
	host := NewHTTPClient(gw, hostID)
	stranger := NewHTTPClient(gw, uuid.New())

	var elig liveEligibility
	if wantData(t, host.MustDo(t, ctx, "GET", "/v1/livestream/eligibility", nil),
		"pilot host GET /v1/livestream/eligibility", http.StatusOK, &elig) {
		if !elig.Eligible || elig.PilotOnly {
			t.Errorf("eligibility for a pilot host: got eligible=%t pilot_only=%t, want true and false (is %s in LIVE_PILOT_USER_IDS?)", elig.Eligible, elig.PilotOnly, livePilotUserEnv)
		}
	}

	title := "e2e pilot stream " + uuid.NewString()
	var created liveStream
	if !wantData(t, host.MustDo(t, ctx, "POST", "/v1/livestream/streams", map[string]any{"title": title}),
		"pilot host POST /v1/livestream/streams", http.StatusCreated, &created) {
		t.FailNow()
	}
	if created.ID == uuid.Nil || created.CreatorUserID != hostID || created.Title != title || created.Status != "scheduled" {
		t.Fatalf("created stream: got id=%s creator=%s title=%q status=%q, want a new id, creator %s, the title sent and status scheduled",
			created.ID, created.CreatorUserID, created.Title, created.Status, hostID)
	}
	streamPath := "/v1/livestream/streams/" + created.ID.String()

	var read liveStream
	if wantData(t, host.MustDo(t, ctx, "GET", streamPath, nil), "host GET "+streamPath, http.StatusOK, &read) {
		if read.ID != created.ID || read.Status != "scheduled" {
			t.Errorf("host read: got id=%s status=%q, want id=%s status=scheduled", read.ID, read.Status, created.ID)
		}
	}

	// Only the host ends a stream.
	wantRefusal(t, stranger.MustDo(t, ctx, "POST", streamPath+"/end", nil),
		"stranger POST "+streamPath+"/end", http.StatusForbidden, "FORBIDDEN")

	var ended liveStream
	if !wantData(t, host.MustDo(t, ctx, "POST", streamPath+"/end", nil), "host POST "+streamPath+"/end", http.StatusOK, &ended) {
		t.FailNow()
	}
	if ended.Status != "ended" || ended.EndedReason == nil || *ended.EndedReason != "host_ended" {
		reason := "<null>"
		if ended.EndedReason != nil {
			reason = *ended.EndedReason
		}
		t.Errorf("host end: got status=%q ended_reason=%s, want ended and host_ended", ended.Status, reason)
	}

	// An ended stream does not start again.
	wantRefusal(t, host.MustDo(t, ctx, "POST", streamPath+"/start", nil),
		"host POST "+streamPath+"/start after end", http.StatusConflict, "STREAM_STATE_CONFLICT")
}
