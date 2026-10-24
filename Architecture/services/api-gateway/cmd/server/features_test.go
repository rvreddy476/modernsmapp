package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/api-gateway/pkg/disabledprefixes"
)

// productFlag is each product's own gate flag. broadcast_channels has none:
// only GATEWAY_DISABLED_PREFIXES closes it.
var productFlag = map[string]string{
	"broadcast_channels": "",
	"communities":        "COMMUNITIES_PUBLIC_ENABLED",
	"groups":             "GROUPS_PUBLIC_ENABLED",
	"qa":                 "QA_PUBLIC_ENABLED",
	"pages":              "PAGES_PUBLIC_ENABLED",
	"memories":           "MEMORIES_PUBLIC_ENABLED",
	"food":               "FOOD_PUBLIC_ENABLED",
	"rider":              "RIDER_PUBLIC_ENABLED",
	"dating":             "DATING_PUBLIC_ENABLED",
	"doorstep":           "DOORSTEP_PUBLIC_ENABLED",
}

// upstreamRoutes points every route at one upstream that answers 200 with a
// marker header, so a test can tell "reached the service" from an edge answer.
func upstreamRoutes(t *testing.T) []route {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Reached-Upstream", "1")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var routes []route
	for _, rd := range routeDefinitions() {
		routes = append(routes, newRoute(rd.prefix, target, ""))
	}
	return routes
}

type featuresResponse struct {
	Data struct {
		Products map[string]struct {
			Enabled *bool `json:"enabled"`
		} `json:"products"`
	} `json:"data"`
}

func getFeatures(t *testing.T, h http.Handler, mutate func(*http.Request)) (*httptest.ResponseRecorder, map[string]bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, featuresPath, nil)
	if mutate != nil {
		mutate(req)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %q", featuresPath, res.Code, res.Body.String())
	}
	var parsed featuresResponse
	if err := json.Unmarshal(res.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("GET %s: body is not JSON: %v (%q)", featuresPath, err, res.Body.String())
	}
	out := map[string]bool{}
	for key, p := range parsed.Data.Products {
		if p.Enabled == nil {
			t.Fatalf("product %s has no enabled field: %q", key, res.Body.String())
		}
		out[key] = *p.Enabled
	}
	return res, out
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The response carries exactly the ten contract keys, the cache header, and
// with nothing configured only broadcast channels are open (they have no
// gate of their own; the values files close them with the disabled list).
func TestFeaturesShapeHeadersAndDefaults(t *testing.T) {
	core := newCoreHandler(upstreamRoutes(t), nil, false, productsFrom(t, nil), disabledprefixes.Set{})
	res, got := getFeatures(t, core, nil)

	if cc := res.Header().Get("Cache-Control"); cc != "public, max-age=60" {
		t.Errorf("Cache-Control = %q, want %q", cc, "public, max-age=60")
	}
	if ct := res.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if res.Header().Get("X-Reached-Upstream") != "" {
		t.Error("/v1/features was proxied; the gateway must answer it itself")
	}
	wantKeys := make([]string, 0, len(productFlag))
	for k := range productFlag {
		wantKeys = append(wantKeys, k)
	}
	sort.Strings(wantKeys)
	if gotKeys := sortedKeys(got); strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("product keys = %v, want %v", gotKeys, wantKeys)
	}
	for key, enabled := range got {
		want := key == "broadcast_channels"
		if enabled != want {
			t.Errorf("%s enabled = %v with nothing configured, want %v", key, enabled, want)
		}
	}
}

// For every combination of one product flag opened (and all of them) with
// one prefix disabled (and none, and the production list), each product's
// enabled flag equals what an anonymous request to its prefix actually gets
// from the same core handler: enabled exactly when it reaches the upstream.
// It also equals the contract rule: flag open AND prefix not disabled.
func TestFeaturesAgreeWithTheGates(t *testing.T) {
	routes := upstreamRoutes(t)
	known := prefixesOf(routeDefinitions())

	envs := []map[string]string{nil}
	all := map[string]string{}
	for _, flag := range productFlag {
		if flag == "" {
			continue
		}
		envs = append(envs, map[string]string{flag: "true"})
		all[flag] = "true"
	}
	envs = append(envs, all)

	disabledLists := []string{"", launchDisabledPrefixes,
		"/v1/memories,/v1/flags,/v1/admin/flags,/v1/reviewer,/v1/wallet,/v1/billpay,/v1/food,/v1/qa,/v1/pages,/v1/broadcast-channels"}
	for _, prefix := range featureProductPrefixes {
		disabledLists = append(disabledLists, prefix)
	}

	for _, env := range envs {
		products := productsFrom(t, env)
		for _, raw := range disabledLists {
			disabled, err := disabledprefixes.Parse(raw, known)
			if err != nil {
				t.Fatalf("parse %q: %v", raw, err)
			}
			core := newCoreHandler(routes, nil, false, products, disabled)
			_, got := getFeatures(t, core, nil)
			for key, prefix := range featureProductPrefixes {
				for _, path := range []string{prefix, prefix + "/anything"} {
					res := httptest.NewRecorder()
					core.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
					served := res.Header().Get("X-Reached-Upstream") == "1"
					if got[key] != served {
						t.Errorf("env=%v disabled=%q: %s enabled=%v but GET %s reached upstream=%v (status %d)",
							env, raw, key, got[key], path, served, res.Code)
					}
				}
				flag := productFlag[key]
				want := (flag == "" || env[flag] == "true") && !disabled.Matches(prefix)
				if got[key] != want {
					t.Errorf("env=%v disabled=%q: %s enabled=%v, want %v", env, raw, key, got[key], want)
				}
			}
		}
	}
}

