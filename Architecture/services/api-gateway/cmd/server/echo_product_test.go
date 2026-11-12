package main

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/atpost/api-gateway/pkg/disabledprefixes"
)

// ---- Echo (premium short notes) --------------------------------------------
//
// Echo is gated exactly like Dating and Doorstep: closed by default, its own
// flag, and an internal pilot allowlist that fails closed. These tests go
// through dormantProductsFromEnv, so they also prove Echo is wired into it.

var echoPaths = []string{
	"/v1/notes", "/v1/notes/abc", "/v1/notes/abc/replies",
	"/v1/feed/notes", "/v1/feed/notes/following", "/v1/feed/notes/for-you",
}

func TestEchoIsInTheDormantProductTable(t *testing.T) {
	for _, p := range productsFrom(t, nil) {
		if strings.Join(p.prefixes, ",") == strings.Join(echoPrefixes, ",") {
			return
		}
	}
	t.Fatal("dormantProductsFromEnv has no Echo entry; /v1/notes and /v1/feed/notes are open at the edge")
}

func TestEchoFlagDefaultsClosed(t *testing.T) {
	for _, env := range []map[string]string{
		nil,
		{"ECHO_PUBLIC_ENABLED": ""},
		{"ECHO_PUBLIC_ENABLED": "false"},
		{"ECHO_PUBLIC_ENABLED": "1"},
		{"ECHO_PUBLIC_ENABLED": "yes"},
		// The service switch is not the edge switch.
		{"ECHO_ENABLED": "true"},
	} {
		products := productsFrom(t, env)
		for _, path := range echoPaths {
			if !gated(t, products, path) {
				t.Errorf("env %v: %s was not gated", env, path)
			}
		}
	}
}

// Prefixes match on whole path segments: closing /v1/feed/notes must not
// close the home feed or anything else under /v1/feed, and /v1/notes must not
// reach a neighbour that merely starts with the same letters.
func TestEchoGateDoesNotTouchNeighbours(t *testing.T) {
	products := productsFrom(t, nil)
	for _, path := range []string{
		"/v1/feed", "/v1/feed/home", "/v1/feed/following", "/v1/feed/notesx", "/v1/feed/note",
		"/v1/feednotes", "/v1/notesx", "/v1/note", "/v1/posts", "/v1/notifications",
	} {
		if gated(t, products, path) {
			t.Errorf("%s was gated but is not Echo", path)
		}
	}
	for _, path := range []string{"/v1/feed/home", "/v1/feed/following"} {
		if got := throughGateway(t, nil, path, bearer(t, outsiderUser, "secret")); !got.reached {
			t.Errorf("signed-in %s: status %d, upstream not reached", path, got.status)
		}
	}
}

// Echo opens on ECHO_PUBLIC_ENABLED alone, and that flag opens nothing else.
func TestEchoGateIsIndependentOfOtherFlags(t *testing.T) {
	othersOpen := productsFrom(t, map[string]string{"GROUPS_PUBLIC_ENABLED": "true", "COMMUNITIES_PUBLIC_ENABLED": "true",
		"RIDER_PUBLIC_ENABLED": "true", "DATING_PUBLIC_ENABLED": "true", "DOORSTEP_PUBLIC_ENABLED": "true"})
	for _, path := range echoPaths {
		if !gated(t, othersOpen, path) {
			t.Errorf("another product's flag opened %s", path)
		}
	}

	echoOpen := productsFrom(t, map[string]string{"ECHO_PUBLIC_ENABLED": "true"})
	for _, path := range echoPaths {
		if gated(t, echoOpen, path) {
			t.Errorf("ECHO_PUBLIC_ENABLED=true still gates %s", path)
		}
	}
	for _, path := range append(append(append(append([]string{}, groupsAndCommunitiesPaths...), riderPaths...), datingPaths...), doorstepPaths...) {
		if !gated(t, echoOpen, path) {
			t.Errorf("ECHO_PUBLIC_ENABLED=true opened %s", path)
		}
	}
}

func TestEchoGateWhileClosed(t *testing.T) {
	pilot := map[string]string{"ECHO_PILOT_USER_IDS": pilotCallA + "," + pilotCallB}

	closedCases := []struct {
		name    string
		path    string
		prepare func(*http.Request)
	}{
		{"anonymous on notes", "/v1/notes", nil},
		{"anonymous on the notes feed", "/v1/feed/notes/following", nil},
		{"non-pilot user on notes", "/v1/notes", bearer(t, outsiderUser, "secret")},
		{"non-pilot user on the notes feed", "/v1/feed/notes/following", bearer(t, outsiderUser, "secret")},
		{"anonymous claiming a pilot X-User-Id", "/v1/notes", spoofUserHeaders(pilotCallA)},
		{"non-pilot user claiming a pilot X-User-Id", "/v1/feed/notes/following",
			both(bearer(t, outsiderUser, "secret"), spoofUserHeaders(pilotCallA))},
	}
	for _, tc := range closedCases {
		got := throughGateway(t, pilot, tc.path, tc.prepare)
		if got.reached || got.status != http.StatusNotFound {
			t.Errorf("%s: %s gave status %d, reached upstream=%v; want 404, not reached", tc.name, tc.path, got.status, got.reached)
		}
	}

	// A forged token carrying a pilot id never reaches Echo.
	got := throughGateway(t, pilot, "/v1/notes", both(bearer(t, pilotCallA, "not-the-gateway-secret"), spoofUserHeaders(pilotCallA)))
	if got.reached || (got.status != http.StatusUnauthorized && got.status != http.StatusNotFound) {
		t.Errorf("forged token: status %d, reached upstream=%v; want 401 or 404, not reached", got.status, got.reached)
	}

	for _, user := range []string{pilotCallA, pilotCallB} {
		for _, path := range echoPaths {
			if got := throughGateway(t, pilot, path, bearer(t, user, "secret")); !got.reached {
				t.Errorf("pilot %s on %s: status %d, upstream not reached", user, path, got.status)
			}
		}
	}
}

