package eligibility

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestProfilesBatchReadsInChunksWithTheKeyAndNoUser(t *testing.T) {
	ids := make([]uuid.UUID, 0, 150)
	for i := 0; i < 150; i++ {
		ids = append(ids, uuid.New())
	}
	var calls []int
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/profiles/batch" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Internal-Service-Key") != "k" || r.Header.Get("X-User-Id") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var req struct {
			UserIDs []string `json:"user_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls = append(calls, len(req.UserIDs))
		out := map[string]any{}
		for i, id := range req.UserIDs {
			if i == 0 {
				out[id] = map[string]any{"username": nil, "display_name": "No Handle", "avatar_url": nil}
				continue
			}
			out[id] = map[string]any{"username": "u" + id[:4], "display_name": "D", "avatar_url": "https://cdn/" + id}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	got, err := NewProfiles(url, "k").Profiles(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != ProfileBatchMax || calls[1] != 50 {
		t.Fatalf("batch sizes = %v, want [100 50]", calls)
	}
	if len(got) != 150 {
		t.Fatalf("profiles = %d", len(got))
	}
	first := got[ids[0]]
	if first.Username != "" || first.AvatarURL != "" || first.DisplayName != "No Handle" {
		t.Fatalf("null fields = %+v", first)
	}
	if p := got[ids[1]]; p.Username == "" || p.AvatarURL == "" {
		t.Fatalf("profile = %+v", p)
	}
}

func TestProfilesBatchFailuresAreErrorsWithoutTheBody(t *testing.T) {
	for name, fn := range map[string]http.HandlerFunc{
		"5xx": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("secret-upstream-detail"))
		},
		// A non-200 whose body would decode is still a failure.
		"5xx with a map body": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{}`))
		},
		"malformed": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{secret-upstream-detail")) },
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://elsewhere.invalid/", http.StatusFound)
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewProfiles(serve(t, fn), "k").Profiles(context.Background(), []uuid.UUID{uuid.New()})
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.Contains(err.Error(), "secret-upstream-detail") {
				t.Fatalf("error quotes the body: %v", err)
			}
		})
	}
	if NewProfiles("  ", "k") != nil {
		t.Fatal("an empty URL must leave the source unset")
	}
}
