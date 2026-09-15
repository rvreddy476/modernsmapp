package events

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// Dating lane D1 moved the profile preview onto the service-only family:
// /v1/dating/internal/profile/:userId/preview, which refuses any request
// carrying an end-user identity. The old path is kept for one release only.
func TestDatingPreviewUsesInternalPathWithoutUserIdentity(t *testing.T) {
	userID := uuid.NewString()
	wantPath := fmt.Sprintf("/v1/dating/internal/profile/%s/preview", userID)

	var gotPath string
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Clone()
		if r.URL.Path != wantPath {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// dating-service's shared response envelope.
		fmt.Fprintf(w, `{"data":{"user_id":%q,"first_name":"Asha"}}`, userID)
	}))
	defer srv.Close()

	c := &datingClient{baseURL: srv.URL, internalKey: "test-internal-key", http: srv.Client()}
	got := c.getFirstName(context.Background(), userID)

	if gotPath != wantPath {
		t.Fatalf("preview path = %q, want %q", gotPath, wantPath)
	}
	if got != "Asha" {
		t.Fatalf("first name = %q, want the enveloped first_name", got)
	}
	if k := gotHeader.Get("X-Internal-Service-Key"); k != "test-internal-key" {
		t.Fatalf("X-Internal-Service-Key = %q", k)
	}
	for _, name := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
		if v := gotHeader.Get(name); v != "" {
			t.Fatalf("service call must not carry %s (got %q)", name, v)
		}
	}
}
