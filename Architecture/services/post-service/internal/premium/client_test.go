package premium

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeAuth answers GET /v1/auth/internal/roles/:id with roles(id), or with
// status when it is not 200. hits counts requests.
func fakeAuth(t *testing.T, status int, roles func(id string) string, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if r.Header.Get("X-Internal-Service-Key") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		const prefix = "/v1/auth/internal/roles/"
		if len(r.URL.Path) <= len(prefix) || r.URL.Path[:len(prefix)] != prefix {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		id := r.URL.Path[len(prefix):]
		_, _ = fmt.Fprintf(w, `{"data":{"user_id":%q,"roles":%s}}`, id, roles(id))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestIsPremium_ReadsTheRole(t *testing.T) {
	var hits int32
	srv := fakeAuth(t, 200, func(string) string { return `["seller","premium"]` }, &hits)
	ok, err := New(srv.URL, "k").IsPremium(context.Background(), uuid.New())
	if err != nil || !ok {
		t.Fatalf("premium role: got %v %v, want true", ok, err)
	}
	srv2 := fakeAuth(t, 200, func(string) string { return `["seller","premium_lapsed"]` }, &hits)
	ok, err = New(srv2.URL, "k").IsPremium(context.Background(), uuid.New())
	if err != nil || ok {
		t.Fatalf("no premium role: got %v %v, want false", ok, err)
	}
}

// Fail closed: every way of not knowing is an error, never a "no" that the
// caller could treat as an answer and never a "yes".
func TestIsPremium_FailsClosed(t *testing.T) {
	var hits int32
	cases := map[string]*Client{
		"auth 500":       New(fakeAuth(t, 500, nil, &hits).URL, "k"),
		"auth 401 (key)": New(fakeAuth(t, 200, func(string) string { return `["premium"]` }, &hits).URL, "wrong"),
		"no roles field": New(fakeAuth(t, 200, func(string) string { return `null` }, &hits).URL, "k"),
		"unreachable":    New("http://127.0.0.1:1", "k"),
		"nil client":     nil,
		// A non-200 that still carries a well-formed premium body is not an answer.
		"503 with a premium body": New(httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, `{"data":{"user_id":%q,"roles":["premium"]}}`, r.URL.Path[len("/v1/auth/internal/roles/"):])
		})).URL, "k"),
		"other user body": New(httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"data":{"user_id":%q,"roles":["premium"]}}`, uuid.NewString())
		})).URL, "k"),
	}
	for name, c := range cases {
		ok, err := c.IsPremium(context.Background(), uuid.New())
		if err == nil || ok || !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: got %v %v, want ErrUnavailable", name, ok, err)
		}
	}
	if New("  ", "k") != nil {
		t.Fatal("an empty base URL must give no client")
	}
}

// 60 s per user: a second read inside the window does not call auth; one
// after it does; another user is never served from the first one's entry;
// a failure is not cached.
func TestIsPremium_CachesPerUserFor60s(t *testing.T) {
	var hits int32
	srv := fakeAuth(t, 200, func(string) string { return `["premium"]` }, &hits)
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	c := New(srv.URL, "k").WithClock(func() time.Time { return now })
	a, b := uuid.New(), uuid.New()
	for i := 0; i < 3; i++ {
		if ok, err := c.IsPremium(context.Background(), a); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	if hits != 1 {
		t.Fatalf("auth called %d times for one user in the window, want 1", hits)
	}
	_, _ = c.IsPremium(context.Background(), b)
	if hits != 2 {
		t.Fatalf("a second user must be read, not served from the first: %d", hits)
	}
	now = now.Add(CacheTTL - time.Second)
	_, _ = c.IsPremium(context.Background(), a)
	if hits != 2 {
		t.Fatalf("read before the TTL: %d calls", hits)
	}
	now = now.Add(2 * time.Second)
	_, _ = c.IsPremium(context.Background(), a)
	if hits != 3 {
		t.Fatalf("read after the TTL must call auth again: %d calls", hits)
	}
	if CacheTTL != 60*time.Second {
		t.Fatalf("CacheTTL = %v, contract says 60 s", CacheTTL)
	}

	var failHits int32
	down := New(fakeAuth(t, 503, nil, &failHits).URL, "k")
	_, _ = down.IsPremium(context.Background(), a)
	_, _ = down.IsPremium(context.Background(), a)
	if failHits != 2 {
		t.Fatalf("a failure was cached: %d calls", failHits)
	}
}
