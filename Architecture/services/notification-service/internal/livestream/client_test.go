package livestream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// The client speaks live-service-v2's internal reminder route exactly:
// path, paging parameters, internal key, and the {data:{…}} answer.
func TestReminderUserIDs_WireContract(t *testing.T) {
	stream := uuid.New()
	a, b := uuid.New(), uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		if want := "/v1/livestream/internal/streams/" + stream.String() + "/reminders"; r.URL.Path != want {
			t.Errorf("path = %s, want %s", r.URL.Path, want)
		}
		if got := r.URL.Query().Get("after"); got != "tok 1&x" {
			t.Errorf("after = %q (the token must round-trip escaped)", got)
		}
		if got := r.URL.Query().Get("limit"); got != "500" {
			t.Errorf("limit = %q", got)
		}
		if r.Header.Get("X-Internal-Service-Key") != "k" {
			t.Errorf("internal key missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"user_ids":["` + a.String() + `","not-a-uuid","` + b.String() + `"],"next_after":"tok-2","has_more":true}}`))
	}))
	defer srv.Close()

	page, err := New(srv.URL, "k").ReminderUserIDs(context.Background(), stream, "tok 1&x", 500)
	if err != nil {
		t.Fatalf("ReminderUserIDs: %v", err)
	}
	if len(page.IDs) != 2 || page.IDs[0] != a || page.IDs[1] != b {
		t.Fatalf("ids = %v", page.IDs)
	}
	if page.NextAfter != "tok-2" || !page.HasMore {
		t.Fatalf("paging = %q %v", page.NextAfter, page.HasMore)
	}
}

func TestReminderUserIDs_EmptyAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") != "" {
			t.Errorf("first page must send an empty after")
		}
		_, _ = w.Write([]byte(`{"data":{"user_ids":[],"next_after":"","has_more":false}}`))
	}))
	defer srv.Close()
	page, err := New(srv.URL, "").ReminderUserIDs(context.Background(), uuid.New(), "", 1)
	if err != nil || len(page.IDs) != 0 || page.HasMore || page.NextAfter != "" {
		t.Fatalf("page = %+v, err %v", page, err)
	}
}

// Anything but 200 is an error so the walk retries. An empty page would
// end the reminder phase for good and tell nobody.
func TestReminderUserIDs_Non200AndGarbageAreErrors(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"data":{"user_ids":[]}}`))
		}))
		page, err := New(srv.URL, "k").ReminderUserIDs(context.Background(), uuid.New(), "", 10)
		srv.Close()
		if err == nil {
			t.Fatalf("status %d returned page %+v, want an error", status, page)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>gateway</html>`))
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "k").ReminderUserIDs(context.Background(), uuid.New(), "", 10); err == nil {
		t.Fatal("an undecodable answer must be an error")
	}
}