// A pilot allowlist does not open the public answer: it is cached and
// public, so it is the anonymous view. A partner flag does not either.
func TestFeaturesIgnorePilotsAndPartnerFlag(t *testing.T) {
	products := productsFrom(t, map[string]string{
		"DATING_PILOT_USER_IDS":    pilotCallA,
		"DOORSTEP_PILOT_USER_IDS":  pilotCallB,
		"DOORSTEP_PARTNER_ENABLED": "true",
	})
	core := newCoreHandler(upstreamRoutes(t), nil, false, products, disabledprefixes.Set{})
	_, got := getFeatures(t, core, nil)
	if got["dating"] || got["doorstep"] {
		t.Fatalf("dating=%v doorstep=%v with only pilots/partner set, want both false", got["dating"], got["doorstep"])
	}
}

// A product whose prefix is not in the route table is never enabled, even
// with every gate open: nothing would serve it.
func TestFeaturesNeedARoute(t *testing.T) {
	var routes []route
	for _, rt := range upstreamRoutes(t) {
		if rt.prefix != "/v1/groups" {
			routes = append(routes, rt)
		}
	}
	core := newCoreHandler(routes, nil, false, productsFrom(t, map[string]string{"GROUPS_PUBLIC_ENABLED": "true"}), disabledprefixes.Set{})
	_, got := getFeatures(t, core, nil)
	if got["groups"] {
		t.Fatal("groups enabled with no /v1/groups route")
	}
}

// Through the real edge chain: no token is needed, and a bad bearer or a
// stale access_token cookie does not turn the answer into a 401. Only GET
// and HEAD are answered; a path under it is not this route.
func TestFeaturesNeedNoAuthentication(t *testing.T) {
	core := newCoreHandler(upstreamRoutes(t), nil, false, productsFrom(t, nil), disabledprefixes.Set{})
	passThrough := func(next http.Handler) http.Handler { return next }
	gw := edgeChain(jwtKeySet{activeKID: "v1", activeSecret: "secret"}, devTestPolicy(), passThrough, nil, core)

	getFeatures(t, gw, nil)
	getFeatures(t, gw, func(r *http.Request) { r.Header.Set("Authorization", "Bearer not-a-token") })
	getFeatures(t, gw, func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "access_token", Value: "expired.or.garbage"}) })

	// The bypass is this exact path only: a bad token elsewhere still 401s.
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/posts", nil)
	req.Header.Set("Authorization", "Bearer not-a-token")
	gw.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("/v1/posts with a bad token: %d, want 401", res.Code)
	}
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, featuresPath+"/x", nil)
	req.Header.Set("Authorization", "Bearer not-a-token")
	gw.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("%s/x with a bad token: %d, want 401 (the bypass must be exact)", featuresPath, res.Code)
	}

	res = httptest.NewRecorder()
	gw.ServeHTTP(res, httptest.NewRequest(http.MethodHead, featuresPath, nil))
	if res.Code != http.StatusOK || res.Body.Len() != 0 || res.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("HEAD: %d body=%q cache=%q", res.Code, res.Body.String(), res.Header().Get("Cache-Control"))
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		res = httptest.NewRecorder()
		gw.ServeHTTP(res, httptest.NewRequest(method, featuresPath, nil))
		if res.Code != http.StatusMethodNotAllowed || res.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("%s: %d allow=%q, want 405", method, res.Code, res.Header().Get("Allow"))
		}
		if res.Header().Get("X-Reached-Upstream") != "" {
			t.Fatalf("%s %s was proxied", method, featuresPath)
		}
	}
}
