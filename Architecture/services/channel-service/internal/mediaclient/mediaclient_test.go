package mediaclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func serve(t *testing.T, status int, body string, gotKey *string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotKey != nil {
			*gotKey = r.Header.Get(internalKeyHeader)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "k-test")
}

func TestGetDecodesTheAssetAndPresentsTheKey(t *testing.T) {
	id, owner := uuid.New(), uuid.New()
	var key string
	c := serve(t, http.StatusOK, fmt.Sprintf(
		`{"data":{"id":%q,"uploader_id":%q,"file_type":"image","processing_status":"ready","moderation_status":"passed"}}`,
		id, owner), &key)
	a, err := c.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if a.UploaderID != owner || a.ProcessingStatus != "ready" || a.ModerationStatus != "passed" {
		t.Fatalf("decoded %+v", a)
	}
	if key != "k-test" {
		t.Fatal("the internal service key was not presented; media-service would answer as to an anonymous viewer")
	}
}

func TestGet404IsNotFound(t *testing.T) {
	c := serve(t, http.StatusNotFound, `{}`, nil)
	if _, err := c.Get(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 = %v, want ErrNotFound", err)
	}
}

// Every answer that is not a verdict must be ErrUnavailable, never a usable
// asset and never ErrNotFound.
func TestGetFailsClosed(t *testing.T) {
	id := uuid.New()
	cases := map[string]struct {
		status int
		body   string
	}{
		"500":             {http.StatusInternalServerError, `{}`},
		"500 with a body": {http.StatusInternalServerError, fmt.Sprintf(`{"data":{"id":%q,"uploader_id":%q,"processing_status":"ready","moderation_status":"passed"}}`, id, uuid.New())},
		"403":             {http.StatusForbidden, `{}`},
		"not json":        {http.StatusOK, `<html>proxy</html>`},
		"no asset":        {http.StatusOK, `{"data":{}}`},
		"different asset": {http.StatusOK, fmt.Sprintf(`{"data":{"id":%q,"uploader_id":%q,"processing_status":"ready","moderation_status":"passed"}}`, uuid.New(), uuid.New())},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := serve(t, tc.status, tc.body, nil)
			a, err := c.Get(context.Background(), id)
			if !errors.Is(err, ErrUnavailable) || a != nil {
				t.Fatalf("got (%v, %v), want ErrUnavailable", a, err)
			}
		})
	}
}

func TestUnreachableAndUnconfiguredAreUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if _, err := New(url, "k").Get(context.Background(), uuid.New()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unreachable = %v, want ErrUnavailable", err)
	}
	var nilClient *Client
	if _, err := nilClient.Get(context.Background(), uuid.New()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client = %v, want ErrUnavailable", err)
	}
	if New("", "k") != nil || New("http://media", "") != nil {
		t.Fatal("a blank URL or a blank key must yield no client")
	}
}
