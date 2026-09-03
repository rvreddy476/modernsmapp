package privacyclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsPrivate_ReadsAccountVisibilityWithInternalKey(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("X-Internal-Service-Key") != "k" {
			t.Errorf("missing internal key")
		}
		switch r.URL.Path {
		case "/v1/users/priv/settings":
			_, _ = w.Write([]byte(`{"data":{"account_visibility":"private"}}`))
		case "/v1/users/pub/settings":
			_, _ = w.Write([]byte(`{"data":{"account_visibility":"public"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "k")

	if v, err := c.IsPrivate(context.Background(), "priv"); err != nil || !v {
		t.Fatalf("priv = %v, %v", v, err)
	}
	if v, err := c.IsPrivate(context.Background(), "pub"); err != nil || v {
		t.Fatalf("pub = %v, %v", v, err)
	}
	// A user the settings service does not know yet is public by default.
	if v, err := c.IsPrivate(context.Background(), "ghost"); err != nil || v {
		t.Fatalf("unknown user = %v, %v; want public, nil", v, err)
	}
}

func TestIsPrivate_CachesForSixtySecondsAndInvalidates(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"data":{"account_visibility":"private"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	now := time.Now()
	c.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if _, err := c.IsPrivate(context.Background(), "u"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (cached)", calls)
	}
	c.Invalidate("u")
	_, _ = c.IsPrivate(context.Background(), "u")
	if calls != 2 {
		t.Fatalf("calls = %d after Invalidate, want 2", calls)
	}
	now = now.Add(cacheTTL + time.Second)
	_, _ = c.IsPrivate(context.Background(), "u")
	if calls != 3 {
		t.Fatalf("calls = %d after TTL, want 3", calls)
	}
	c.Prime("primed", true)
	if v, _ := c.IsPrivate(context.Background(), "primed"); !v || calls != 3 {
		t.Fatalf("Prime not honoured (v=%v calls=%d)", v, calls)
	}
}

// A failure is an error, never "public": the indexer retries instead of
// writing a private account's post as searchable.
func TestIsPrivate_FailuresAreErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "").IsPrivate(context.Background(), "u"); err == nil {
		t.Fatal("502 returned no error")
	}
	if _, err := New("", "").IsPrivate(context.Background(), "u"); err == nil {
		t.Fatal("unconfigured client returned no error")
	}
}
