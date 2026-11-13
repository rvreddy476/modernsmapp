package officialverify

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestAuthRoleClient(t *testing.T) {
	admin, other := uuid.New(), uuid.New()
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Internal-Service-Key")
		switch r.URL.Path {
		case "/v1/auth/internal/roles/" + admin.String():
			_, _ = w.Write([]byte(`{"data":{"user_id":"` + admin.String() + `","roles":["superadmin","admin","moderator"]}}`))
		case "/v1/auth/internal/roles/" + other.String():
			_, _ = w.Write([]byte(`{"data":{"user_id":"` + other.String() + `","roles":["moderator","admin_viewer"]}}`))
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := NewAuthRoleClient(srv.URL+"/", "k")
	if ok, err := c.HasRole(context.Background(), admin, AdminRole); err != nil || !ok {
		t.Fatalf("admin = %v, %v", ok, err)
	}
	if gotKey != "k" {
		t.Fatal("internal key not sent")
	}
	if ok, err := c.HasRole(context.Background(), other, AdminRole); err != nil || ok {
		t.Fatalf("moderator counted as admin: %v, %v", ok, err)
	}
	if ok, err := c.HasRole(context.Background(), uuid.New(), AdminRole); !errors.Is(err, ErrRolesUnavailable) || ok {
		t.Fatalf("a failed read = %v, %v", ok, err)
	}
	if NewAuthRoleClient("", "k") != nil || NewAuthRoleClient("http://x", "") != nil {
		t.Fatal("a client without a URL or key")
	}
}

func TestAuthRoleClient_AnswerForAnotherUserIsRefused(t *testing.T) {
	asked := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"user_id":"` + uuid.NewString() + `","roles":["admin"]}}`))
	}))
	defer srv.Close()
	if ok, err := NewAuthRoleClient(srv.URL, "k").HasRole(context.Background(), asked, AdminRole); err == nil || ok {
		t.Fatalf("mismatched user id accepted: %v %v", ok, err)
	}
}
