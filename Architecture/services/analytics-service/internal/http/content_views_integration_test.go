//go:build integration

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Plan 5B (issue M-13): the view count a post shows comes from the same
// foundation the creator is paid from — content_daily_summary for every
// rolled-up day, plus today's hourly buckets, which the rollup has not
// folded yet. An hourly row for a day that already has a daily row is
// the rollup's input, not a second count.
func TestContentViewsBatchSumsDailyAndOpenHours(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.Pool(t, "apihttp")
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.content_hourly_agg, analytics.content_daily_summary`); err != nil {
		t.Fatal(err)
	}
	creator := uuid.New()
	rolledUp, todayOnly, unknown := uuid.New(), uuid.New(), uuid.New()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	insertDaily := func(content uuid.UUID, day time.Time, views int64) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO analytics.content_daily_summary
				(content_id, day_bucket, creator_id, content_type, views_display)
			VALUES ($1, $2, $3, 'flick', $4)`, content, day, creator, views); err != nil {
			t.Fatal(err)
		}
	}
	insertHourly := func(content uuid.UUID, hour time.Time, views int64) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO analytics.content_hourly_agg
				(content_id, hour_bucket, creator_id, content_type, views_display)
			VALUES ($1, $2, $3, 'flick', $4)`, content, hour, creator, views); err != nil {
			t.Fatal(err)
		}
	}
	// rolledUp: two settled days, one hourly row inside a settled day
	// (must not double count), one hourly row today (must count).
	insertDaily(rolledUp, today.AddDate(0, 0, -3), 10)
	insertDaily(rolledUp, today.AddDate(0, 0, -2), 5)
	insertHourly(rolledUp, today.AddDate(0, 0, -2).Add(10*time.Hour), 7)
	insertHourly(rolledUp, today.Add(time.Duration(time.Now().UTC().Hour())*time.Hour), 3)
	// todayOnly: an old hourly row with no daily row is not "open" — the
	// rollup decided that day (eligibility, cap) and its answer is the
	// daily table, which has nothing. Only today's hours are still open.
	insertHourly(todayOnly, today.AddDate(0, 0, -5), 9)
	insertHourly(todayOnly, today, 4)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(nil, nil).
		WithAggregateStore(pgstore.NewAggregateStore(pool)).
		WithInternalKey("m13-key").
		RegisterRoutes(router)

	call := func(ids []string, key string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"content_ids": ids})
		req := httptest.NewRequest(http.MethodPost, "/v1/analytics/internal/content-views", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Service-Key", key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	got := call([]string{rolledUp.String(), todayOnly.String(), unknown.String()}, "m13-key")
	if got.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
	var resp struct {
		Data map[string]struct {
			ViewsDisplay int64  `json:"views_display"`
			AsOf         string `json:"as_of"`
		} `json:"data"`
		Freshness struct {
			Window string `json:"window"`
		} `json:"freshness"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s: %v", got.Body.String(), err)
	}
	want := map[uuid.UUID]int64{rolledUp: 18, todayOnly: 4, unknown: 0}
	for id, views := range want {
		entry, ok := resp.Data[id.String()]
		if !ok {
			t.Fatalf("content %s missing from response %s", id, got.Body.String())
		}
		if entry.ViewsDisplay != views {
			t.Fatalf("content %s views_display=%d want %d (body %s)", id, entry.ViewsDisplay, views, got.Body.String())
		}
		asOf, err := time.Parse(time.RFC3339, entry.AsOf)
		if err != nil || time.Since(asOf) > time.Minute {
			t.Fatalf("content %s as_of=%q is not a recent RFC3339 timestamp (%v)", id, entry.AsOf, err)
		}
	}
	if resp.Freshness.Window == "" {
		t.Fatalf("response documents no freshness window: %s", got.Body.String())
	}

	if got := call(nil, "wrong"); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong internal key status=%d", got.Code)
	}
	tooMany := make([]string, 201)
	for i := range tooMany {
		tooMany[i] = uuid.New().String()
	}
	if got := call(tooMany, "m13-key"); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "200") {
		t.Fatalf("201 ids status=%d body=%s, want 400 naming the limit", got.Code, got.Body.String())
	}
	if got := call([]string{"not-a-uuid"}, "m13-key"); got.Code != http.StatusBadRequest {
		t.Fatalf("bad id status=%d body=%s", got.Code, got.Body.String())
	}
}
