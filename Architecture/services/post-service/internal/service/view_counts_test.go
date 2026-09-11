package service

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newViewCountTestService(analyticsURL string) *Service {
	return &Service{
		httpClient:          &http.Client{Timeout: 2 * time.Second},
		analyticsServiceURL: analyticsURL,
		internalServiceKey:  "test-internal-key",
	}
}

// Plan 5B (issue M-13): the view count on a post is analytics-service's
// display view — capped, self-view-excluded, the number the creator is
// paid on — read through the internal batch route. The Redis hash it
// used to read was written by nothing.
func TestPostViewCountComesFromAnalytics(t *testing.T) {
	postID := uuid.New()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/analytics/internal/content-views" || r.Method != http.MethodPost {
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Internal-Service-Key"); got != "test-internal-key" {
			t.Errorf("X-Internal-Service-Key=%q", got)
		}
		var req struct {
			ContentIDs []string `json:"content_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.ContentIDs) != 1 || req.ContentIDs[0] != postID.String() {
			t.Errorf("request body content_ids=%v err=%v", req.ContentIDs, err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				postID.String(): map[string]any{"views_display": 4231, "as_of": time.Now().UTC().Format(time.RFC3339)},
			},
			"freshness": map[string]any{"window": "test"},
		})
	}))
	defer srv.Close()

	s := newViewCountTestService(srv.URL)
	if got := s.getViewCount(context.Background(), postID); got != 4231 {
		t.Fatalf("getViewCount=%d want 4231", got)
	}
	// Second read inside the 60 s window is served from the in-process
	// cache: one upstream call, not two.
	if got := s.getViewCount(context.Background(), postID); got != 4231 {
		t.Fatalf("cached getViewCount=%d want 4231", got)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("analytics called %d times, want 1 (60 s cache)", n)
	}
}

// Fail-open: analytics being down never fails a post read. The count is
// zero and a warning says why.
func TestPostViewCountFailsOpenToZero(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newViewCountTestService(srv.URL)
	if got := s.getViewCount(context.Background(), uuid.New()); got != 0 {
		t.Fatalf("getViewCount on a 500=%d want 0", got)
	}
	// Unreachable, not just erroring.
	s = newViewCountTestService("http://127.0.0.1:1")
	if got := s.getViewCount(context.Background(), uuid.New()); got != 0 {
		t.Fatalf("getViewCount on an unreachable analytics=%d want 0", got)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "view count") {
		t.Fatalf("no warning logged for the fail-open; logs=%q", logs.String())
	}
}
