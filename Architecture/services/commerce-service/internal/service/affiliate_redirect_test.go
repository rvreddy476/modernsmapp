package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Exercises ResolveAffiliateRedirect against a fake monetization-service.
// The product lookup hits the real store, so these tests skip when the
// store is nil (covered separately by store tests). We can still pin
// the monetization-side error mapping which is the security-relevant
// bit (404 / inactive / unreachable).

func TestResolveAffiliateRedirectLinkNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	s := newRedirectTestService(srv.URL)
	_, err := s.ResolveAffiliateRedirect(context.Background(), uuid.New())
	if !errors.Is(err, ErrAffiliateRedirectLinkNotFound) {
		t.Fatalf("got %v want ErrAffiliateRedirectLinkNotFound", err)
	}
}

func TestResolveAffiliateRedirectLinkInactive(t *testing.T) {
	linkID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"id":"%s","link_code":"abc","listing_id":"%s","is_active":false}}`,
			linkID, uuid.New())
	}))
	defer srv.Close()

	s := newRedirectTestService(srv.URL)
	_, err := s.ResolveAffiliateRedirect(context.Background(), linkID)
	if !errors.Is(err, ErrAffiliateRedirectLinkInactive) {
		t.Fatalf("got %v want ErrAffiliateRedirectLinkInactive", err)
	}
}

func TestResolveAffiliateRedirectMonetizationUnreachable(t *testing.T) {
	s := newRedirectTestService("http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := s.ResolveAffiliateRedirect(ctx, uuid.New())
	if err == nil {
		t.Fatal("want error on unreachable monetization")
	}
	// Fail-closed: do NOT bubble out a typed error from the typed set.
	if errors.Is(err, ErrAffiliateRedirectLinkNotFound) ||
		errors.Is(err, ErrAffiliateRedirectLinkInactive) ||
		errors.Is(err, ErrAffiliateRedirectProductMissing) {
		t.Fatalf("unreachable shouldn't surface a typed error, got %v", err)
	}
}

func TestResolveAffiliateRedirectURLEnsuresMissingConfig(t *testing.T) {
	s := &Service{httpClient: &http.Client{Timeout: time.Second}}
	_, err := s.ResolveAffiliateRedirect(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("want error when monetizationServiceURL is empty")
	}
}

// ─── helpers ────────────────────────────────────────────────────────

func newRedirectTestService(monetizationURL string) *Service {
	return &Service{
		monetizationServiceURL: monetizationURL,
		httpClient:             &http.Client{Timeout: 2 * time.Second},
	}
}
