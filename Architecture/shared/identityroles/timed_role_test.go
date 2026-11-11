package identityroles

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// premium is a timed role: never an ecosystem role, never granted without
// an expiry, and its expiry reaches identity as RFC 3339 UTC.
func TestPremiumIsTimedNotEcosystem(t *testing.T) {
	if RolePremium != "premium" || !IsTimedRole(RolePremium) {
		t.Fatal("premium must be the timed role")
	}
	if IsEcosystemRole(RolePremium) {
		t.Fatal("premium must not be an ecosystem role: identity takes it only with expires_at")
	}
	for _, r := range Ecosystem() {
		if IsTimedRole(r) {
			t.Fatalf("ecosystem role %q must not be timed", r)
		}
	}
}

func TestGrantRefusesTimedRoleWithoutExpiry(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	err := newTestClient(srv).Grant(context.Background(), testUser, RolePremium, "")
	if !errors.Is(err, ErrNotGrantable) || called {
		t.Fatalf("Grant(premium) err=%v called=%v; want ErrNotGrantable and no request", err, called)
	}
}

func TestGrantUntilSendsExpiry(t *testing.T) {
	var got map[string]any
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"data":{"status":"granted"}}`))
	}))
	defer srv.Close()
	at := time.Now().Add(33 * 24 * time.Hour).Truncate(time.Second).In(time.FixedZone("IST", 19800))
	if err := newTestClient(srv).GrantUntil(context.Background(), testUser, RolePremium, "pass", at); err != nil {
		t.Fatalf("GrantUntil: %v", err)
	}
	if method != http.MethodPost || got["role"] != "premium" {
		t.Fatalf("method=%s body=%v", method, got)
	}
	if got["expires_at"] != at.UTC().Format(time.RFC3339) {
		t.Fatalf("expires_at = %v, want %s", got["expires_at"], at.UTC().Format(time.RFC3339))
	}
}

func TestGrantUntilRefusals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request may be sent")
	}))
	defer srv.Close()
	c := newTestClient(srv)
	if err := c.GrantUntil(context.Background(), testUser, RoleSeller, "", time.Now().Add(time.Hour)); !errors.Is(err, ErrNotGrantable) {
		t.Fatalf("seller via GrantUntil: %v", err)
	}
	if err := c.GrantUntil(context.Background(), testUser, RolePremium, "", time.Now().Add(-time.Second)); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("past expiry: %v", err)
	}
	if err := c.GrantUntil(context.Background(), testUser, RolePremium, "", time.Time{}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("zero expiry: %v", err)
	}
}

func TestRevokeAcceptsTimedRole(t *testing.T) {
	var method string
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"data":{"status":"revoked"}}`))
	}))
	defer srv.Close()
	if err := newTestClient(srv).Revoke(context.Background(), testUser, RolePremium, "lapsed"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if method != http.MethodDelete {
		t.Fatalf("method = %s", method)
	}
	if _, ok := got["expires_at"]; ok {
		t.Fatalf("a revoke carries no expiry: %v", got)
	}
	if err := newTestClient(srv).Revoke(context.Background(), testUser, "admin", ""); !errors.Is(err, ErrNotGrantable) {
		t.Fatalf("revoke admin: %v", err)
	}
}
