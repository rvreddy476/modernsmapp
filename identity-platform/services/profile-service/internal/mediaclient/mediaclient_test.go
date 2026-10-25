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

// The claimant and kind strings are media-service's registry keys
// (media-service internal/service/media_claims.go claimKinds). A drift here
// turns every avatar save into a 400 there.
func TestRegisteredNames(t *testing.T) {
	if Claimant != "profile-service" || KindProfileAvatar != "profile_avatar" || KindProfileCover != "profile_cover" {
		t.Fatalf("claimant/kinds changed: %q %q %q", Claimant, KindProfileAvatar, KindProfileCover)
	}
}

func TestClaimPostsTheHolderWithTheKey(t *testing.T) {
	var got claimCapture
	c := serveClaims(t, http.StatusOK, `{"data":{"claimed":1}}`, &got)
	ref, id := uuid.New(), uuid.New()
	if err := c.Claim(context.Background(), KindProfileAvatar, ref, []uuid.UUID{id}); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got.path != "/v1/media/internal/claims" || got.key != "k-test" {
		t.Fatalf("posted to %q (key presented: %v)", got.path, got.key == "k-test")
	}
	if got.body["claimant"] != "profile-service" || got.body["kind"] != "profile_avatar" || got.body["ref"] != ref.String() {
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
	if err := c.Release(context.Background(), KindProfileCover, uuid.New(), nil); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got.path != "/v1/media/internal/claims/release" || got.body["kind"] != "profile_cover" {
		t.Fatalf("posted to %q: %v", got.path, got.body)
	}
	if _, ok := got.body["media_ids"]; ok {
		t.Fatalf("a release-all must not send media_ids: %v", got.body)
	}
}

// Only media-service's own MEDIA_NOT_FOUND is a verdict. A bare 404 is a
// media-service without the claim route (deployed out of order).
func TestClaimAnswers(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"asset missing":     {http.StatusNotFound, `{"error":{"code":"MEDIA_NOT_FOUND","message":"media does not exist"}}`, ErrNotFound},
		"route missing":     {http.StatusNotFound, `404 page not found`, ErrUnavailable},
		"other 404 code":    {http.StatusNotFound, `{"error":{"code":"NOT_FOUND"}}`, ErrUnavailable},
		"unregistered":      {http.StatusBadRequest, `{"error":{"code":"CLAIM_INVALID"}}`, ErrUnavailable},
		"server error":      {http.StatusInternalServerError, `{}`, ErrUnavailable},
		"unauthenticated":   {http.StatusUnauthorized, `{}`, ErrUnavailable},
		"created is not ok": {http.StatusCreated, `{}`, ErrUnavailable},
	} {
		c := serveClaims(t, tc.status, tc.body, nil)
		if err := c.Claim(context.Background(), KindProfileAvatar, uuid.New(), []uuid.UUID{uuid.New()}); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
	var nilClient *Client
	if err := nilClient.Claim(context.Background(), KindProfileAvatar, uuid.New(), []uuid.UUID{uuid.New()}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client claim = %v", err)
	}
	if err := nilClient.Release(context.Background(), KindProfileAvatar, uuid.New(), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client release = %v", err)
	}
}

func TestUnreachableAndUnconfigured(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if err := New(url, "k").Claim(context.Background(), KindProfileAvatar, uuid.New(), []uuid.UUID{uuid.New()}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unreachable = %v, want ErrUnavailable", err)
	}
	if New("", "k") != nil || New("http://media", "") != nil {
		t.Fatal("a blank URL or a blank key must yield no client")
	}
}

// "Keep nothing" must never go out as a release-except.
func TestReleaseExceptNeedsAKeepSet(t *testing.T) {
	var got claimCapture
	c := serveClaims(t, http.StatusOK, `{}`, &got)
	if err := c.ReleaseExcept(context.Background(), KindProfileAvatar, uuid.New(), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("empty keep = %v", err)
	}
	if got.path != "" {
		t.Fatal("an empty keep set was sent")
	}
	keep := uuid.New()
	if err := c.ReleaseExcept(context.Background(), KindProfileAvatar, uuid.New(), []uuid.UUID{keep}); err != nil {
		t.Fatal(err)
	}
	ids, _ := got.body["except_media_ids"].([]any)
	if got.path != "/v1/media/internal/claims/release" || len(ids) != 1 || ids[0] != keep.String() {
		t.Fatalf("sent %q %v", got.path, got.body)
	}
}
