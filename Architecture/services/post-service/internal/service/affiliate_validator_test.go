package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Exercises the validator's 200/404/410-equivalent (is_active=false)/
// 403 (wrong owner) paths against a fake monetization-service. Doesn't
// need Redis — Service.rdb stays nil, which the cache helpers tolerate.

func TestValidateAffiliateLinkValid(t *testing.T) {
	linkID := uuid.New()
	creatorID := uuid.New()
	listingID := uuid.New()

	srv := newMonetizationFake(t, monetizationFakeConfig{
		linkID:    linkID,
		creatorID: creatorID,
		listingID: listingID,
		isActive:  true,
	})
	defer srv.Close()

	s := newValidatorTestService(srv.URL)
	gotCreator, gotListing, err := s.ValidateAffiliateLink(
		context.Background(), linkID, creatorID,
	)
	if err != nil {
		t.Fatalf("ValidateAffiliateLink: %v", err)
	}
	if gotCreator != creatorID || gotListing != listingID {
		t.Fatalf("got (%v,%v) want (%v,%v)",
			gotCreator, gotListing, creatorID, listingID)
	}
}

func TestValidateAffiliateLinkNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	s := newValidatorTestService(srv.URL)
	_, _, err := s.ValidateAffiliateLink(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrAffiliateLinkNotFound) {
		t.Fatalf("got %v want ErrAffiliateLinkNotFound", err)
	}
}

func TestValidateAffiliateLinkInactive(t *testing.T) {
	linkID := uuid.New()
	creatorID := uuid.New()

	srv := newMonetizationFake(t, monetizationFakeConfig{
		linkID:    linkID,
		creatorID: creatorID,
		listingID: uuid.New(),
		isActive:  false, // deactivated by the creator
	})
	defer srv.Close()

	s := newValidatorTestService(srv.URL)
	_, _, err := s.ValidateAffiliateLink(context.Background(), linkID, creatorID)
	if !errors.Is(err, ErrAffiliateLinkInactive) {
		t.Fatalf("got %v want ErrAffiliateLinkInactive", err)
	}
}

func TestValidateAffiliateLinkNotOwned(t *testing.T) {
	linkID := uuid.New()
	ownerID := uuid.New()
	someoneElse := uuid.New()

	srv := newMonetizationFake(t, monetizationFakeConfig{
		linkID:    linkID,
		creatorID: ownerID,
		listingID: uuid.New(),
		isActive:  true,
	})
	defer srv.Close()

	s := newValidatorTestService(srv.URL)
	_, _, err := s.ValidateAffiliateLink(context.Background(), linkID, someoneElse)
	if !errors.Is(err, ErrAffiliateLinkNotOwned) {
		t.Fatalf("got %v want ErrAffiliateLinkNotOwned", err)
	}
}

func TestValidateAffiliateLinkUnreachable(t *testing.T) {
	// URL with a closed port — fast connection refused.
	s := newValidatorTestService("http://127.0.0.1:1")
	// Bound the test in case the OS retries.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := s.ValidateAffiliateLink(ctx, uuid.New(), uuid.New())
	if err == nil {
		t.Fatal("want error on unreachable monetization-service")
	}
	// Fail-closed: NOT a typed error from the typed set, just a wrap.
	if errors.Is(err, ErrAffiliateLinkNotFound) ||
		errors.Is(err, ErrAffiliateLinkInactive) ||
		errors.Is(err, ErrAffiliateLinkNotOwned) {
		t.Fatalf("unreachable shouldn't surface a typed error, got %v", err)
	}
}

func TestValidateAffiliateLinkForwardsInternalKey(t *testing.T) {
	linkID := uuid.New()
	creatorID := uuid.New()
	const wantKey = "internal-key-under-test"

	var seenKey atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey.Store(r.Header.Get("X-Internal-Service-Key"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"id":"%s","creator_id":"%s","listing_id":"%s","is_active":true}}`,
			linkID, creatorID, uuid.New())
	}))
	defer srv.Close()

	s := newValidatorTestService(srv.URL)
	s.internalServiceKey = wantKey
	_, _, err := s.ValidateAffiliateLink(context.Background(), linkID, creatorID)
	if err != nil {
		t.Fatalf("ValidateAffiliateLink: %v", err)
	}
	got, _ := seenKey.Load().(string)
	if got != wantKey {
		t.Fatalf("X-Internal-Service-Key = %q, want %q", got, wantKey)
	}
}

// ─── helpers ────────────────────────────────────────────────────────

type monetizationFakeConfig struct {
	linkID    uuid.UUID
	creatorID uuid.UUID
	listingID uuid.UUID
	isActive  bool
}

func newMonetizationFake(t *testing.T, cfg monetizationFakeConfig) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		want := "/v1/monetization/affiliate/links/" + cfg.linkID.String()
		if r.URL.Path != want {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"id":"%s","creator_id":"%s","listing_id":"%s","is_active":%t}}`,
			cfg.linkID, cfg.creatorID, cfg.listingID, cfg.isActive)
	}))
}

func newValidatorTestService(monetizationURL string) *Service {
	return &Service{
		monetizationServiceURL: monetizationURL,
		httpClient:             &http.Client{Timeout: 2 * time.Second},
	}
}
