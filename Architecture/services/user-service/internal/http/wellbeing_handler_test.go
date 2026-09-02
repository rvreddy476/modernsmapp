package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// fakeWellbeingStore is an in-memory wellbeingStore so the handlers can be
// exercised without a database.
type fakeWellbeingStore struct {
	wellbeing map[uuid.UUID]*store.DigitalWellbeing
	screen    map[string]store.ScreenTimeLog // key: userID|YYYY-MM-DD
	upserts   int
}

func newFakeWellbeingStore() *fakeWellbeingStore {
	return &fakeWellbeingStore{
		wellbeing: map[uuid.UUID]*store.DigitalWellbeing{},
		screen:    map[string]store.ScreenTimeLog{},
	}
}

func (f *fakeWellbeingStore) GetWellbeing(_ context.Context, userID uuid.UUID) (*store.DigitalWellbeing, error) {
	if w, ok := f.wellbeing[userID]; ok {
		cp := *w
		return &cp, nil
	}
	// Mirrors store.Store.GetWellbeing's "no row" default.
	return &store.DigitalWellbeing{UserID: userID, NudgeIntervalMins: 30}, nil
}

func (f *fakeWellbeingStore) UpsertWellbeing(_ context.Context, w *store.DigitalWellbeing) error {
	cp := *w
	cp.UpdatedAt = time.Now().UTC()
	f.wellbeing[w.UserID] = &cp
	return nil
}

func (f *fakeWellbeingStore) UpsertScreenTimeLog(_ context.Context, userID uuid.UUID, date time.Time, minutes, sessions int) error {
	f.upserts++
	key := userID.String() + "|" + date.UTC().Format("2006-01-02")
	// REPLACE semantics: the client owns the day's total.
	f.screen[key] = store.ScreenTimeLog{
		ID: uuid.New(), UserID: userID, Date: date.UTC(),
		Minutes: minutes, SessionCount: sessions,
	}
	return nil
}

func (f *fakeWellbeingStore) GetScreenTimeBetween(_ context.Context, userID uuid.UUID, from, to time.Time) ([]store.ScreenTimeLog, error) {
	var out []store.ScreenTimeLog
	for d := utcDate(from); !d.After(utcDate(to)); d = d.AddDate(0, 0, 1) {
		if l, ok := f.screen[userID.String()+"|"+d.Format("2006-01-02")]; ok {
			out = append(out, l)
		}
	}
	return out, nil
}

const testUserID = "11111111-1111-4111-8111-111111111111"

// fixedNow keeps "today" stable inside a test: 2026-09-02 10:30 UTC.
var fixedNow = time.Date(2026, 9, 2, 10, 30, 0, 0, time.UTC)

func newWellbeingTestRouter(f *fakeWellbeingStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	wh := &wellbeingHandler{st: f, now: func() time.Time { return fixedNow }}
	r := gin.New()
	r.GET("/v1/users/me/wellbeing", wh.Get)
	r.PUT("/v1/users/me/wellbeing", wh.Update)
	r.POST("/v1/users/me/screen-time", wh.LogScreenTime)
	r.GET("/v1/users/me/screen-time", wh.GetScreenTime)
	return r
}

