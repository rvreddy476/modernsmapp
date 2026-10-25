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

type seen struct {
	key, path, method string
	body              map[string]any
}

func serve(t *testing.T, status int, body string, got *seen) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got != nil {
			got.key, got.path, got.method = r.Header.Get(internalKeyHeader), r.URL.Path, r.Method
			got.body = nil
			_ = json.NewDecoder(r.Body).Decode(&got.body)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "k-test")
}

func TestNewNeedsURLAndKey(t *testing.T) {
	if New("", "k") != nil || New("http://m", "") != nil {
		t.Fatal("a client without a URL or a key must be nil (fail closed), not an anonymous caller")
	}
}

func TestGetDecodesTheAssetAndPresentsTheKey(t *testing.T) {
	id, owner := uuid.New(), uuid.New()
	var got seen
	c := serve(t, http.StatusOK, fmt.Sprintf(
		`{"data":{"id":%q,"uploader_id":%q,"file_type":"image","processing_status":"ready","moderation_status":"passed"}}`,
		id, owner), &got)
	a, err := c.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if a.UploaderID != owner || a.ProcessingStatus != "ready" || a.ModerationStatus != "passed" {
		t.Fatalf("decoded %+v", a)
	}
	if got.key != "k-test" || got.path != "/v1/media/"+id.String() {
		t.Fatalf("request key=%q path=%q", got.key, got.path)
	}
}

func TestGetFailsClosed(t *testing.T) {
	id := uuid.New()
	cases := map[string]struct {
		status int
		body   string
	}{
		"5xx":           {http.StatusBadGateway, `{}`},
		"401":           {http.StatusUnauthorized, `{}`},
		"undecodable":   {http.StatusOK, `not json`},
		"empty data":    {http.StatusOK, `{"data":{}}`},
		"another asset": {http.StatusOK, fmt.Sprintf(`{"data":{"id":%q}}`, uuid.New())},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := serve(t, tc.status, tc.body, nil).Get(context.Background(), id)
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("got %v, want ErrUnavailable", err)
			}
		})
	}
	if _, err := serve(t, http.StatusNotFound, `{}`, nil).Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 = %v, want ErrNotFound", err)
	}
	var nilClient *Client
	if _, err := nilClient.Get(context.Background(), id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client = %v, want ErrUnavailable", err)
	}
}

func TestClaimSendsClaimantKindRefAndIDs(t *testing.T) {
	id := uuid.New()
	var got seen
	c := serve(t, http.StatusOK, `{"data":{"claimed":1}}`, &got)
	if err := c.Claim(context.Background(), KindLiveCover, "ref-1", []uuid.UUID{id}); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got.method != http.MethodPost || got.path != "/v1/media/internal/claims" || got.key != "k-test" {
		t.Fatalf("request %+v", got)
	}
	ids, _ := got.body["media_ids"].([]any)
	if got.body["claimant"] != Claimant || got.body["kind"] != KindLiveCover || got.body["ref"] != "ref-1" ||
		len(ids) != 1 || ids[0] != id.String() {
		t.Fatalf("body %v", got.body)
	}
}

func TestReleaseAndReleaseExceptBodies(t *testing.T) {
	keep := uuid.New()
	var got seen
	c := serve(t, http.StatusOK, `{"data":{"released":0}}`, &got)
	if err := c.Release(context.Background(), KindLiveCover, "r", nil); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got.path != "/v1/media/internal/claims/release" || got.body["media_ids"] != nil || got.body["except_media_ids"] != nil {
		t.Fatalf("release-all body %v at %s", got.body, got.path)
	}
	if err := c.ReleaseExcept(context.Background(), KindLiveCover, "r", []uuid.UUID{keep}); err != nil {
		t.Fatalf("ReleaseExcept: %v", err)
	}
	except, _ := got.body["except_media_ids"].([]any)
	if len(except) != 1 || except[0] != keep.String() || got.body["media_ids"] != nil {
		t.Fatalf("release-except body %v", got.body)
	}
	// An empty keep set would release everything: refused before sending.
	if err := c.ReleaseExcept(context.Background(), KindLiveCover, "r", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("empty keep = %v, want refusal", err)
	}
}

// A 404 is ErrNotFound only with media-service's code; a bare 404 is a
// media-service without the claim routes, which is not a verdict.
func TestClaim404OnlyWithTheCodeIsNotFound(t *testing.T) {
	err := serve(t, http.StatusNotFound, `{"error":{"code":"MEDIA_NOT_FOUND"}}`, nil).
		Claim(context.Background(), KindLiveCover, "r", []uuid.UUID{uuid.New()})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("coded 404 = %v, want ErrNotFound", err)
	}
	err = serve(t, http.StatusNotFound, `404 page not found`, nil).
		Claim(context.Background(), KindLiveCover, "r", []uuid.UUID{uuid.New()})
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotFound) {
		t.Fatalf("bare 404 = %v, want ErrUnavailable", err)
	}
	err = serve(t, http.StatusNotFound, `{"error":{"code":"ROUTE_NOT_FOUND"}}`, nil).
		Claim(context.Background(), KindLiveCover, "r", []uuid.UUID{uuid.New()})
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotFound) {
		t.Fatalf("404 with another code = %v, want ErrUnavailable", err)
	}
	err = serve(t, http.StatusBadRequest, `{"error":{"code":"CLAIM_INVALID"}}`, nil).
		Claim(context.Background(), KindLiveCover, "r", []uuid.UUID{uuid.New()})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("400 = %v, want ErrUnavailable", err)
	}
	var nilClient *Client
	if err := nilClient.Release(context.Background(), KindLiveCover, "r", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client = %v", err)
	}
}
