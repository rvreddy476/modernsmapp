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

const testUser = "11111111-1111-4111-8111-111111111111"

// newTestClient points a client at srv with a short timeout.
func newTestClient(srv *httptest.Server) *Client {
	return NewClient(srv.URL, "test-key", "commerce-service").
		WithHTTPClient(&http.Client{Timeout: 2 * time.Second})
}

func TestEcosystemRoleList(t *testing.T) {
	// Guards the copy of auth-service's internal/roles.Ecosystem(). If that
	// list ever changes, this fails and points at the file to update.
	want := []string{"seller", "restaurant_owner", "delivery_partner", "rider_partner"}
	got := Ecosystem()
	if len(got) != len(want) {
		t.Fatalf("Ecosystem() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Ecosystem()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if IsEcosystemRole("admin") || IsEcosystemRole("superadmin") || IsEcosystemRole("customer") {
		t.Fatal("a platform role must never be reported as service-grantable")
	}
}

func TestGrantSuccess(t *testing.T) {
	var gotMethod, gotPath, gotKey string
	var gotBody mutateBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotKey = r.Header.Get("X-Internal-Service-Key")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"status":"granted","user_id":"` + testUser + `","role":"seller"}}`))
	}))
	defer srv.Close()

	if err := newTestClient(srv).Grant(context.Background(), testUser, RoleSeller, "SA-1042 approved"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/v1/auth/internal/roles" {
		t.Errorf("path = %s, want /v1/auth/internal/roles", gotPath)
	}
	if gotKey != "test-key" {
		t.Errorf("X-Internal-Service-Key = %q, want test-key", gotKey)
	}
	if gotBody.UserID != testUser || gotBody.Role != "seller" ||
		gotBody.Service != "commerce-service" || gotBody.Reason != "SA-1042 approved" {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestRevokeUsesDelete(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_, _ = w.Write([]byte(`{"data":{"status":"revoked"}}`))
	}))
	defer srv.Close()

	if err := newTestClient(srv).Revoke(context.Background(), testUser, RoleRiderPartner, "blocked"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %s, want DELETE", gotMethod)
	}
}

func TestGrantNonEcosystemRoleIsRefusedLocally(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	err := newTestClient(srv).Grant(context.Background(), testUser, "superadmin", "nice try")
	if !errors.Is(err, ErrNotGrantable) {
		t.Fatalf("err = %v, want ErrNotGrantable", err)
	}
	if called {
		t.Fatal("a non-ecosystem role must not reach the network")
	}
	if !IsPermanent(err) {
		t.Fatal("ErrNotGrantable must be permanent so the worker dead-letters it")
	}
}

func TestGrant403RoleNotGrantable(t *testing.T) {
	// identity itself refusing, e.g. after its vocabulary changed under us.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"ROLE_NOT_GRANTABLE","message":"role is not service-grantable","details":{"grantable_roles":["seller"]}}}`))
	}))
	defer srv.Close()

	err := newTestClient(srv).Grant(context.Background(), testUser, RoleSeller, "")
	if !errors.Is(err, ErrNotGrantable) {
		t.Fatalf("err = %v, want ErrNotGrantable", err)
	}
	if !IsPermanent(err) {
		t.Fatal("must be permanent")
	}
}

