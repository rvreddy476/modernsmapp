package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// US-M3: a seller may link only a business page they own. The id used to be
// stored from the request body unchecked, so naming a stranger's page linked
// it to the attacker's shop on approval.

const pageOwnerTestKey = "test-internal-key"

// pageOwnerServer stands in for user-service GET /internal/pages/:id/owner.
func pageOwnerServer(t *testing.T, owners map[uuid.UUID]uuid.UUID, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != pageOwnerTestKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // internal/pages/<id>/owner
		if len(parts) != 4 || parts[0] != "internal" || parts[1] != "pages" || parts[3] != "owner" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		id, err := uuid.Parse(parts[2])
		owner, ok := owners[id]
		if err != nil || !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"page_id": id, "user_id": owner, "status": "draft"}})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("APP_USER_SERVICE_URL", srv.URL)
	return srv
}

func TestVerifyBusinessPageOwner(t *testing.T) {
	seller, stranger := uuid.New(), uuid.New()
	ownPage, strangersPage := uuid.New(), uuid.New()
	srv := pageOwnerServer(t, map[uuid.UUID]uuid.UUID{ownPage: seller, strangersPage: stranger}, 0)
	svc := (&Service{}).WithInternalServiceKey(pageOwnerTestKey).WithHTTPClient(srv.Client())
	ctx := context.Background()

	if err := svc.verifyBusinessPageOwner(ctx, seller, ownPage); err != nil {
		t.Fatalf("the seller's own page was refused: %v", err)
	}
	if err := svc.verifyBusinessPageOwner(ctx, seller, strangersPage); !errors.Is(err, ErrBusinessPageNotOwned) {
		t.Fatalf("someone else's page: got %v, want ErrBusinessPageNotOwned", err)
	}
	if err := svc.verifyBusinessPageOwner(ctx, seller, uuid.New()); !errors.Is(err, ErrBusinessPageNotOwned) {
		t.Fatalf("a page that does not exist: got %v, want ErrBusinessPageNotOwned", err)
	}
}

// Fail closed: when the check cannot run, the id is refused, not stored.
func TestVerifyBusinessPageOwnerFailsClosed(t *testing.T) {
	seller, page := uuid.New(), uuid.New()
	ctx := context.Background()

	srv := pageOwnerServer(t, map[uuid.UUID]uuid.UUID{page: seller}, 0)
	if err := (&Service{}).WithHTTPClient(srv.Client()).verifyBusinessPageOwner(ctx, seller, page); !errors.Is(err, ErrBusinessPageCheckUnavailable) {
		t.Fatalf("no internal key: got %v, want ErrBusinessPageCheckUnavailable", err)
	}
	if err := (&Service{}).WithInternalServiceKey("wrong").WithHTTPClient(srv.Client()).verifyBusinessPageOwner(ctx, seller, page); !errors.Is(err, ErrBusinessPageCheckUnavailable) {
		t.Fatalf("refused key: got %v, want ErrBusinessPageCheckUnavailable", err)
	}

	broken := pageOwnerServer(t, nil, http.StatusInternalServerError)
	if err := (&Service{}).WithInternalServiceKey(pageOwnerTestKey).WithHTTPClient(broken.Client()).verifyBusinessPageOwner(ctx, seller, page); !errors.Is(err, ErrBusinessPageCheckUnavailable) {
		t.Fatalf("user-service error: got %v, want ErrBusinessPageCheckUnavailable", err)
	}
}

// StartOnboarding runs the check before it reads or writes the store: the
// Service here has no store, so reaching it would panic.
func TestStartOnboardingRefusesAPageTheSellerDoesNotOwn(t *testing.T) {
	seller, stranger, page := uuid.New(), uuid.New(), uuid.New()
	srv := pageOwnerServer(t, map[uuid.UUID]uuid.UUID{page: stranger}, 0)
	svc := (&Service{}).WithInternalServiceKey(pageOwnerTestKey).WithHTTPClient(srv.Client())
	_, err := svc.StartOnboarding(context.Background(), StartOnboardingInput{
		UserID: seller, BusinessPageID: &page, StoreName: "Shop", Email: "s@example.com",
	})
	if !errors.Is(err, ErrBusinessPageNotOwned) {
		t.Fatalf("got %v, want ErrBusinessPageNotOwned", err)
	}
}
