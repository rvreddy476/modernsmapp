package ownedmedia

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOwnedPrivateReadyImage(t *testing.T) {
	owner, id := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		valid  bool
	}{
		{"own private ready", func(map[string]any) {}, true},
		{"foreign owner", func(m map[string]any) { m["uploader_id"] = uuid.New() }, false},
		{"wrong id", func(m map[string]any) { m["id"] = uuid.New() }, false},
		{"not ready", func(m map[string]any) { m["processing_status"] = "processing" }, false},
		{"not moderated", func(m map[string]any) { m["moderation_status"] = "pending" }, false},
		{"rejected", func(m map[string]any) { m["moderation_status"] = "rejected" }, false},
		{"video", func(m map[string]any) { m["file_type"] = "video" }, false},
		{"public object", func(m map[string]any) { m["storage_key"] = "public/identity.jpg" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Internal-Service-Key") != "test-key" || r.Header.Get("X-User-Id") != "" || r.URL.Path != "/v1/media/"+id.String() {
					t.Error("caller contract changed")
				}
				data := map[string]any{"id": id, "uploader_id": owner, "file_type": "image", "processing_status": "ready", "moderation_status": "passed", "storage_key": "protected/identity.jpg"}
				tc.change(data)
				if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			err := New(srv.URL, "test-key").VerifyOwnedImage(context.Background(), owner, id)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected outcome: %v", err)
			}
		})
	}
}
func TestUnavailableAndRedirectFailClosed(t *testing.T) {
	owner, id := uuid.New(), uuid.New()
	var c *Client
	if !errors.Is(c.VerifyOwnedImage(context.Background(), owner, id), ErrUnavailable) {
		t.Fatal("nil client accepted")
	}
	if !errors.Is(New("http://unused", "").VerifyOwnedImage(context.Background(), owner, id), ErrUnavailable) {
		t.Fatal("missing key accepted")
	}
	targetHit := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetHit = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	if !errors.Is(New(redirect.URL, "test-key").VerifyOwnedImage(context.Background(), owner, id), ErrUnavailable) || targetHit {
		t.Fatal("credential-bearing redirect followed")
	}
}
