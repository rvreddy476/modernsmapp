package mediaclient

import (
	"context"
	"encoding/json"
	"errors"
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

func TestClaimSendsClaimantKindRefAndIDs(t *testing.T) {
	id := uuid.New()
	var got seen
	c := serve(t, http.StatusOK, `{"data":{"claimed":1}}`, &got)
	if err := c.Claim(context.Background(), KindPhoto, "ref-1", []uuid.UUID{id}); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got.method != http.MethodPost || got.path != "/v1/media/internal/claims" || got.key != "k-test" {
		t.Fatalf("request %+v", got)
	}
	ids, _ := got.body["media_ids"].([]any)
	if got.body["claimant"] != Claimant || got.body["kind"] != KindPhoto || got.body["ref"] != "ref-1" ||
		len(ids) != 1 || ids[0] != id.String() {
		t.Fatalf("body %v", got.body)
	}
}

func TestReleaseAndReleaseExceptBodies(t *testing.T) {
	keep := uuid.New()
	var got seen
	c := serve(t, http.StatusOK, `{"data":{"released":0}}`, &got)
	if err := c.Release(context.Background(), KindPhoto, "r", nil); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got.path != "/v1/media/internal/claims/release" || got.body["media_ids"] != nil || got.body["except_media_ids"] != nil {
		t.Fatalf("release-all body %v at %s", got.body, got.path)
	}
	if err := c.ReleaseExcept(context.Background(), KindPhoto, "r", []uuid.UUID{keep}); err != nil {
		t.Fatalf("ReleaseExcept: %v", err)
	}
	except, _ := got.body["except_media_ids"].([]any)
	if len(except) != 1 || except[0] != keep.String() || got.body["media_ids"] != nil {
		t.Fatalf("release-except body %v", got.body)
	}
	// An empty keep set would release everything: refused before sending.
	if err := c.ReleaseExcept(context.Background(), KindPhoto, "r", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("empty keep = %v, want refusal", err)
	}
}

// A 404 is ErrNotFound only with media-service's code; a bare 404 is a
// media-service without the claim routes, which is not a verdict.
func TestClaim404OnlyWithTheCodeIsNotFound(t *testing.T) {
	err := serve(t, http.StatusNotFound, `{"error":{"code":"MEDIA_NOT_FOUND"}}`, nil).
		Claim(context.Background(), KindPhoto, "r", []uuid.UUID{uuid.New()})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("coded 404 = %v, want ErrNotFound", err)
	}
	err = serve(t, http.StatusNotFound, `404 page not found`, nil).
		Claim(context.Background(), KindPhoto, "r", []uuid.UUID{uuid.New()})
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotFound) {
		t.Fatalf("bare 404 = %v, want ErrUnavailable", err)
	}
	err = serve(t, http.StatusNotFound, `{"error":{"code":"ROUTE_NOT_FOUND"}}`, nil).
		Claim(context.Background(), KindPhoto, "r", []uuid.UUID{uuid.New()})
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotFound) {
		t.Fatalf("404 with another code = %v, want ErrUnavailable", err)
	}
	err = serve(t, http.StatusBadRequest, `{"error":{"code":"CLAIM_INVALID"}}`, nil).
		Claim(context.Background(), KindPhoto, "r", []uuid.UUID{uuid.New()})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("400 = %v, want ErrUnavailable", err)
	}
	var nilClient *Client
	if err := nilClient.Release(context.Background(), KindPhoto, "r", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client = %v", err)
	}
}
