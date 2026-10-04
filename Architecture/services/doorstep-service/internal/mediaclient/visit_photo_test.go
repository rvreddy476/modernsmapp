package mediaclient

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrepareVisitPhoto(t *testing.T) {
	id, owner := uuid.New(), uuid.New()
	status := 204
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/media/internal/"+id.String()+"/doorstep-photo" {
			t.Fatal("wrong route")
		}
		if r.Header.Get("X-Service-Authorization") != "Bearer scope-token" || r.Header.Get("X-Internal-Service-Key") != "key" || r.Header.Get("X-User-ID") != "" {
			t.Fatal("wrong credentials")
		}
		var body map[string]uuid.UUID
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["owner_id"] != owner {
			t.Fatal("wrong owner")
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := New(srv.URL, "key").WithPhotoScopeToken(func() (string, error) { return "scope-token", nil })
	if err := c.PrepareVisitPhoto(context.Background(), id, owner); err != nil {
		t.Fatal(err)
	}
	status = 404
	if err := c.PrepareVisitPhoto(context.Background(), id, owner); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	status = 403
	if err := c.PrepareVisitPhoto(context.Background(), id, owner); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
