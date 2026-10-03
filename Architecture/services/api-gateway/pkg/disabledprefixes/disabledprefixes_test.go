package disabledprefixes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

var known = []string{"/v1/admin/flags", "/v1/flags", "/v1/memories", "/v1/reviewer", "/v1/food", "/v1/livestream", "/v1/posts"}

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    []string
		wantErr string
	}{
		{name: "empty closes nothing", raw: "", want: nil},
		{name: "whitespace and empty entries ignored", raw: " , ,  ", want: nil},
		{name: "launch list", raw: "/v1/memories,/v1/flags,/v1/admin/flags,/v1/reviewer",
			want: []string{"/v1/admin/flags", "/v1/flags", "/v1/memories", "/v1/reviewer"}},
		{name: "trimmed, trailing slash dropped, duplicates collapsed", raw: " /v1/memories/ , /v1/memories ,/v1/flags",
			want: []string{"/v1/flags", "/v1/memories"}},
		{name: "a path under a known prefix is allowed", raw: "/v1/food/admin", want: []string{"/v1/food/admin"}},
		{name: "unknown prefix is a boot error", raw: "/v1/memory", wantErr: `"/v1/memory" is not a route prefix`},
		{name: "near-prefix is not under a known one", raw: "/v1/flagship", wantErr: "not a route prefix"},
		{name: "probe paths cannot be closed", raw: "/healthz", wantErr: "not a route prefix"},
		{name: "relative entry rejected", raw: "v1/memories", wantErr: "must be an absolute path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.raw, known)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Parse(%q) err = %v, want containing %q", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.raw, err)
			}
			if !reflect.DeepEqual(got.Prefixes(), tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.raw, got.Prefixes(), tc.want)
			}
		})
	}
}

func TestServeAnswers404NotAvailable(t *testing.T) {
	set, err := Parse("/v1/memories,/v1/flags,/v1/admin/flags,/v1/reviewer", known)
	if err != nil {
		t.Fatal(err)
	}
	closed := []string{
		"/v1/memories", "/v1/memories/", "/v1/memories/2026/today",
		"/v1/flags", "/v1/flags/evaluate",
		"/v1/admin/flags", "/v1/admin/flags/x",
		"/v1/reviewer", "/v1/reviewer/assignments/next",
	}
	for _, path := range closed {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			res := httptest.NewRecorder()
			if !set.Serve(res, httptest.NewRequest(method, path, nil)) {
				t.Fatalf("%s %s: not served", method, path)
			}
			if res.Code != http.StatusNotFound {
				t.Fatalf("%s %s: status %d, want 404", method, path, res.Code)
			}
			if ct := res.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("%s %s: content type %q", method, path, ct)
			}
			var parsed struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &parsed); err != nil {
				t.Fatalf("%s %s: body is not JSON: %v (%q)", method, path, err, res.Body.String())
			}
			if parsed.Error.Code != Code {
				t.Fatalf("%s %s: code %q, want %q", method, path, parsed.Error.Code, Code)
			}
			if parsed.Error.Message == "" {
				t.Fatalf("%s %s: empty message", method, path)
			}
		}
	}
}

func TestServeLeavesOpenPrefixesAlone(t *testing.T) {
	set, err := Parse("/v1/memories,/v1/flags,/v1/reviewer", known)
	if err != nil {
		t.Fatal(err)
	}
	open := []string{
		"/v1/food", "/v1/food/orders", // deployed-but-gated: not in this list
		"/v1/flagship", "/v1/memoriesx", "/v1/reviewers",
		"/v1/admin/flags", // /v1/flags closed does not close its admin twin
		"/v1/livestream", "/v1/live", "/v1/posts", "/healthz", "/",
	}
	for _, path := range open {
		res := httptest.NewRecorder()
		if set.Serve(res, httptest.NewRequest(http.MethodGet, path, nil)) {
			t.Fatalf("%s: wrongly closed (%d %q)", path, res.Code, res.Body.String())
		}
		if res.Code != http.StatusOK || res.Body.Len() != 0 {
			t.Fatalf("%s: recorder touched on an open path", path)
		}
	}
}

func TestZeroSetClosesNothing(t *testing.T) {
	var set Set
	for _, path := range known {
		if set.Matches(path) {
			t.Fatalf("zero Set matched %s", path)
		}
	}
	if set.Prefixes() != nil {
		t.Fatalf("zero Set has prefixes %v", set.Prefixes())
	}
}
