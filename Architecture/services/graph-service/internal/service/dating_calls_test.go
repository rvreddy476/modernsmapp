package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

type stubDatingProvider struct {
	open  bool
	err   error
	calls int
}

func (s *stubDatingProvider) HasOpenDatingMatch(ctx context.Context, a, b uuid.UUID) (bool, error) {
	s.calls++
	if s.err != nil {
		return true, s.err // deliberately returns true WITH an error
	}
	return s.open, nil
}

// The fact must fail closed. The stub above returns (true, err) on purpose:
// a caller that reads the bool before checking the error would grant a call
// to a pair with no match at all, every time chat-service hiccups.
func TestDatingMatchFactFailsClosedOnProviderError(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	p := &stubDatingProvider{err: errors.New("chat-service unreachable")}
	svc := (&Service{}).WithDatingCalls(true, p)

	if svc.datingMatchFact(context.Background(), a, b) {
		t.Fatal("provider error granted the dating-match fact; it must fail closed")
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", p.calls)
	}
}

func TestDatingMatchFactOffWhenFlagDisabled(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	p := &stubDatingProvider{open: true}
	svc := (&Service{}).WithDatingCalls(false, p)

	if svc.datingMatchFact(context.Background(), a, b) {
		t.Fatal("fact was true with DATING_CALLS_ENABLED off")
	}
	if p.calls != 0 {
		t.Fatalf("provider was called %d times with the flag off; want 0", p.calls)
	}
}

func TestDatingMatchFactOffWithNilProvider(t *testing.T) {
	svc := (&Service{}).WithDatingCalls(true, nil)
	if svc.DatingCallsEnabled() {
		t.Fatal("dating calls reported enabled with a nil provider")
	}
	if svc.datingMatchFact(context.Background(), uuid.New(), uuid.New()) {
		t.Fatal("fact was true with a nil provider")
	}
}

func TestDatingMatchFactTrueOnOpenMatch(t *testing.T) {
	p := &stubDatingProvider{open: true}
	svc := (&Service{}).WithDatingCalls(true, p)
	if !svc.datingMatchFact(context.Background(), uuid.New(), uuid.New()) {
		t.Fatal("open match did not produce the fact")
	}
}

func TestDatingMatchFactSelfPairNeverFetches(t *testing.T) {
	self := uuid.New()
	p := &stubDatingProvider{open: true}
	svc := (&Service{}).WithDatingCalls(true, p)
	if svc.datingMatchFact(context.Background(), self, self) {
		t.Fatal("a user matched themselves")
	}
	if p.calls != 0 {
		t.Fatalf("provider called %d times for a self-pair; want 0", p.calls)
	}
}

// The cache key must be direction-independent: A→B and B→A are the same fact.
func TestDatingPairKeyIsSymmetric(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if datingPairKey(a, b) != datingPairKey(b, a) {
		t.Fatal("dating pair cache key depends on argument order")
	}
}

// --- the HTTP provider ------------------------------------------------------

func TestHTTPDatingMatchProviderRequiresConfig(t *testing.T) {
	if _, err := NewHTTPDatingMatchProvider("", "key", nil); err == nil {
		t.Fatal("empty MESSAGE_SERVICE_URL accepted")
	}
	if _, err := NewHTTPDatingMatchProvider("chat:8080", "key", nil); err == nil {
		t.Fatal("scheme-less MESSAGE_SERVICE_URL accepted")
	}
	if _, err := NewHTTPDatingMatchProvider("http://chat:8080", "  ", nil); err == nil {
		t.Fatal("empty INTERNAL_SERVICE_KEY accepted")
	}
	if _, err := NewHTTPDatingMatchProvider("http://chat:8080/", "key", nil); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestHTTPDatingMatchProviderReadsOpenMatch(t *testing.T) {
	var gotKey, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Internal-Service-Key")
		gotPath = r.URL.Path
		if r.URL.RawQuery != "" {
			t.Errorf("user ids leaked into the query string: %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"open_match":true}}`))
	}))
	defer srv.Close()

	p, err := NewHTTPDatingMatchProvider(srv.URL, "secret-key", srv.Client())
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	open, err := p.HasOpenDatingMatch(context.Background(), uuid.New(), uuid.New())
	if err != nil || !open {
		t.Fatalf("open=%v err=%v; want true,nil", open, err)
	}
	if gotKey != "secret-key" {
		t.Fatalf("internal key header = %q", gotKey)
	}
	if gotPath != "/internal/v1/chat/dating-match/state" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestHTTPDatingMatchProviderErrorsAreErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", http.StatusUnauthorized, `{"error":"nope"}`},
		{"server error", http.StatusInternalServerError, `{"error":"boom"}`},
		{"undecodable body", http.StatusOK, `not json`},
		{"200 without the field is unknown, not false", http.StatusOK, `{"data":{}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			p, err := NewHTTPDatingMatchProvider(srv.URL, "key", srv.Client())
			if err != nil {
				t.Fatalf("provider: %v", err)
			}
			open, err := p.HasOpenDatingMatch(context.Background(), uuid.New(), uuid.New())
			if err == nil {
				t.Fatal("expected an error so the caller fails closed")
			}
			if open {
				t.Fatal("provider reported an open match alongside an error")
			}
		})
	}
}

// End to end through the fact: a refusing chat-service must not grant a call.
func TestDatingMatchFactFailsClosedAgainstRefusingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p, err := NewHTTPDatingMatchProvider(srv.URL, "key", srv.Client())
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	svc := (&Service{}).WithDatingCalls(true, p)
	if svc.datingMatchFact(context.Background(), uuid.New(), uuid.New()) {
		t.Fatal("a refused probe granted the dating-match fact")
	}
}
