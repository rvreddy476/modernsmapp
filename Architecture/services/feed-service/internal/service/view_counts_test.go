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

// Plan 5B (issue M-13): analytics-service being down, slow or wrong never
// fails a feed page. Every post still renders, with view_count 0, and a
// warning names the reason.
func TestFeedHydrationViewCountFailsOpenToZero(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	analytics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"INTERNAL_ERROR"}}`, http.StatusInternalServerError)
	}))
	defer analytics.Close()

	svc := &Service{
		analyticsServiceURL: analytics.URL,
		analyticsClient:     analytics.Client(),
	}
	posts := []HydratedPost{
		{ID: uuid.New(), ContentType: "flick"},
		{ID: uuid.New(), ContentType: "post"},
	}
	svc.enrichViewCounts(context.Background(), posts)
	for i, p := range posts {
		if p.ViewCount != 0 {
			t.Fatalf("post %d view_count=%d want 0 on analytics failure", i, p.ViewCount)
		}
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "view count") {
		t.Fatalf("no warning logged for the fail-open; logs=%q", logs.String())
	}

	// Unreachable rather than erroring: same outcome.
	logs.Reset()
	svc = &Service{
		analyticsServiceURL: "http://127.0.0.1:1",
		analyticsClient:     &http.Client{Timeout: time.Second},
	}
	svc.enrichViewCounts(context.Background(), posts)
	if posts[0].ViewCount != 0 || !strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("unreachable analytics: view_count=%d logs=%q", posts[0].ViewCount, logs.String())
	}
}

// The happy path: one batch call per page, the internal key forwarded,
// the numbers applied by id, and a repeat within 60 s served from cache.
func TestFeedHydrationViewCountComesFromAnalytics(t *testing.T) {
	t.Setenv("INTERNAL_SERVICE_KEY", "test-internal")
	a, b := uuid.New(), uuid.New()
	var calls atomic.Int32
	analytics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/analytics/internal/content-views" {
			t.Errorf("unexpected call %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Internal-Service-Key"); got != "test-internal" {
			t.Errorf("internal key=%q", got)
		}
		var req struct {
			ContentIDs []string `json:"content_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.ContentIDs) != 2 {
			t.Errorf("content_ids=%v err=%v", req.ContentIDs, err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			a.String(): map[string]any{"views_display": 120, "as_of": time.Now().UTC().Format(time.RFC3339)},
			b.String(): map[string]any{"views_display": 0, "as_of": time.Now().UTC().Format(time.RFC3339)},
		}})
	}))
	defer analytics.Close()

	svc := &Service{
		analyticsServiceURL: analytics.URL,
		analyticsClient:     analytics.Client(),
	}
	posts := []HydratedPost{{ID: a}, {ID: b}}
	svc.enrichViewCounts(context.Background(), posts)
	if posts[0].ViewCount != 120 || posts[1].ViewCount != 0 {
		t.Fatalf("view counts=%d,%d want 120,0", posts[0].ViewCount, posts[1].ViewCount)
	}
	again := []HydratedPost{{ID: a}, {ID: b}}
	svc.enrichViewCounts(context.Background(), again)
	if again[0].ViewCount != 120 {
		t.Fatalf("cached view count=%d want 120", again[0].ViewCount)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("analytics called %d times, want 1 (60 s cache)", n)
	}
}
