package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// US-M3, the second half: the page must also be active, and a page admin is
// admitted as well as its owner. The owner-only and fail-closed cases are in
// onboarding_page_owner_test.go.

func TestDecideBusinessPage(t *testing.T) {
	caller, owner := uuid.New(), uuid.New()
	cases := []struct {
		name          string
		caller, owner uuid.UUID
		role, status  string
		want          error
	}{
		{"owner, approved", caller, caller, "", "approved", nil},
		{"owner, pending review", caller, caller, "owner", "pending_review", nil},
		{"owner, draft", caller, caller, "", "draft", nil},
		{"admin of someone else's page", caller, owner, "admin", "approved", nil},
		{"owner role row, not the creator", caller, owner, "owner", "approved", nil},
		{"editor is not enough", caller, owner, "editor", "approved", ErrBusinessPageNotOwned},
		{"viewer is not enough", caller, owner, "viewer", "approved", ErrBusinessPageNotOwned},
		{"no role", caller, owner, "", "approved", ErrBusinessPageNotOwned},
		{"stranger on a suspended page learns nothing", caller, owner, "", "suspended", ErrBusinessPageNotOwned},
		{"owner, suspended", caller, caller, "", "suspended", ErrBusinessPageInactive},
		{"owner, disabled", caller, caller, "", "disabled", ErrBusinessPageInactive},
		{"admin, rejected", caller, owner, "admin", "rejected", ErrBusinessPageInactive},
		{"owner, unknown status fails closed", caller, caller, "", "archived", ErrBusinessPageInactive},
		{"owner, empty status fails closed", caller, caller, "", "", ErrBusinessPageInactive},
		{"nil caller", uuid.Nil, uuid.Nil, "", "approved", ErrBusinessPageNotOwned},
	}
	for _, tc := range cases {
		if got := decideBusinessPage(tc.caller, tc.owner, tc.role, tc.status); !errors.Is(got, tc.want) && !(got == nil && tc.want == nil) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// pageAnswerServer answers the internal route with a fixed page, the role of
// whichever user_id the caller asked about, and records that it was asked.
func pageAnswerServer(t *testing.T, owner uuid.UUID, status string, roles map[uuid.UUID]string, asked *uuid.UUID) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != pageOwnerTestKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		data := map[string]any{"user_id": owner, "status": status}
		if q := r.URL.Query().Get("user_id"); q != "" {
			id, _ := uuid.Parse(q)
			if asked != nil {
				*asked = id
			}
			data["user_role"] = roles[id]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("APP_USER_SERVICE_URL", srv.URL)
	return srv
}

func TestVerifyBusinessPageAdmitsAnAdminAndAsksAboutTheCaller(t *testing.T) {
	owner, admin, editor := uuid.New(), uuid.New(), uuid.New()
	var asked uuid.UUID
	srv := pageAnswerServer(t, owner, "approved", map[uuid.UUID]string{admin: "admin", editor: "editor"}, &asked)
	svc := (&Service{}).WithInternalServiceKey(pageOwnerTestKey).WithHTTPClient(srv.Client())
	ctx := context.Background()
	page := uuid.New()

	if err := svc.verifyBusinessPageOwner(ctx, admin, page); err != nil {
		t.Fatalf("page admin refused: %v", err)
	}
	if asked != admin {
		t.Fatalf("user-service was asked about %s, want the caller %s", asked, admin)
	}
	if err := svc.verifyBusinessPageOwner(ctx, editor, page); !errors.Is(err, ErrBusinessPageNotOwned) {
		t.Fatalf("editor: %v; want ErrBusinessPageNotOwned", err)
	}
}

func TestVerifyBusinessPageRefusesAnInactivePage(t *testing.T) {
	seller := uuid.New()
	for _, status := range []string{"suspended", "disabled", "rejected"} {
		srv := pageAnswerServer(t, seller, status, nil, nil)
		svc := (&Service{}).WithInternalServiceKey(pageOwnerTestKey).WithHTTPClient(srv.Client())
		if err := svc.verifyBusinessPageOwner(context.Background(), seller, uuid.New()); !errors.Is(err, ErrBusinessPageInactive) {
			t.Errorf("%s: %v; want ErrBusinessPageInactive", status, err)
		}
	}
}

// StartOnboarding refuses before touching the store (none is wired here).
func TestStartOnboardingRefusesAnInactivePage(t *testing.T) {
	seller, page := uuid.New(), uuid.New()
	srv := pageAnswerServer(t, seller, "suspended", nil, nil)
	svc := (&Service{}).WithInternalServiceKey(pageOwnerTestKey).WithHTTPClient(srv.Client())
	_, err := svc.StartOnboarding(context.Background(), StartOnboardingInput{
		UserID: seller, BusinessPageID: &page, StoreName: "Shop", Email: "s@example.com",
	})
	if !errors.Is(err, ErrBusinessPageInactive) {
		t.Fatalf("got %v, want ErrBusinessPageInactive", err)
	}
}
