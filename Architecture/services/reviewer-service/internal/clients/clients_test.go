package clients

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// IsRelated must treat a block in either direction as a tie. On graph-service's
// single-pair relationship route "blocked" is target→viewer and "blocked_by" is
// viewer→target.
func TestIsRelatedSeesABlockInEitherDirection(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"no tie", `{"data":{"connection_status":"none"}}`, false},
		{"creator blocked reviewer", `{"data":{"blocked":true,"connection_status":"none"}}`, true},
		{"reviewer blocked creator", `{"data":{"blocked_by":true,"connection_status":"none"}}`, true},
		{"follow", `{"data":{"follows":true,"connection_status":"none"}}`, true},
		{"pending connection", `{"data":{"connection_status":"pending"}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/graph/relationship" {
					t.Errorf("path = %s", r.URL.Path)
				}
				if r.Header.Get("X-Internal-Service-Key") != "k" {
					t.Errorf("internal key not sent")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := New(srv.URL, "", "", "", "k")
			got, err := c.IsRelated(context.Background(), uuid.New(), uuid.New())
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("related = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsRelatedFailsClosedOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	got, err := New(srv.URL, "", "", "", "k").IsRelated(context.Background(), uuid.New(), uuid.New())
	if err == nil || !got {
		t.Fatalf("want related=true with an error, got %v, %v", got, err)
	}
}
