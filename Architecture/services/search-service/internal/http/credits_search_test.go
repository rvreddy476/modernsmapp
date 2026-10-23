package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/search-service/internal/store/search"
)

// Video credits (2026-10-23): ?genre= / ?year= on GET /v1/search/posts are
// validated before OpenSearch is asked, reach the query only when set, work
// on any type, and the result rows keep the shape they had before credits.

func TestCreditSearch_MalformedFiltersAreRefusedBeforeOpenSearch(t *testing.T) {
	store, os := newTubeFakeOS(t)
	h := &Handler{store: store}
	before := os.searchCount()
	for _, target := range []string{
		"/v1/search/posts?q=asha&genre=drama%20thriller",
		"/v1/search/posts?q=asha&genre=*",
		"/v1/search/posts?q=asha&type=videos&year=1850",
		"/v1/search/posts?q=asha&year=soon",
	} {
		code, body := tubeRequest(t, h, target)
		if code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (%s)", target, code, body)
		}
		if !strings.Contains(string(body), "'genre'") && !strings.Contains(string(body), "'year'") {
			t.Fatalf("%s: body %s does not name the parameter", target, body)
		}
	}
	if os.searchCount() != before {
		t.Fatalf("%d quer(ies) reached OpenSearch for refused requests", os.searchCount()-before)
	}
}

func TestCreditSearch_FiltersReachTheQueryOnAnyType(t *testing.T) {
	store, os := newTubeFakeOS(t)
	h := &Handler{store: store}
	cases := []struct{ target, mustHave string }{
		{"/v1/search/posts?q=asha&genre=Drama", `{"term":{"genres":"drama"}}`},
		{"/v1/search/posts?q=asha&type=videos&year=2024", `{"term":{"release_year":2024}}`},
		{"/v1/search/posts?q=asha&type=videos&sort=views&genre=sci-fi", `{"term":{"genres":"sci-fi"}}`},
	}
	for _, tc := range cases {
		code, body := tubeRequest(t, h, tc.target)
		if code != http.StatusOK {
			t.Fatalf("%s: status = %d: %s", tc.target, code, body)
		}
		sent, _ := json.Marshal(os.body(search.IndexPosts))
		if !strings.Contains(string(sent), tc.mustHave) {
			t.Fatalf("%s: query lacks %s: %s", tc.target, tc.mustHave, sent)
		}
		if !strings.Contains(string(sent), `"credit_names^2"`) {
			t.Fatalf("%s: query does not match credited names: %s", tc.target, sent)
		}
	}
	// Unset: no credit filter at all.
	if code, _ := tubeRequest(t, h, "/v1/search/posts?q=asha"); code != http.StatusOK {
		t.Fatalf("plain search status = %d", code)
	}
	plain, _ := json.Marshal(os.body(search.IndexPosts))
	if strings.Contains(string(plain), `"genres":`) || strings.Contains(string(plain), "release_year") {
		t.Fatalf("plain search carries a credit filter: %s", plain)
	}
}

func TestCreditSearch_ResultRowsDoNotCarryCreditFields(t *testing.T) {
	store, os := newTubeFakeOS(t)
	os.respond(search.IndexPosts, hits(`{
		"post_id":"6b1f9a2e-0c6d-4a5e-9f3a-1b2c3d4e5f60","author_id":"a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"text":"A film","visibility":"public","review_status":"approved","search_rev":2,
		"created_at":"2026-10-20T10:00:00Z","content_type":"long_video","title":"Monsoon",
		"credit_names":["Asha Rao"],"credit_roles":["Director"],"genres":["drama"],
		"release_year":2024,"production_house":"Blue Hour Films"
	}`))
	h := &Handler{store: store}
	code, body := tubeRequest(t, h, "/v1/search/posts?q=Asha%20Rao")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	if !strings.Contains(string(body), "Monsoon") {
		t.Fatalf("the credited video is not in the response: %s", body)
	}
	for _, field := range []string{"credit_names", "credit_roles", "genres", "release_year", "production_house"} {
		if strings.Contains(string(body), `"`+field+`"`) {
			t.Fatalf("result row carries %s; the search row shape must not change: %s", field, body)
		}
	}
}
