package clients

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// Security sweep #11 (21 Oct 2026): post-service admits its internal
// review-status and visibility writes only with a reviewer-service token
// carrying the route's operation. The fake below verifies exactly that, the
// way post-service's requireServiceToken does.

type fakePostWrites struct {
	mu       sync.Mutex
	verifier *servicetoken.Verifier
	calls    map[string]int
	refused  map[string]int
}

func (f *fakePostWrites) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op := map[string]string{
		"/v1/posts/internal/review-status": OpReviewStatusWrite,
		"/v1/posts/internal/visibility":    OpVisibilityWrite,
	}[r.URL.Path]
	f.calls[r.URL.Path]++
	v, err := f.verifier.Verify(strings.TrimPrefix(r.Header.Get(ServiceAuthHeader), "Bearer "), op, "")
	if op == "" || err != nil || v.Issuer != Issuer || r.Header.Get("X-Internal-Service-Key") != "k" {
		f.refused[r.URL.Path]++
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func newFakePostWrites(t *testing.T) (*fakePostWrites, *servicetoken.Signer, *httptest.Server) {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(Issuer, "v1", priv)
	if err != nil {
		t.Fatal(err)
	}
	v := servicetoken.NewVerifier(postAudience)
	if err := v.RegisterBase64(Issuer, "v1", pub, []string{OpReviewStatusWrite, OpVisibilityWrite}, nil); err != nil {
		t.Fatal(err)
	}
	f := &fakePostWrites{verifier: v, calls: map[string]int{}, refused: map[string]int{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, signer, srv
}

func TestPostWritesCarryTheirOperationToken(t *testing.T) {
	f, signer, srv := newFakePostWrites(t)
	c := New("", "", srv.URL, "", "k").WithPostTokens(signer)
	ctx := context.Background()
	if err := c.SetPostReviewStatus(ctx, uuid.New(), "approved"); err != nil {
		t.Fatalf("review-status: %v", err)
	}
	if err := c.SetPostVisibility(ctx, uuid.New(), "public"); err != nil {
		t.Fatalf("visibility: %v", err)
	}
	for path, n := range f.refused {
		if n != 0 {
			t.Fatalf("%s refused %d time(s): the token did not carry the route's operation", path, n)
		}
	}
	if f.calls["/v1/posts/internal/review-status"] != 1 || f.calls["/v1/posts/internal/visibility"] != 1 {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestPostWritesWithoutASignerAreNotSent(t *testing.T) {
	f, _, srv := newFakePostWrites(t)
	c := New("", "", srv.URL, "", "k")
	ctx := context.Background()
	if err := c.SetPostReviewStatus(ctx, uuid.New(), "approved"); !errors.Is(err, ErrPostTokensNotConfigured) {
		t.Fatalf("review-status without a signer: %v", err)
	}
	if err := c.SetPostVisibility(ctx, uuid.New(), "public"); !errors.Is(err, ErrPostTokensNotConfigured) {
		t.Fatalf("visibility without a signer: %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("requests were sent without a token: %v", f.calls)
	}
}
