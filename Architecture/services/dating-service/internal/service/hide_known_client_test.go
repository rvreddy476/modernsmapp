package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Mechanic M16: the connections list is read page by page from
// graph-service's internal route, with the internal key and no identity.
func TestHTTPConnectionLister(t *testing.T) {
	user := uuid.New()
	all := make([]uuid.UUID, connectionsPageSize+3)
	for i := range all {
		all[i] = uuid.New()
	}
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(internalKeyHeader) != "k" || r.Header.Get("X-User-Id") != "" {
			t.Errorf("headers: key %q user %q", r.Header.Get(internalKeyHeader), r.Header.Get("X-User-Id"))
		}
		if r.URL.Path != "/v1/graph/connections/"+user.String() {
			t.Errorf("path %s", r.URL.Path)
		}
		pages++
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		end := offset + limit
		if end > len(all) {
			end = len(all)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": all[offset:end]})
	}))
	defer srv.Close()
	got, err := NewHTTPConnectionLister(srv.URL, "k", &http.Client{Timeout: 2 * time.Second}).AcceptedConnections(context.Background(), user)
	if err != nil || len(got) != len(all) || pages != 2 {
		t.Fatalf("got %d connections in %d pages (err %v), want %d in 2", len(got), pages, err, len(all))
	}
}

func TestHTTPConnectionListerFailsOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := NewHTTPConnectionLister(srv.URL, "", nil).AcceptedConnections(context.Background(), uuid.New()); err == nil {
		t.Fatal("a 401 was read as no connections")
	}
}
