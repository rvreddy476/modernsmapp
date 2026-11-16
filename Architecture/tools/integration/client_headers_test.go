//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// WithHeader must reach the wire: the social flow's Idempotency-Key was once
// set on the client and never sent. This needs no stack.
func TestHTTPClientWithHeaderIsSent(t *testing.T) {
	var got, other string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Idempotency-Key")
		other = r.Header.Get("X-Second")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	base := NewHTTPClient(srv.URL, uuid.New())
	c := base.WithHeader("Idempotency-Key", "k-1").WithHeader("X-Second", "2")
	if _, err := c.Do(context.Background(), "POST", "/x", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if got != "k-1" || other != "2" {
		t.Fatalf("headers on the wire: Idempotency-Key=%q X-Second=%q", got, other)
	}
	if len(base.Headers) != 0 {
		t.Fatalf("WithHeader changed the client it was called on: %v", base.Headers)
	}
}