func doJSON(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", testUserID)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

// --- PUT /v1/users/me/wellbeing validation table ---

func TestUpdateWellbeingValidation(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode int
		wantErr  string // error code expected in the body for 400s
	}{
		{"valid full settings", `{"daily_limit_mins":60,"bedtime_start":"23:00","bedtime_end":"07:00","nudge_interval_mins":30}`, 200, ""},
		{"daily limit zero means off", `{"daily_limit_mins":0}`, 200, ""},
		{"daily limit absent means off", `{}`, 200, ""},
		{"daily limit at min", `{"daily_limit_mins":10}`, 200, ""},
		{"daily limit at max", `{"daily_limit_mins":1440}`, 200, ""},
		{"daily limit below min", `{"daily_limit_mins":5}`, 400, "INVALID_DAILY_LIMIT"},
		{"daily limit above max", `{"daily_limit_mins":1441}`, 400, "INVALID_DAILY_LIMIT"},
		{"daily limit negative", `{"daily_limit_mins":-1}`, 400, "INVALID_DAILY_LIMIT"},
		{"overnight sleep window ok", `{"bedtime_start":"23:00","bedtime_end":"07:00"}`, 200, ""},
		{"same-day sleep window ok", `{"bedtime_start":"13:00","bedtime_end":"15:30"}`, 200, ""},
		{"both bedtimes null is off", `{"bedtime_start":null,"bedtime_end":null}`, 200, ""},
		{"only start set", `{"bedtime_start":"23:00"}`, 400, "INVALID_SLEEP_HOURS"},
		{"only end set", `{"bedtime_end":"07:00"}`, 400, "INVALID_SLEEP_HOURS"},
		{"bad hour", `{"bedtime_start":"24:00","bedtime_end":"07:00"}`, 400, "INVALID_SLEEP_HOURS"},
		{"bad minute", `{"bedtime_start":"23:60","bedtime_end":"07:00"}`, 400, "INVALID_SLEEP_HOURS"},
		{"not a clock", `{"bedtime_start":"bed","bedtime_end":"07:00"}`, 400, "INVALID_SLEEP_HOURS"},
		{"start equals end", `{"bedtime_start":"22:00","bedtime_end":"22:00"}`, 400, "INVALID_SLEEP_HOURS"},
		{"nudge at bounds", `{"nudge_interval_mins":5}`, 200, ""},
		{"nudge below min", `{"nudge_interval_mins":4}`, 400, "INVALID_NUDGE_INTERVAL"},
		{"nudge above max", `{"nudge_interval_mins":241}`, 400, "INVALID_NUDGE_INTERVAL"},
		{"focus until in past", fmt.Sprintf(`{"focus_mode_until":%q}`, fixedNow.Add(-time.Hour).Format(time.RFC3339)), 400, "INVALID_FOCUS_MODE_UNTIL"},
		{"focus until in future", fmt.Sprintf(`{"focus_mode_until":%q}`, fixedNow.Add(time.Hour).Format(time.RFC3339)), 200, ""},
		{"detox until in past", fmt.Sprintf(`{"detox_mode_until":%q}`, fixedNow.Add(-time.Hour).Format(time.RFC3339)), 400, "INVALID_DETOX_MODE_UNTIL"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newWellbeingTestRouter(newFakeWellbeingStore())
			resp := doJSON(t, r, http.MethodPut, "/v1/users/me/wellbeing", tc.body)
			if resp.Code != tc.wantCode {
				t.Fatalf("status %d, want %d; body: %s", resp.Code, tc.wantCode, resp.Body.String())
			}
			if tc.wantErr != "" && !strings.Contains(resp.Body.String(), tc.wantErr) {
				t.Errorf("body missing error code %s: %s", tc.wantErr, resp.Body.String())
			}
		})
	}
}

