package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestProfilePhotoAccessUsesPrivacyForAnonymousViewer(t *testing.T) {
	ownerID := uuid.New()
	userService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != "secret" {
			t.Error("internal key was not forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"who_can_see_profile_photo":"everyone"}}`))
	}))
	defer userService.Close()

	checker := NewProfilePhotoAccessChecker("", userService.URL, "secret")
	allowed, err := checker.CanViewProfilePhoto(context.Background(), uuid.Nil, ownerID)
	if err != nil || !allowed {
		t.Fatalf("anonymous viewer of everyone photo: allowed=%v err=%v", allowed, err)
	}
}

func TestProfilePhotoAccessUsesGraphForAuthenticatedViewer(t *testing.T) {
	viewerID, ownerID := uuid.New(), uuid.New()
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-Id") != viewerID.String() {
			t.Error("verified viewer was not forwarded")
		}
		if r.URL.Query().Get("actions") != "view_profile" {
			t.Error("wrong permission action")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"decisions":{"view_profile":{"allowed":true}}}}`))
	}))
	defer graph.Close()

	checker := NewProfilePhotoAccessChecker(graph.URL, "", "secret")
	allowed, err := checker.CanViewProfilePhoto(context.Background(), viewerID, ownerID)
	if err != nil || !allowed {
		t.Fatalf("authenticated viewer: allowed=%v err=%v", allowed, err)
	}
}
