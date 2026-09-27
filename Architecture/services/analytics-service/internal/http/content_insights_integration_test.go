//go:build integration

package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/service"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MTube content insights and the widened creator route against the real
// tables (private analytics_it_apihttp database, see internal/testsupport).
//
// Same conventions as content_views_integration_test.go: settled days come
// from content_daily_summary, today from content_hourly_agg; an hourly row
// inside a settled day is not a second count. Retention and traffic come
// from finalized playback_sessions and their play_start events.
func TestContentInsightsFromTheRealTables(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.Pool(t, "apihttp")
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.content_hourly_agg, analytics.content_daily_summary,
		analytics.playback_sessions, analytics.events_raw, analytics.content_ownership CASCADE`); err != nil {
		t.Fatal(err)
	}
	owner, outsider, content := uuid.New(), uuid.New(), uuid.New()
	store := pgstore.New(pool)
	if err := store.UpsertContentOwnership(ctx, pgstore.ContentOwnership{
		ContentID: content, CreatorID: owner, ContentType: "long_video", CreatedAt: time.Now().Add(-48 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	// Two settled days, one hourly row inside a settled day (ignored), one
	// hourly row today (counted).
	exec(`INSERT INTO analytics.content_daily_summary (content_id, day_bucket, creator_id, content_type, views_display, watch_time_total_ms, unique_viewers)
	      VALUES ($1, $2, $3, 'long_video', 10, 100000, 8)`, content, today.AddDate(0, 0, -3), owner)
	exec(`INSERT INTO analytics.content_daily_summary (content_id, day_bucket, creator_id, content_type, views_display, watch_time_total_ms, unique_viewers)
	      VALUES ($1, $2, $3, 'long_video', 5, 50000, 4)`, content, today.AddDate(0, 0, -2), owner)
	exec(`INSERT INTO analytics.content_hourly_agg (content_id, hour_bucket, creator_id, content_type, views_display, watch_time_total_ms)
	      VALUES ($1, $2, $3, 'long_video', 7, 70000)`, content, today.AddDate(0, 0, -2).Add(10*time.Hour), owner)
	exec(`INSERT INTO analytics.content_hourly_agg (content_id, hour_bucket, creator_id, content_type, views_display, watch_time_total_ms, unique_viewers)
	      VALUES ($1, $2, $3, 'long_video', 3, 30000, 3)`, content, now.Truncate(time.Hour), owner)

	// Sessions: one half-watched, one fully watched, one self-view (ignored),
	// one open (ignored). play_start events name the surface.
	insertSession := func(actor uuid.UUID, coveredSeconds int, self bool, finalized bool, surface string) {
		t.Helper()
		session := uuid.New()
		bm := make([]byte, 13)
		for s := 0; s < coveredSeconds; s++ {
			bm[s/8] |= 1 << uint(s%8)
		}
		var finalizedAt *time.Time
		var reason *string
		if finalized {
			at, r := now.Add(-time.Minute), "play_end"
			finalizedAt, reason = &at, &r
		}
		exec(`INSERT INTO analytics.playback_sessions (actor_id, session_id, content_id, creator_id, content_type,
		        first_seen, last_seen, content_duration_ms, watched_ms, percent_covered, coverage,
		        is_self_view, finalized_at, finalize_reason, is_display_view)
		      VALUES ($1, $2, $3, $4, 'long_video', $5, $5, 100000, $6, $7, $8, $9, $10, $11, TRUE)`,
			actor, session, content, owner, now.Add(-2*time.Hour), coveredSeconds*1000, float64(coveredSeconds), bm, self, finalizedAt, reason)
		payload, _ := json.Marshal(map[string]any{
			"content_id": content.String(), "session_id": session.String(), "surface": surface, "event_name": "play_start",
		})
		exec(`INSERT INTO analytics.events_raw (id, user_id, session_id, type, payload, ts) VALUES ($1, $2, $3, 'play_start', $4, $5)`,
			uuid.New(), actor, session, payload, now.Add(-2*time.Hour))
	}
	insertSession(uuid.New(), 50, false, true, "feed")
	insertSession(uuid.New(), 100, false, true, "reels")
	insertSession(owner, 100, true, true, "feed")
	insertSession(uuid.New(), 100, false, false, "feed")

	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(nil, nil).
		WithCreatorService(service.NewCreatorService(pgstore.NewAggregateStore(pool))).
		WithAggregateStore(pgstore.NewAggregateStore(pool)).
		WithInternalKey("mtube-key").
		RegisterRoutes(router)
	get := func(path, user, role string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-User-Id", user)
		req.Header.Set("X-Internal-Service-Key", "mtube-key")
		if role != "" {
			req.Header.Set(adminRoleHeader, role)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	path := "/v1/analytics/content/" + content.String() + "?period=7d"
	rec := get(path, owner.String(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ViewsByDay []struct {
				Day   string `json:"day"`
				Views int64  `json:"views"`
			} `json:"views_by_day"`
			AvgMS      float64   `json:"average_view_duration_ms"`
			AvgPercent float64   `json:"average_percent_viewed"`
			Retention  []float64 `json:"retention"`
			Traffic    []struct {
				Surface string `json:"surface"`
				Views   int64  `json:"views"`
			} `json:"traffic"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if len(resp.Data.ViewsByDay) != 8 {
		t.Fatalf("views_by_day has %d days, want a dense 8 (7d + today): %s", len(resp.Data.ViewsByDay), rec.Body.String())
	}
	var total int64
	byDay := map[string]int64{}
	for _, d := range resp.Data.ViewsByDay {
		total += d.Views
		byDay[d.Day] = d.Views
	}
	if total != 18 || byDay[today.Format("2006-01-02")] != 3 || byDay[today.AddDate(0, 0, -2).Format("2006-01-02")] != 5 {
		t.Fatalf("views_by_day = %v (total %d), want 10+5 settled and 3 today", byDay, total)
	}
	if resp.Data.AvgMS != 75000 || resp.Data.AvgPercent != 75 {
		t.Fatalf("averages = %v ms %v%%, want 75000 / 75 over the two finalized non-self sessions", resp.Data.AvgMS, resp.Data.AvgPercent)
	}
	if len(resp.Data.Retention) != 100 || resp.Data.Retention[0] != 1 || resp.Data.Retention[99] != 0.5 {
		t.Fatalf("retention = %v", resp.Data.Retention)
	}
	if len(resp.Data.Traffic) != 2 || resp.Data.Traffic[0].Surface != "feed" || resp.Data.Traffic[0].Views != 1 || resp.Data.Traffic[1].Surface != "reels" || resp.Data.Traffic[1].Views != 1 {
		t.Fatalf("traffic = %+v", resp.Data.Traffic)
	}

	blocked := get(path, outsider.String(), "")
	unknown := get("/v1/analytics/content/"+uuid.New().String(), outsider.String(), "")
	if blocked.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound || blocked.Body.String() != unknown.Body.String() {
		t.Fatalf("non-enumeration mismatch blocked=%d %q unknown=%d %q", blocked.Code, blocked.Body.String(), unknown.Code, unknown.Body.String())
	}
	if rec := get(path, outsider.String(), "admin"); rec.Code != http.StatusOK {
		t.Fatalf("admin status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := get(path, outsider.String(), "moderator"); rec.Code != http.StatusNotFound {
		t.Fatalf("moderator status=%d", rec.Code)
	}

	// The widened creator route: display views over the period, the
	// realtime strip, and the old keys still present.
	rec = get("/v1/analytics/creator/me?period=28d", owner.String(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("creator/me status=%d body=%s", rec.Code, rec.Body.String())
	}
	var creator struct {
		Period         string `json:"period"`
		Views          int64  `json:"views"`
		TotalViews     int64  `json:"total_views"`
		WatchTimeMS    int64  `json:"watch_time_ms"`
		UniqueViewers  int64  `json:"unique_viewers"`
		FollowersDelta *int64 `json:"followers_delta"`
		TopContent     []struct {
			ContentID string `json:"content_id"`
			Views     int64  `json:"views"`
		} `json:"top_content"`
		Realtime struct {
			Views48h int64   `json:"views_48h"`
			Series   []int64 `json:"series_48h"`
		} `json:"realtime"`
		DataStatus string `json:"data_status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &creator); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if creator.Period != "28d" || creator.Views != 18 || creator.WatchTimeMS != 180000 || creator.UniqueViewers != 15 {
		t.Fatalf("creator totals = %+v, want views 18 watch 180000 uniques 15", creator)
	}
	if creator.TotalViews != 10 {
		t.Fatalf("total_views (hourly aggregate sum, the old key) = %d, want 10", creator.TotalViews)
	}
	if creator.FollowersDelta != nil || creator.DataStatus != "recorded" {
		t.Fatalf("followers_delta=%v data_status=%q", creator.FollowersDelta, creator.DataStatus)
	}
	if len(creator.TopContent) != 1 || creator.TopContent[0].ContentID != content.String() || creator.TopContent[0].Views != 18 {
		t.Fatalf("top_content = %+v", creator.TopContent)
	}
	if len(creator.Realtime.Series) != 48 || creator.Realtime.Views48h != 10 || creator.Realtime.Series[47] != 3 {
		t.Fatalf("realtime = %+v, want 48 buckets, 10 views (7 two days ago falls inside 48h + 3 now), 3 in the current hour", creator.Realtime)
	}
}
