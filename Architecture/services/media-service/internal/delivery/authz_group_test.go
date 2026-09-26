package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

/*
The group authority: asked at group-service's route, with the viewer and
asset in the body and the internal key in the header; its yes is a yes and
its 403 is a resolved denial (not an outage). The path is the one
group-service registers in internal/http/handler.go — the two must agree
or every group attachment is denied again, silently.
*/
func TestGroupAuthorizerAsksGroupService(t *testing.T) {
	var gotPath, gotKey string
	var gotBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("X-Internal-Service-Key")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allowed":true,"decision":"allowed","reason":"group_post"}`))
	}))
	defer server.Close()

	authorizer := NewHTTPGroupAuthorizer(server.URL, "internal", server.Client())
	if err := authorizer.Authorize(context.Background(), "viewer-1", "media-1"); err != nil {
		t.Fatalf("group-service said yes but the authorizer denied: %v", err)
	}
	if gotPath != "/v1/internal/groups/media-access" {
		t.Fatalf("asked %s, want /v1/internal/groups/media-access", gotPath)
	}
	if gotKey != "internal" {
		t.Fatal("internal service key not sent — group-service refuses unkeyed calls")
	}
	if gotBody["viewer_id"] != "viewer-1" || gotBody["media_id"] != "media-1" {
		t.Fatalf("body %v", gotBody)
	}
}

func TestGroupAuthorizerTreats403AsResolvedDenial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"allowed":false,"decision":"denied","reason":"not_a_member"}`))
	}))
	defer server.Close()

	authorizer := NewHTTPGroupAuthorizer(server.URL, "internal", server.Client())
	err := authorizer.Authorize(context.Background(), "viewer-1", "media-1")
	if !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("got %v, want ErrDeliveryDenied (a resolved no, not an outage to retry)", err)
	}
}

// An anonymous viewer never reaches group-service: group content has no
// signed-out audience, unlike public profile photos.
func TestGroupAuthorizerRefusesAnonymousViewer(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()
	authorizer := NewHTTPGroupAuthorizer(server.URL, "internal", server.Client())
	if err := authorizer.Authorize(context.Background(), "", "media-1"); !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("got %v, want ErrDeliveryDenied", err)
	}
	if called {
		t.Fatal("group-service was asked about an anonymous viewer")
	}
}