func TestEchoGateEmptyAllowlistLetsNobodyIn(t *testing.T) {
	for _, env := range []map[string]string{
		nil,
		{"ECHO_PILOT_USER_IDS": ""},
		{"ECHO_PILOT_USER_IDS": " , ,"},
	} {
		for _, user := range []string{pilotCallA, outsiderUser} {
			got := throughGateway(t, env, "/v1/notes", bearer(t, user, "secret"))
			if got.reached || got.status != http.StatusNotFound {
				t.Errorf("allowlist %q, user %s: status %d, reached=%v; want 404", env["ECHO_PILOT_USER_IDS"], user, got.status, got.reached)
			}
		}
	}
}

func TestEchoGateOpenLetsEveryoneThrough(t *testing.T) {
	open := map[string]string{"ECHO_PUBLIC_ENABLED": "true"}
	for _, prepare := range []func(*http.Request){nil, bearer(t, outsiderUser, "secret")} {
		for _, path := range echoPaths {
			if got := throughGateway(t, open, path, prepare); !got.reached {
				t.Errorf("ECHO_PUBLIC_ENABLED=true: %s gave status %d, upstream not reached", path, got.status)
			}
		}
	}
}

// Pilot lists are per product: a dating or doorstep pilot is not an Echo
// pilot, and an Echo pilot reaches no other closed product.
func TestEchoPilotIsSeparate(t *testing.T) {
	for _, env := range []map[string]string{
		{"DATING_PILOT_USER_IDS": pilotCallA},
		{"DOORSTEP_PILOT_USER_IDS": pilotCallA},
	} {
		if got := throughGateway(t, env, "/v1/notes", bearer(t, pilotCallA, "secret")); got.reached {
			t.Errorf("env %v: another product's pilot reached Echo", env)
		}
	}
	echoOnly := map[string]string{"ECHO_PILOT_USER_IDS": pilotCallA}
	for _, path := range append(append(append(append([]string{}, groupsAndCommunitiesPaths...), riderPaths...), datingPaths...), doorstepPaths...) {
		if got := throughGateway(t, echoOnly, path, bearer(t, pilotCallA, "secret")); got.reached {
			t.Errorf("Echo pilot user reached %s", path)
		}
	}
}

func TestEchoPilotAllowlistParsing(t *testing.T) {
	withInvalid := pilotCallA + ", call_a"
	if _, err := dormantProductsFromEnv(envOf(map[string]string{"ECHO_PILOT_USER_IDS": withInvalid}), true); err == nil {
		t.Error("production accepted an invalid ECHO_PILOT_USER_IDS entry; want a boot error")
	} else if !strings.Contains(err.Error(), "ECHO_PILOT_USER_IDS") {
		t.Errorf("boot error does not name the variable: %v", err)
	}
	dev := map[string]string{"ECHO_PILOT_USER_IDS": withInvalid}
	if got := throughGateway(t, dev, "/v1/notes", bearer(t, "call_a", "secret")); got.reached {
		t.Error("dev: an invalid allowlist entry was honoured")
	}
	if got := throughGateway(t, dev, "/v1/notes", bearer(t, pilotCallA, "secret")); !got.reached {
		t.Error("dev: the valid allowlist entry was dropped with the invalid one")
	}
}

// Unlike the parked products, Echo is closed in the dev compose too (it is
// a pilot, like Doorstep), and the production compose pins it closed.
func TestComposeEchoFlags(t *testing.T) {
	for file, want := range map[string]string{
		"../../../../docker/docker-compose.yml":            "${ECHO_PUBLIC_ENABLED:-false}",
		"../../../../docker/docker-compose.production.yml": `"false"`,
	} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		if !strings.Contains(text, "      ECHO_PUBLIC_ENABLED: "+want+"\n") {
			t.Errorf("%s does not set ECHO_PUBLIC_ENABLED: %s", file, want)
		}
	}
}

// GET /v1/features reports echo closed by default, closed with only a pilot
// list, and open on the flag; the home feed's prefix is unaffected.
func TestFeaturesReportEcho(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want bool
	}{
		{nil, false},
		{map[string]string{"ECHO_PILOT_USER_IDS": pilotCallA}, false},
		{map[string]string{"ECHO_PUBLIC_ENABLED": "false"}, false},
		{map[string]string{"ECHO_PUBLIC_ENABLED": "true"}, true},
	} {
		core := newCoreHandler(upstreamRoutes(t), nil, false, productsFrom(t, tc.env), disabledprefixes.Set{})
		_, got := getFeatures(t, core, nil)
		if enabled, ok := got["echo"]; !ok || enabled != tc.want {
			t.Errorf("env %v: echo enabled=%v (present %v), want %v", tc.env, enabled, ok, tc.want)
		}
	}
}
