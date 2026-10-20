package places

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeGoogle(t *testing.T, status int, body string, seen *autocompleteRequest, headers *http.Header) *Searcher {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if seen != nil {
			_ = json.Unmarshal(raw, seen)
		}
		if headers != nil {
			*headers = r.Header.Clone()
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	s := New("test-key")
	s.endpoint = srv.URL
	return s
}

const charminar = `{"suggestions":[
 {"placePrediction":{"placeId":"ChIJm5MaboqXyzsR-xPxquRpWss","text":{"text":"Charminar, Hyderabad"},
  "structuredFormat":{"mainText":{"text":"Charminar"},"secondaryText":{"text":"Hyderabad, Telangana, India"}}}},
 {"queryPrediction":{"text":{"text":"charminar food"}}},
 {"placePrediction":{"placeId":"bad id with spaces","text":{"text":"Nope"}}}
]}`

func TestAutocompleteMapsPlacesAndSkipsTheRest(t *testing.T) {
	var seen autocompleteRequest
	var hdr http.Header
	s := fakeGoogle(t, http.StatusOK, charminar, &seen, &hdr)
	got, err := s.Autocomplete(context.Background(), "  Charminar  ", "4f9c1c2e-0b6a-4f0e-9c55-6a1b2c3d4e5f", "en")
	if err != nil {
		t.Fatal(err)
	}
	want := []Place{{PlaceID: "ChIJm5MaboqXyzsR-xPxquRpWss", Name: "Charminar", Secondary: "Hyderabad, Telangana, India"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %+v", got)
	}
	if seen.Input != "Charminar" || seen.SessionToken == "" || seen.LanguageCode != "en" {
		t.Fatalf("request %+v", seen)
	}
	if hdr.Get("X-Goog-Api-Key") != "test-key" || !strings.Contains(hdr.Get("X-Goog-FieldMask"), "placeId") {
		t.Fatal("key or field mask missing")
	}
}

func TestAutocompleteWithoutKeyIsDisabled(t *testing.T) {
	if _, err := New("").Autocomplete(context.Background(), "Charminar", "", ""); !errors.Is(err, ErrDisabled) {
		t.Fatalf("got %v", err)
	}
}

func TestAutocompleteRefusesBadQueriesBeforeCallingGoogle(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	s := New("k")
	s.endpoint = srv.URL
	for _, q := range []string{"", " a ", strings.Repeat("x", MaxQueryRunes+1)} {
		if _, err := s.Autocomplete(context.Background(), q, "", ""); !errors.Is(err, ErrBadQuery) {
			t.Fatalf("%q: %v", q, err)
		}
	}
	if called {
		t.Fatal("a bad query must not cost a Google request")
	}
}

func TestAutocompleteUpstreamFailureIsUnavailable(t *testing.T) {
	s := fakeGoogle(t, http.StatusForbidden, `{"error":{"status":"PERMISSION_DENIED"}}`, nil, nil)
	if _, err := s.Autocomplete(context.Background(), "Charminar", "", ""); !errors.Is(err, ErrUpstream) {
		t.Fatalf("got %v", err)
	}
}

func TestSessionTokenAndLanguageAreSanitised(t *testing.T) {
	if sessionToken("abc def") != "" || sessionToken(strings.Repeat("a", 37)) != "" || sessionToken("ok_token-1") != "ok_token-1" {
		t.Fatal("session token")
	}
	if languageCode("en-IN") != "en-IN" || languageCode("e") != "" || languageCode("en;drop") != "" {
		t.Fatal("language")
	}
}