func TestGrant403BadKeyIsNotConfusedWithNotGrantable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"FORBIDDEN","message":"invalid internal service key"}}`))
	}))
	defer srv.Close()

	err := newTestClient(srv).Grant(context.Background(), testUser, RoleSeller, "")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if errors.Is(err, ErrNotGrantable) {
		t.Fatal("a rejected key must not masquerade as a bad role")
	}
}

func TestGrantTimeoutIsRetryable(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never answers within the client timeout
	}))
	defer func() { close(block); srv.Close() }()

	c := NewClient(srv.URL, "k", "food-service").
		WithHTTPClient(&http.Client{Timeout: 80 * time.Millisecond})

	start := time.Now()
	err := c.Grant(context.Background(), testUser, RoleDeliveryPartner, "")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if IsPermanent(err) {
		t.Fatal("a timeout must be retryable, not dead-lettered")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("timeout not honoured, took %v", d)
	}
}

func TestGrantIdentityUnavailable(t *testing.T) {
	// Server closed before the call: connection refused, the shape of
	// identity being down.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := NewClient(url, "k", "rider-service").
		WithHTTPClient(&http.Client{Timeout: time.Second})
	err := c.Grant(context.Background(), testUser, RoleRiderPartner, "")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if IsPermanent(err) {
		t.Fatal("connection refused must be retryable")
	}
}

func TestGrant5xxIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL_ERROR","message":"boom"}}`))
	}))
	defer srv.Close()

	err := newTestClient(srv).Grant(context.Background(), testUser, RoleSeller, "")
	if !errors.Is(err, ErrUnavailable) || IsPermanent(err) {
		t.Fatalf("err = %v, want retryable ErrUnavailable", err)
	}
}

func TestGrant400IsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BAD_REQUEST","message":"invalid user_id"}}`))
	}))
	defer srv.Close()

	err := newTestClient(srv).Grant(context.Background(), "not-a-uuid", RoleSeller, "")
	if !errors.Is(err, ErrBadRequest) || !IsPermanent(err) {
		t.Fatalf("err = %v, want permanent ErrBadRequest", err)
	}
}

func TestUnconfiguredClient(t *testing.T) {
	c := NewClient("", "k", "commerce-service")
	if c.Configured() {
		t.Fatal("empty base URL must not be Configured")
	}
	if err := c.Grant(context.Background(), testUser, RoleSeller, ""); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if !IsPermanent(ErrNotConfigured) {
		t.Fatal("a misconfigured client must dead-letter visibly, not spin")
	}
}

func TestRolesReadsEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/internal/roles/"+testUser {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"user_id":"` + testUser + `","roles":["seller","rider_partner"]}}`))
	}))
	defer srv.Close()

	got, err := newTestClient(srv).Roles(context.Background(), testUser)
	if err != nil {
		t.Fatalf("Roles: %v", err)
	}
	if len(got) != 2 || got[0] != "seller" || got[1] != "rider_partner" {
		t.Fatalf("roles = %v", got)
	}
}

func TestRolesIsNotAnExistenceCheck(t *testing.T) {
	// The trap the backfill has to avoid: identity answers 200 with an empty
	// list for a user id that does not exist at all. If this ever starts
	// 404ing, the backfill can be simplified — but until then UserExists is
	// the only honest check.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"user_id":"` + testUser + `","roles":[]}}`))
	}))
	defer srv.Close()

	got, err := newTestClient(srv).Roles(context.Background(), testUser)
	if err != nil {
		t.Fatalf("Roles: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("roles = %v, want empty non-nil slice", got)
	}
}

func TestUserExists(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   bool
		wantOK bool
	}{
		{"present", http.StatusOK, true, true},
		{"absent", http.StatusNotFound, false, true},
		{"identity down", http.StatusBadGateway, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/auth/internal/users/"+testUser {
					t.Errorf("path = %s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				if tc.status == http.StatusOK {
					_, _ = w.Write([]byte(`{"data":{"user_id":"` + testUser + `","email":"a@b.c"}}`))
				}
			}))
			defer srv.Close()

			got, err := newTestClient(srv).UserExists(context.Background(), testUser)
			if tc.wantOK && err != nil {
				t.Fatalf("UserExists: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Fatal("want an error when identity cannot answer — a caller must never read that as 'no such user'")
			}
			if got != tc.want {
				t.Fatalf("exists = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestContextCancellationSurfacesAsUnavailable(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := newTestClient(srv).Grant(ctx, testUser, RoleSeller, "")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}
