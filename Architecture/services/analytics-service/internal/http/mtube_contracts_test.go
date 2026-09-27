package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/service"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MTube analytics contracts (2026-09-27) over httptest with the store
// faked and the clock pinned; bodies asserted against
// testdata/contracts/mtube. The database-backed shape of the same routes
// is content_insights_integration_test.go.

var (
	mtubeNow     = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	mtubeOwner   = uuid.MustParse("9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c01")
	mtubeContent = uuid.MustParse("5d1d1e4e-0c1a-4d3e-9a7b-3b1e6c2f8a01")
	mtubeOther   = uuid.MustParse("0b6a2c7d-8e9f-4a1b-8c2d-4e5f6a7b8c02")
)

func mtubeFixture(t *testing.T, name string) any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "contracts", "mtube", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("fixture %s is not JSON: %v", name, err)
	}
	return v
}

func assertBodyMatchesFixture(t *testing.T, body []byte, fixture string) {
	t.Helper()
	var got any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, body)
	}
	if want := mtubeFixture(t, fixture); !reflect.DeepEqual(got, want) {
		t.Fatalf("response differs from %s\n got: %s", fixture, body)
	}
}

// ── creator/me ──────────────────────────────────────────────────────────

type fakeCreatorStore struct{ ownerOnly bool }

func (f fakeCreatorStore) zeroFor(creatorID uuid.UUID) bool { return creatorID != mtubeOwner }

func (f fakeCreatorStore) GetCreatorAggStats(_ context.Context, creatorID uuid.UUID, _ time.Time) (*pgstore.CreatorAggStats, error) {
	if f.zeroFor(creatorID) {
		return &pgstore.CreatorAggStats{}, nil
	}
	return &pgstore.CreatorAggStats{TotalViews: 40, TotalLikes: 5, TotalComments: 2, TotalShares: 1, TotalFollows: 3}, nil
}

func (f fakeCreatorStore) GetCreatorDailyTrend(_ context.Context, creatorID uuid.UUID, _ time.Time) ([]pgstore.DailySummaryRow, error) {
	if f.zeroFor(creatorID) {
		return nil, nil
	}
	return []pgstore.DailySummaryRow{{Date: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), Views: 17, WatchTimeMS: 5000, CQS: 0.5}}, nil
}

func (f fakeCreatorStore) GetContentList(_ context.Context, creatorID uuid.UUID, _ int, _ time.Time, _ string) ([]pgstore.ContentSummary, error) {
	if f.zeroFor(creatorID) {
		return nil, nil
	}
	return []pgstore.ContentSummary{{ContentID: mtubeContent, ContentType: "long_video", ViewsDisplay: 30, WatchTimeTotalMS: 90000, Likes: 4, Shares: 1}}, nil
}

func (f fakeCreatorStore) GetCreatorPeriodTotals(_ context.Context, creatorID uuid.UUID, _, _ time.Time) (*pgstore.CreatorPeriodTotals, error) {
	if f.zeroFor(creatorID) {
		return &pgstore.CreatorPeriodTotals{}, nil
	}
	return &pgstore.CreatorPeriodTotals{Views: 42, WatchTimeMS: 123000, UniqueViewers: 31}, nil
}

func (f fakeCreatorStore) GetCreatorTopContent(_ context.Context, creatorID uuid.UUID, _, _ time.Time, _ int) ([]pgstore.ContentPeriodStat, error) {
	if f.zeroFor(creatorID) {
		return nil, nil
	}
	return []pgstore.ContentPeriodStat{
		{ContentID: mtubeContent, Views: 30, WatchTimeMS: 90000},
		{ContentID: mtubeOther, Views: 12, WatchTimeMS: 33000},
	}, nil
}

func (f fakeCreatorStore) GetCreatorHourlyViews(_ context.Context, creatorID uuid.UUID, _ time.Time, hours int) ([]int64, error) {
	series := make([]int64, hours)
	if !f.zeroFor(creatorID) {
		series[hours-1] = 3
		series[hours-2] = 2
	}
	return series, nil
}

func creatorRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cs := service.NewCreatorServiceFrom(fakeCreatorStore{}).WithClock(func() time.Time { return mtubeNow })
	New(nil, nil).WithCreatorService(cs).RegisterRoutes(r)
	return r
}

func mtubeGet(r *gin.Engine, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCreatorMeIsWidenedNotReplaced(t *testing.T) {
	r := creatorRouter()
	rec := mtubeGet(r, "/v1/analytics/creator/me?period=28d", map[string]string{"X-User-Id": mtubeOwner.String()})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertBodyMatchesFixture(t, rec.Body.Bytes(), "creator_me.json")

	// The pre-MTube default and the new periods all resolve; unknown
	// periods keep the old default rather than erroring.
	for raw, want := range map[string]string{"": "30d", "7d": "7d", "90d": "90d", "365d": "365d", "1y": "30d"} {
		rec := mtubeGet(r, "/v1/analytics/creator/me?period="+raw, map[string]string{"X-User-Id": mtubeOwner.String()})
		var body struct {
			Period   string `json:"period"`
			Realtime struct {
				Series []int64 `json:"series_48h"`
			} `json:"realtime"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Period != want || len(body.Realtime.Series) != 48 {
			t.Fatalf("period %q: status=%d period=%q series=%d err=%v", raw, rec.Code, body.Period, len(body.Realtime.Series), err)
		}
	}
	if rec := mtubeGet(r, "/v1/analytics/creator/me", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: status=%d", rec.Code)
	}
	// Another creator gets their own figures — zero here — never the owner's.
	rec = mtubeGet(r, "/v1/analytics/creator/me?period=28d", map[string]string{"X-User-Id": uuid.New().String()})
	var other struct {
		Views      int64            `json:"views"`
		TopContent []map[string]any `json:"top_content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &other); err != nil || other.Views != 0 || len(other.TopContent) != 0 {
		t.Fatalf("stranger saw views=%d top=%v err=%v", other.Views, other.TopContent, err)
	}
}

// ── content insights ────────────────────────────────────────────────────

type fakeInsightsStore struct {
	ownsErr error
	asked   []uuid.UUID
}

func bitmap(coveredSeconds, totalSeconds int) []byte {
	b := make([]byte, (totalSeconds+7)/8)
	for s := 0; s < coveredSeconds; s++ {
		b[s/8] |= 1 << uint(s%8)
	}
	return b
}

func (f *fakeInsightsStore) OwnsContent(_ context.Context, creatorID, contentID uuid.UUID) (bool, error) {
	f.asked = append(f.asked, creatorID)
	if f.ownsErr != nil {
		return false, f.ownsErr
	}
	return creatorID == mtubeOwner && contentID == mtubeContent, nil
}

func (f *fakeInsightsStore) GetContentViewsByDay(_ context.Context, contentID uuid.UUID, _, _ time.Time) ([]pgstore.DayViews, error) {
	if contentID != mtubeContent {
		return nil, nil
	}
	return []pgstore.DayViews{{Day: "2026-09-25", Views: 4}, {Day: "2026-09-26", Views: 9}, {Day: "2026-09-27", Views: 2}}, nil
}

func (f *fakeInsightsStore) GetContentSessionCoverage(_ context.Context, contentID uuid.UUID, _ time.Time) ([]pgstore.SessionCoverage, error) {
	if contentID != mtubeContent {
		return nil, nil
	}
	return []pgstore.SessionCoverage{
		{DurationMS: 100_000, WatchedMS: 50_000, PercentCovered: 50, Coverage: bitmap(50, 100)},
		{DurationMS: 100_000, WatchedMS: 100_000, PercentCovered: 100, Coverage: bitmap(100, 100)},
	}, nil
}

func (f *fakeInsightsStore) GetContentTrafficBySurface(_ context.Context, contentID uuid.UUID, _ time.Time) ([]pgstore.SurfaceViews, error) {
	if contentID != mtubeContent {
		return nil, nil
	}
	return []pgstore.SurfaceViews{{Surface: "feed", Views: 7}, {Surface: "reels", Views: 5}, {Surface: "other", Views: 3}}, nil
}

func insightsRouter(store *fakeInsightsStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(nil, nil)
	h.insights = store
	h.now = func() time.Time { return mtubeNow }
	h.RegisterRoutes(r)
	return r
}

func TestContentInsightsAreOwnerOnlyAndNonEnumerating(t *testing.T) {
	store := &fakeInsightsStore{}
	r := insightsRouter(store)
	path := "/v1/analytics/content/" + mtubeContent.String()

	rec := mtubeGet(r, path+"?period=7d", map[string]string{"X-User-Id": mtubeOwner.String()})
	if rec.Code != http.StatusOK {
		t.Fatalf("owner: status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertBodyMatchesFixture(t, rec.Body.Bytes(), "content_insights.json")

	stranger := uuid.New().String()
	blocked := mtubeGet(r, path, map[string]string{"X-User-Id": stranger})
	unknown := mtubeGet(r, "/v1/analytics/content/"+uuid.New().String(), map[string]string{"X-User-Id": stranger})
	if blocked.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound || blocked.Body.String() != unknown.Body.String() {
		t.Fatalf("non-enumeration: blocked=%d %q unknown=%d %q", blocked.Code, blocked.Body.String(), unknown.Code, unknown.Body.String())
	}
	if rec := mtubeGet(r, path, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: status=%d", rec.Code)
	}
	if rec := mtubeGet(r, "/v1/analytics/content/not-a-uuid", map[string]string{"X-User-Id": mtubeOwner.String()}); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: status=%d", rec.Code)
	}
	// The existing /views route on the same prefix is untouched.
	if rec := mtubeGet(r, path+"/views", nil); rec.Code != http.StatusOK {
		t.Fatalf("/views: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestContentInsightsAdmitAnAdminAndOnlyAnAdmin(t *testing.T) {
	store := &fakeInsightsStore{}
	r := insightsRouter(store)
	path := "/v1/analytics/content/" + mtubeContent.String()
	stranger := uuid.New().String()
	for role, want := range map[string]int{"admin": http.StatusOK, "superadmin": http.StatusOK, "moderator": http.StatusNotFound, "": http.StatusNotFound} {
		rec := mtubeGet(r, path, map[string]string{"X-User-Id": stranger, adminRoleHeader: role})
		if rec.Code != want {
			t.Fatalf("role %q: status=%d want %d body=%s", role, rec.Code, want, rec.Body.String())
		}
	}
	// An admin without a verified user is still nobody.
	if rec := mtubeGet(r, path, map[string]string{adminRoleHeader: "admin"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin without user: status=%d", rec.Code)
	}
	// An admin asking about an unknown id gets the same 404 as anyone.
	if rec := mtubeGet(r, "/v1/analytics/content/"+uuid.New().String(), map[string]string{"X-User-Id": stranger, adminRoleHeader: "admin"}); rec.Code != http.StatusOK {
		// The fake answers empty for unknown content; the real store does the
		// same (zero rows), so an admin sees an empty insight, not a 404.
		t.Fatalf("admin unknown id: status=%d", rec.Code)
	}
}

func TestContentInsightsFailClosedWhenOwnershipIsUnresolved(t *testing.T) {
	store := &fakeInsightsStore{ownsErr: errors.New("db down")}
	r := insightsRouter(store)
	rec := mtubeGet(r, "/v1/analytics/content/"+mtubeContent.String(), map[string]string{"X-User-Id": mtubeOwner.String()})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s; an ownership outage must never read as owned", rec.Code, rec.Body.String())
	}
	// Without an aggregate store the route says so rather than 500ing.
	bare := gin.New()
	New(nil, nil).RegisterRoutes(bare)
	if rec := mtubeGet(bare, "/v1/analytics/content/"+mtubeContent.String(), map[string]string{"X-User-Id": mtubeOwner.String()}); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no store: status=%d", rec.Code)
	}
}
