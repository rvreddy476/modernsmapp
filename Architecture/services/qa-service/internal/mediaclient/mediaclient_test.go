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

// testKind is any kind this service registers.
const testKind = KindQuestionMedia

type capture struct {
	method, path, key string
	body              map[string]any
}

func serve(t *testing.T, status int, body string, got *capture) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got != nil {
			got.method, got.path, got.key = r.Method, r.URL.Path, r.Header.Get(internalKeyHeader)
			if r.Method == http.MethodPost {
				_ = json.NewDecoder(r.Body).Decode(&got.body)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "k-test")
}

func TestGetDecodesTheAssetAndPresentsTheKey(t *testing.T) {
	id, owner := uuid.New(), uuid.New()
	var got capture
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
		t.Fatalf("asked %q with key %q; without the key media-service answers as to an anonymous viewer", got.path, got.key)
	}
}

// Only a 404 is "no such media"; every other answer that is not the asked-for
// asset is ErrUnavailable, never a usable asset.
func TestGetFailsClosed(t *testing.T) {
	id := uuid.New()
	if _, err := serve(t, http.StatusNotFound, `{}`, nil).Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 = %v, want ErrNotFound", err)
	}
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"server error":   {http.StatusInternalServerError, `{}`},
		"unauthorized":   {http.StatusUnauthorized, `{}`},
		"not json":       {http.StatusOK, `<html>`},
		"no asset":       {http.StatusOK, `{"data":{}}`},
		"another asset":  {http.StatusOK, fmt.Sprintf(`{"data":{"id":%q}}`, uuid.New())},
		"created not ok": {http.StatusCreated, fmt.Sprintf(`{"data":{"id":%q}}`, id)},
	} {
		a, err := serve(t, tc.status, tc.body, nil).Get(context.Background(), id)
		if !errors.Is(err, ErrUnavailable) || a != nil {
			t.Errorf("%s: got (%v, %v), want ErrUnavailable", name, a, err)
		}
	}
	var none *Client
	if _, err := none.Get(context.Background(), id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client = %v", err)
	}
	if New("", "k") != nil || New("http://m", "") != nil {
		t.Fatal("a blank URL or a blank key must yield no client")
	}
}

func TestClaimPostsTheHolderAsThisService(t *testing.T) {
	var got capture
	c := serve(t, http.StatusOK, `{"data":{"claimed":1}}`, &got)
	ref, id := uuid.New(), uuid.New()
	if err := c.Claim(context.Background(), testKind, ref, []uuid.UUID{id}); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got.path != "/v1/media/internal/claims" || got.key != "k-test" {
		t.Fatalf("posted to %q with key %q", got.path, got.key)
	}
	if got.body["claimant"] != Claimant || got.body["kind"] != testKind || got.body["ref"] != ref.String() {
		t.Fatalf("body %v", got.body)
	}
	ids, _ := got.body["media_ids"].([]any)
	if len(ids) != 1 || ids[0] != id.String() {
		t.Fatalf("media_ids %v", got.body["media_ids"])
	}
}

func TestReleaseAllOmitsTheIDs(t *testing.T) {
	var got capture
	c := serve(t, http.StatusOK, `{"data":{"released":0}}`, &got)
	if err := c.Release(context.Background(), testKind, uuid.New(), nil); err != nil {
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
// media-service without the claim route, and a 400 is a kind it has not
// registered yet: both must refuse the attach as unavailable, never succeed.
func TestClaimAnswers(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"asset missing":     {http.StatusNotFound, `{"error":{"code":"MEDIA_NOT_FOUND"}}`, ErrNotFound},
		"route missing":     {http.StatusNotFound, `404 page not found`, ErrUnavailable},
		"other 404 code":    {http.StatusNotFound, `{"error":{"code":"NOT_FOUND"}}`, ErrUnavailable},
		"kind unregistered": {http.StatusBadRequest, `{"error":{"code":"CLAIM_INVALID"}}`, ErrUnavailable},
		"server error":      {http.StatusInternalServerError, `{}`, ErrUnavailable},
		"created is not ok": {http.StatusCreated, `{}`, ErrUnavailable},
	} {
		err := serve(t, tc.status, tc.body, nil).Claim(context.Background(), testKind, uuid.New(), []uuid.UUID{uuid.New()})
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
	var none *Client
	if err := none.Claim(context.Background(), testKind, uuid.New(), []uuid.UUID{uuid.New()}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client claim = %v", err)
	}
	if err := none.Release(context.Background(), testKind, uuid.New(), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client release = %v", err)
	}
}

// "Keep nothing" must never go out as a release-except.
func TestReleaseExceptNeedsAKeepSet(t *testing.T) {
	var got capture
	c := serve(t, http.StatusOK, `{}`, &got)
	if err := c.ReleaseExcept(context.Background(), testKind, uuid.New(), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("empty keep = %v", err)
	}
	if got.path != "" {
		t.Fatal("an empty keep set was sent")
	}
	keep := uuid.New()
	if err := c.ReleaseExcept(context.Background(), testKind, uuid.New(), []uuid.UUID{keep}); err != nil {
		t.Fatal(err)
	}
	ids, _ := got.body["except_media_ids"].([]any)
	if got.path != "/v1/media/internal/claims/release" || len(ids) != 1 || ids[0] != keep.String() {
		t.Fatalf("sent %q %v", got.path, got.body)
	}
}
