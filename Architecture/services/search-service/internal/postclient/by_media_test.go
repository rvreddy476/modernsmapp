package postclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPostIDsByMedia(t *testing.T) {
	var gotPath, gotKey string
	status, body := http.StatusOK, `{"media_id":"m1","post_ids":["p1","p2"]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("X-Internal-Service-Key")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := New(srv.URL+"/", "k")
	ctx := context.Background()

	ids, err := c.PostIDsByMedia(ctx, "m1")
	if err != nil || len(ids) != 2 || ids[0] != "p1" || ids[1] != "p2" {
		t.Fatalf("got %v, %v", ids, err)
	}
	if gotPath != "/v1/internal/posts/by-media/m1" || gotKey != "k" {
		t.Fatalf("called %s with key %q", gotPath, gotKey)
	}

	body = `{"media_id":"m1","post_ids":[]}`
	if ids, err := c.PostIDsByMedia(ctx, "m1"); err != nil || len(ids) != 0 {
		t.Fatalf("no posts: %v, %v", ids, err)
	}

	// Anything but a clear answer is an error, never "no posts".
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"503":           {http.StatusServiceUnavailable, `{"error":"unresolved"}`},
		"401":           {http.StatusUnauthorized, `{}`},
		"no post_ids":   {http.StatusOK, `{"media_id":"m1"}`},
		"not json":      {http.StatusOK, `<html>`},
		"null post_ids": {http.StatusOK, `{"post_ids":null}`},
	} {
		status, body = tc.status, tc.body
		if _, err := c.PostIDsByMedia(ctx, "m1"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}

	if _, err := New("", "k").PostIDsByMedia(ctx, "m1"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unconfigured: %v", err)
	}
}