// PUT then GET agree on one contract: HH:MM bedtimes, explicit nulls.
func TestWellbeingPutThenGetContract(t *testing.T) {
	f := newFakeWellbeingStore()
	r := newWellbeingTestRouter(f)

	put := doJSON(t, r, http.MethodPut, "/v1/users/me/wellbeing",
		`{"daily_limit_mins":60,"bedtime_start":"23:00","bedtime_end":"7:00"}`)
	if put.Code != 200 {
		t.Fatalf("PUT status %d: %s", put.Code, put.Body.String())
	}

	get := doJSON(t, r, http.MethodGet, "/v1/users/me/wellbeing", "")
	if get.Code != 200 {
		t.Fatalf("GET status %d: %s", get.Code, get.Body.String())
	}

	for _, resp := range []*httptest.ResponseRecorder{put, get} {
		var env struct {
			Data wellbeingView `json:"data"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal: %v; body: %s", err, resp.Body.String())
		}
		d := env.Data
		if d.DailyLimitMins == nil || *d.DailyLimitMins != 60 {
			t.Errorf("daily_limit_mins = %v, want 60", d.DailyLimitMins)
		}
		if d.BedtimeStart == nil || *d.BedtimeStart != "23:00" {
			t.Errorf("bedtime_start = %v, want 23:00", d.BedtimeStart)
		}
		// "7:00" must be canonicalized to "07:00".
		if d.BedtimeEnd == nil || *d.BedtimeEnd != "07:00" {
			t.Errorf("bedtime_end = %v, want 07:00", d.BedtimeEnd)
		}
		if d.NudgeIntervalMins != 30 {
			t.Errorf("nudge_interval_mins = %d, want default 30", d.NudgeIntervalMins)
		}
	}

	// Turning the limit off with 0 stores null.
	off := doJSON(t, r, http.MethodPut, "/v1/users/me/wellbeing", `{"daily_limit_mins":0}`)
	if off.Code != 200 {
		t.Fatalf("PUT off status %d: %s", off.Code, off.Body.String())
	}
	if !strings.Contains(off.Body.String(), `"daily_limit_mins":null`) {
		t.Errorf("limit 0 should read back as null: %s", off.Body.String())
	}
}

// --- POST /v1/users/me/screen-time ---

func TestLogScreenTimeUpsertIsIdempotent(t *testing.T) {
	f := newFakeWellbeingStore()
	r := newWellbeingTestRouter(f)

	// 3599 secs -> ceil to 60 minutes.
	first := doJSON(t, r, http.MethodPost, "/v1/users/me/screen-time",
		`{"date":"2026-09-02","foreground_secs":3599,"sessions":3}`)
	if first.Code != 200 {
		t.Fatalf("POST status %d: %s", first.Code, first.Body.String())
	}
	if !strings.Contains(first.Body.String(), `"minutes":60`) {
		t.Errorf("expected ceil(3599/60)=60: %s", first.Body.String())
	}

	// Same day again with a bigger total: replaced, not summed.
	second := doJSON(t, r, http.MethodPost, "/v1/users/me/screen-time",
		`{"date":"2026-09-02","foreground_secs":7200,"sessions":5}`)
	if second.Code != 200 {
		t.Fatalf("POST status %d: %s", second.Code, second.Body.String())
	}

	key := testUserID + "|2026-09-02"
	row, ok := f.screen[key]
	if !ok {
		t.Fatalf("no row stored for %s", key)
	}
	if row.Minutes != 120 || row.SessionCount != 5 {
		t.Errorf("row = %d mins / %d sessions, want 120/5 (replace, not add)", row.Minutes, row.SessionCount)
	}
	if len(f.screen) != 1 {
		t.Errorf("expected one row for the day, got %d", len(f.screen))
	}
}

func TestLogScreenTimeValidation(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode int
		wantErr  string
	}{
		{"date defaults to today", `{"foreground_secs":600}`, 200, ""},
		{"legacy minutes body", `{"minutes":45,"sessions":2}`, 200, ""},
		{"neither field", `{"sessions":2}`, 400, "INVALID_SCREEN_TIME"},
		{"negative secs", `{"foreground_secs":-1}`, 400, "INVALID_SCREEN_TIME"},
		{"secs above a day", `{"foreground_secs":86401}`, 400, "INVALID_SCREEN_TIME"},
		{"minutes above a day", `{"minutes":1441}`, 400, "INVALID_SCREEN_TIME"},
		{"bad date", `{"date":"02-09-2026","foreground_secs":60}`, 400, "INVALID_DATE"},
		{"future date", `{"date":"2026-09-03","foreground_secs":60}`, 400, "INVALID_DATE"},
		{"too old", `{"date":"2026-07-01","foreground_secs":60}`, 400, "INVALID_DATE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newWellbeingTestRouter(newFakeWellbeingStore())
			resp := doJSON(t, r, http.MethodPost, "/v1/users/me/screen-time", tc.body)
			if resp.Code != tc.wantCode {
				t.Fatalf("status %d, want %d; body: %s", resp.Code, tc.wantCode, resp.Body.String())
			}
			if tc.wantErr != "" && !strings.Contains(resp.Body.String(), tc.wantErr) {
				t.Errorf("body missing %s: %s", tc.wantErr, resp.Body.String())
			}
		})
	}
}

// --- GET /v1/users/me/screen-time?range=week ---

func TestGetScreenTimeWeekAggregation(t *testing.T) {
	f := newFakeWellbeingStore()
	r := newWellbeingTestRouter(f)

	// Limit set so the response carries it.
	if resp := doJSON(t, r, http.MethodPut, "/v1/users/me/wellbeing", `{"daily_limit_mins":60}`); resp.Code != 200 {
		t.Fatalf("PUT wellbeing: %d %s", resp.Code, resp.Body.String())
	}
	// Two logged days inside the window, one outside it.
	for _, p := range []string{
		`{"date":"2026-09-02","foreground_secs":3600,"sessions":4}`, // today, 60m
		`{"date":"2026-08-30","foreground_secs":1800,"sessions":2}`, // 30m
		`{"date":"2026-08-25","foreground_secs":6000,"sessions":9}`, // outside the 7-day window
	} {
		if resp := doJSON(t, r, http.MethodPost, "/v1/users/me/screen-time", p); resp.Code != 200 {
			t.Fatalf("POST %s: %d %s", p, resp.Code, resp.Body.String())
		}
	}

	resp := doJSON(t, r, http.MethodGet, "/v1/users/me/screen-time?range=week", "")
	if resp.Code != 200 {
		t.Fatalf("GET status %d: %s", resp.Code, resp.Body.String())
	}
	var env struct {
		Data screenTimeView `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v; body: %s", err, resp.Body.String())
	}
	d := env.Data

	if len(d.Days) != 7 {
		t.Fatalf("week returned %d days, want 7", len(d.Days))
	}
	if d.Days[0].Date != "2026-08-27" || d.Days[6].Date != "2026-09-02" {
		t.Errorf("window = %s..%s, want 2026-08-27..2026-09-02 ascending", d.Days[0].Date, d.Days[6].Date)
	}
	byDate := map[string]screenTimeDay{}
	for _, day := range d.Days {
		byDate[day.Date] = day
	}
	if got := byDate["2026-08-30"]; got.Minutes != 30 || got.Sessions != 2 {
		t.Errorf("2026-08-30 = %+v, want 30 mins / 2 sessions", got)
	}
	if got := byDate["2026-08-28"]; got.Minutes != 0 || got.Sessions != 0 {
		t.Errorf("missing day should be zero-filled, got %+v", got)
	}
	if d.TodayMinutes != 60 {
		t.Errorf("today_minutes = %d, want 60", d.TodayMinutes)
	}
	if d.DailyLimitMins == nil || *d.DailyLimitMins != 60 {
		t.Errorf("daily_limit_mins = %v, want 60", d.DailyLimitMins)
	}
}

func TestGetScreenTimeTodayAndBadRange(t *testing.T) {
	f := newFakeWellbeingStore()
	r := newWellbeingTestRouter(f)
	if resp := doJSON(t, r, http.MethodPost, "/v1/users/me/screen-time", `{"foreground_secs":600,"sessions":1}`); resp.Code != 200 {
		t.Fatalf("POST: %d %s", resp.Code, resp.Body.String())
	}

	resp := doJSON(t, r, http.MethodGet, "/v1/users/me/screen-time", "")
	if resp.Code != 200 {
		t.Fatalf("GET today status %d: %s", resp.Code, resp.Body.String())
	}
	var env struct {
		Data screenTimeView `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Data.Days) != 1 || env.Data.Days[0].Date != "2026-09-02" || env.Data.TodayMinutes != 10 {
		t.Errorf("today view wrong: %+v", env.Data)
	}

	bad := doJSON(t, r, http.MethodGet, "/v1/users/me/screen-time?range=month", "")
	if bad.Code != 400 || !strings.Contains(bad.Body.String(), "INVALID_RANGE") {
		t.Errorf("bad range: %d %s", bad.Code, bad.Body.String())
	}
}
