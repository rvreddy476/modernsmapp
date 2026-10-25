package mediaclient

import (
	"context"
	"encoding/json"
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

type claimCapture struct {
	path, key string
	body      map[string]any
}

func serveClaims(t *testing.T, status int, body string, got *claimCapture) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got != nil {
			got.path, got.key = r.URL.Path, r.Header.Get(internalKeyHeader)
			_ = json.NewDecoder(r.Body).Decode(&got.body)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "k-test")
}

func TestClaimPostsTheHolderWithTheKey(t *testing.T) {
	var got claimCapture
	c := serveClaims(t, http.StatusOK, `{"data":{"claimed":1}}`, &got)
	ref, id := uuid.New(), uuid.New()
	if err := c.Claim(context.Background(), "channel_update", ref, []uuid.UUID{id}); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got.path != "/v1/media/internal/claims" || got.key != "k-test" {
		t.Fatalf("posted to %q with key %q", got.path, got.key)
	}
	if got.body["claimant"] != "channel-service" || got.body["kind"] != "channel_update" || got.body["ref"] != ref.String() {
		t.Fatalf("body %v", got.body)
	}
	ids, _ := got.body["media_ids"].([]any)
	if len(ids) != 1 || ids[0] != id.String() {
		t.Fatalf("media_ids %v", got.body["media_ids"])
	}
}

func TestReleaseAllOmitsTheIDs(t *testing.T) {
	var got claimCapture
	c := serveClaims(t, http.StatusOK, `{"data":{"released":0}}`, &got)
	if err := c.Release(context.Background(), "channel_avatar", uuid.New(), nil); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got.path != "/v1/media/internal/claims/release" {
		t.Fatalf("posted to %q", got.path)
	}
	if _, ok := got.body["media_ids"]; ok {
		t.Fatalf("a release-all must not send media_ids: %v", got.body)
	}
}

// Only media-service's own MEDIA_NOT_FOUND is a verdict. A bare 404 is a
// media-service without the claim route (deployed out of order): treating it
// as "no such media" would refuse every attach with a wrong answer, and
// treating it as success would attach with no claim at all.
func TestClaimAnswers(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"asset missing":     {http.StatusNotFound, `{"error":{"code":"MEDIA_NOT_FOUND","message":"media does not exist"}}`, ErrNotFound},
		"route missing":     {http.StatusNotFound, `404 page not found`, ErrUnavailable},
		"other 404 code":    {http.StatusNotFound, `{"error":{"code":"NOT_FOUND"}}`, ErrUnavailable},
		"bad claim":         {http.StatusBadRequest, `{"error":{"code":"CLAIM_INVALID"}}`, ErrUnavailable},
		"server error":      {http.StatusInternalServerError, `{}`, ErrUnavailable},
		"unauthenticated":   {http.StatusUnauthorized, `{}`, ErrUnavailable},
		"created is not ok": {http.StatusCreated, `{}`, ErrUnavailable},
	} {
		c := serveClaims(t, tc.status, tc.body, nil)
		if err := c.Claim(context.Background(), "channel_update", uuid.New(), []uuid.UUID{uuid.New()}); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
	var nilClient *Client
	if err := nilClient.Claim(context.Background(), "channel_update", uuid.New(), []uuid.UUID{uuid.New()}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client claim = %v", err)
	}
	if err := nilClient.Release(context.Background(), "channel_update", uuid.New(), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client release = %v", err)
	}
}

// "Keep nothing" must never go out as a release-except: media-service would
// refuse it, and a client that sent it would mean "release everything".
func TestReleaseExceptNeedsAKeepSet(t *testing.T) {
	var got claimCapture
	c := serveClaims(t, http.StatusOK, `{}`, &got)
	if err := c.ReleaseExcept(context.Background(), KindChannelUpdate, uuid.New(), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("empty keep = %v", err)
	}
	if got.path != "" {
		t.Fatal("an empty keep set was sent")
	}
	keep := uuid.New()
	if err := c.ReleaseExcept(context.Background(), KindChannelUpdate, uuid.New(), []uuid.UUID{keep}); err != nil {
		t.Fatal(err)
	}
	ids, _ := got.body["except_media_ids"].([]any)
	if got.path != "/v1/media/internal/claims/release" || len(ids) != 1 || ids[0] != keep.String() {
		t.Fatalf("sent %q %v", got.path, got.body)
	}
}
