package graph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// The client speaks graph-service's cursor follower listing exactly: path,
// paginate=cursor, the token round-tripped, internal key, no viewer, and
// the {data:{items,next_cursor}} answer.
func TestFollowerIDs_WireContract(t *testing.T) {
	creator := uuid.New()
	a, b := uuid.New(), uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		if want := "/v1/graph/followers/" + creator.String(); r.URL.Path != want {
			t.Errorf("path = %s, want %s", r.URL.Path, want)
		}
		q := r.URL.Query()
		if q.Get("paginate") != "cursor" {
			t.Errorf("paginate = %q (the offset variant is refused past 10,000)", q.Get("paginate"))
		}
		if got := q.Get("cursor"); got != "1759400000000000:"+a.String() {
			t.Errorf("cursor = %q (the token must round-trip)", got)
		}
		if got := q.Get("limit"); got != "100" {
			t.Errorf("limit = %q", got)
		}
		if q.Has("offset") {
			t.Errorf("offset sent on the cursor route")
		}
		if r.Header.Get("X-Internal-Service-Key") != "k" {
			t.Errorf("internal key missing")
		}
		if r.Header.Get("X-User-Id") != "" {
			t.Errorf("a viewer was named; the listing must be the internal view")
		}
		_, _ = w.Write([]byte(`{"data":{"items":["` + a.String() + `","not-a-uuid","` + b.String() + `"],"next_cursor":"tok-2"}}`))
	}))
	defer srv.Close()

	page, err := New(srv.URL, "k").FollowerIDs(context.Background(), creator, "1759400000000000:"+a.String(), 100)
	if err != nil {
		t.Fatalf("FollowerIDs: %v", err)
	}
	if len(page.IDs) != 2 || page.IDs[0] != a || page.IDs[1] != b {
		t.Fatalf("ids = %v", page.IDs)
	}
	if page.NextCursor != "tok-2" {
		t.Fatalf("next cursor = %q", page.NextCursor)
	}
}

// The first page sends no cursor at all, and a limit graph-service would
// answer with a page of 20 (anything above 100, or none) is clamped to
// its maximum instead.
func TestFollowerIDs_FirstPageAndLimitClamp(t *testing.T) {
	for _, c := range []struct {
		ask  int
		want string
	}{{500, "100"}, {0, "100"}, {-3, "100"}, {100, "100"}, {1, "1"}} {
		var got string
		var hadCursor bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.Query().Get("limit")
			hadCursor = r.URL.Query().Has("cursor")
			_, _ = w.Write([]byte(`{"data":{"items":[],"next_cursor":""}}`))
		}))
		page, err := New(srv.URL, "k").FollowerIDs(context.Background(), uuid.New(), "", c.ask)
		srv.Close()
		if err != nil {
			t.Fatalf("limit %d: %v", c.ask, err)
		}
		if got != c.want {
			t.Fatalf("asked %d, sent limit=%s, want %s", c.ask, got, c.want)
		}
		if hadCursor {
			t.Fatalf("first page sent a cursor parameter")
		}
		if len(page.IDs) != 0 || page.NextCursor != "" {
			t.Fatalf("empty page = %+v", page)
		}
	}
}

// "Could not ask" is an error, never an empty page: an empty page ends
// the follower walk for good.
func TestFollowerIDs_FailuresAreErrors(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"500": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		"404": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
		"400": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) },
		// A refusal that still carries a well-formed empty listing.
		"404 with a listing body": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"data":{"items":[],"next_cursor":""}}`))
		},
		"not json":  func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`<html>`)) },
		"bare list": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) },
		"no items":  func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":{"next_cursor":"x"}}`)) },
		"no data":   func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"error":{"code":"X"}}`)) },
	} {
		srv := httptest.NewServer(h)
		page, err := New(srv.URL, "k").FollowerIDs(context.Background(), uuid.New(), "", 100)
		srv.Close()
		if err == nil {
			t.Fatalf("%s: got page %+v, want an error", name, page)
		}
	}

	var nilClient *Client
	if _, err := nilClient.FollowerIDs(context.Background(), uuid.New(), "", 100); err == nil {
		t.Fatal("a nil client must be an error, not an empty page")
	}
	if _, err := New("", "k").FollowerIDs(context.Background(), uuid.New(), "", 100); err == nil {
		t.Fatal("an unconfigured client must be an error, not an empty page")
	}
}
