package http

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/user-service/internal/store"
	"github.com/google/uuid"
)

// Digital wellbeing — the TikTok-style "Screen time" settings.
//
// JSON contract (shared by GET and PUT /v1/users/me/wellbeing):
//
//	{
//	  "daily_limit_mins":    60 | null,        // null = off; 10..1440 when set
//	  "bedtime_start":       "23:00" | null,   // both null = sleep hours off
//	  "bedtime_end":         "07:00" | null,   // overnight windows allowed
//	  "focus_mode_enabled":  false,
//	  "focus_mode_until":    RFC3339 | null,
//	  "nudge_interval_mins": 30,               // 5..240
//	  "hide_like_counts":    false,
//	  "detox_mode_until":    RFC3339 | null,
//	  "updated_at":          RFC3339 | null    // null until the first PUT
//	}
//
// PUT is a full replace of the row (this was the existing behaviour, kept on
// purpose: the client holds the whole settings object it got from GET and
// sends it back). Omitted fields fall back to their "off"/default values:
// daily_limit_mins null, bedtime null/null, focus off, nudge 30, likes shown,
// detox null. A daily_limit_mins of 0 is accepted as "off" and stored as null.

const (
	minDailyLimitMins = 10
	maxDailyLimitMins = 1440
	minNudgeMins      = 5
	maxNudgeMins      = 240
	defaultNudgeMins  = 30
)

// wellbeingError is a 400-class validation failure with a stable code.
type wellbeingError struct {
	Code    string
	Message string
}

func (e *wellbeingError) Error() string { return e.Code + ": " + e.Message }

// updateWellbeingRequest uses pointers so "absent" is distinguishable from
// the zero value.
type updateWellbeingRequest struct {
	DailyLimitMins    *int       `json:"daily_limit_mins"`
	BedtimeStart      *string    `json:"bedtime_start"`
	BedtimeEnd        *string    `json:"bedtime_end"`
	FocusModeEnabled  *bool      `json:"focus_mode_enabled"`
	FocusModeUntil    *time.Time `json:"focus_mode_until"`
	NudgeIntervalMins *int       `json:"nudge_interval_mins"`
	HideLikeCounts    *bool      `json:"hide_like_counts"`
	DetoxModeUntil    *time.Time `json:"detox_mode_until"`
}

// wellbeingView is the wire shape for GET and PUT responses. No omitempty:
// "off" is an explicit null the client can rely on.
type wellbeingView struct {
	DailyLimitMins    *int       `json:"daily_limit_mins"`
	BedtimeStart      *string    `json:"bedtime_start"`
	BedtimeEnd        *string    `json:"bedtime_end"`
	FocusModeEnabled  bool       `json:"focus_mode_enabled"`
	FocusModeUntil    *time.Time `json:"focus_mode_until"`
	NudgeIntervalMins int        `json:"nudge_interval_mins"`
	HideLikeCounts    bool       `json:"hide_like_counts"`
	DetoxModeUntil    *time.Time `json:"detox_mode_until"`
	UpdatedAt         *time.Time `json:"updated_at"`
}

// validateWellbeing turns a request into the row to store, or a
// *wellbeingError describing the first rule it broke.
func validateWellbeing(userID uuid.UUID, req updateWellbeingRequest, now time.Time) (*store.DigitalWellbeing, error) {
	w := &store.DigitalWellbeing{
		UserID:            userID,
		NudgeIntervalMins: defaultNudgeMins,
	}

	// Daily limit: absent or 0 = off; otherwise 10..1440 minutes.
	if req.DailyLimitMins != nil && *req.DailyLimitMins != 0 {
		v := *req.DailyLimitMins
		if v < minDailyLimitMins || v > maxDailyLimitMins {
			return nil, &wellbeingError{"INVALID_DAILY_LIMIT",
				fmt.Sprintf("daily_limit_mins must be 0 (off) or between %d and %d", minDailyLimitMins, maxDailyLimitMins)}
		}
		w.DailyLimitMins = &v
	}

	// Sleep hours: both null (off) or both valid HH:MM and different.
	start, startSet := clockOrEmpty(req.BedtimeStart)
	end, endSet := clockOrEmpty(req.BedtimeEnd)
	switch {
	case !startSet && !endSet:
		// off
	case startSet != endSet:
		return nil, &wellbeingError{"INVALID_SLEEP_HOURS",
			"bedtime_start and bedtime_end must both be set or both be null"}
	default:
		s, ok1 := parseClock(start)
		e, ok2 := parseClock(end)
		if !ok1 || !ok2 {
			return nil, &wellbeingError{"INVALID_SLEEP_HOURS",
				"bedtime_start and bedtime_end must be \"HH:MM\" (00:00..23:59)"}
		}
		if s == e {
			return nil, &wellbeingError{"INVALID_SLEEP_HOURS",
				"bedtime_start and bedtime_end must differ"}
		}
		w.BedtimeStart = &s
		w.BedtimeEnd = &e
	}

	// Nudge interval: absent = default; otherwise 5..240.
	if req.NudgeIntervalMins != nil {
		v := *req.NudgeIntervalMins
		if v < minNudgeMins || v > maxNudgeMins {
			return nil, &wellbeingError{"INVALID_NUDGE_INTERVAL",
				fmt.Sprintf("nudge_interval_mins must be between %d and %d", minNudgeMins, maxNudgeMins)}
		}
		w.NudgeIntervalMins = v
	}

	if req.FocusModeUntil != nil {
		if !req.FocusModeUntil.After(now) {
			return nil, &wellbeingError{"INVALID_FOCUS_MODE_UNTIL", "focus_mode_until must be in the future"}
		}
		t := req.FocusModeUntil.UTC()
		w.FocusModeUntil = &t
	}
	if req.DetoxModeUntil != nil {
		if !req.DetoxModeUntil.After(now) {
			return nil, &wellbeingError{"INVALID_DETOX_MODE_UNTIL", "detox_mode_until must be in the future"}
		}
		t := req.DetoxModeUntil.UTC()
		w.DetoxModeUntil = &t
	}

	if req.FocusModeEnabled != nil {
		w.FocusModeEnabled = *req.FocusModeEnabled
	}
	if req.HideLikeCounts != nil {
		w.HideLikeCounts = *req.HideLikeCounts
	}
	return w, nil
}

// clockOrEmpty treats null and "" the same way: not set.
func clockOrEmpty(p *string) (string, bool) {
	if p == nil {
		return "", false
	}
	s := strings.TrimSpace(*p)
	return s, s != ""
}

// parseClock accepts "HH:MM" or "HH:MM:SS" (Postgres TIME::TEXT) and returns
// the canonical "HH:MM".
func parseClock(s string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 && len(parts) != 3 {
		return "", false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 2 {
			return "", false
		}
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return "", false
	}
	if len(parts) == 3 {
		sec, err := strconv.Atoi(parts[2])
		if err != nil || sec < 0 || sec > 59 {
			return "", false
		}
	}
	return fmt.Sprintf("%02d:%02d", h, m), true
}

// normalizeClock maps whatever the store hands back ("23:00:00") to "HH:MM";
// anything unparsable is returned as-is rather than dropped.
func normalizeClock(p *string) *string {
	if p == nil {
		return nil
	}
	if v, ok := parseClock(*p); ok {
		return &v
	}
	return p
}

func toWellbeingView(w *store.DigitalWellbeing) wellbeingView {
	v := wellbeingView{
		DailyLimitMins:    w.DailyLimitMins,
		BedtimeStart:      normalizeClock(w.BedtimeStart),
		BedtimeEnd:        normalizeClock(w.BedtimeEnd),
		FocusModeEnabled:  w.FocusModeEnabled,
		FocusModeUntil:    w.FocusModeUntil,
		NudgeIntervalMins: w.NudgeIntervalMins,
		HideLikeCounts:    w.HideLikeCounts,
		DetoxModeUntil:    w.DetoxModeUntil,
	}
	if !w.UpdatedAt.IsZero() {
		t := w.UpdatedAt
		v.UpdatedAt = &t
	}
	return v
}

// --- Screen time ---
//
// POST /v1/users/me/screen-time
//
//	{"date":"YYYY-MM-DD", "foreground_secs":1234, "sessions":3}
//
// One row per (user, date). The client owns the day's total, so a repeat POST
// for the same date REPLACES minutes/sessions (idempotent) rather than adding
// to them. date defaults to today (UTC); it may not be in the future or more
// than 30 days old. foreground_secs is 0..86400 and is stored as
// ceil(secs/60) minutes. The legacy body {"minutes":N,"sessions":M} is still
// accepted (minutes 0..1440); when both are present foreground_secs wins.
//
// Response: {"data":{"date":"YYYY-MM-DD","minutes":N,"sessions":M}}
//
// GET /v1/users/me/screen-time?range=today|week (default today)
//
//	{"data":{"range":"week","days":[{"date","minutes","sessions"}],
//	         "today_minutes":N,"daily_limit_mins":N|null}}
//
// week = the last 7 days including today, ascending, missing days as 0.

const (
	maxForegroundSecs  = 86400
	maxMinutesPerDay   = 1440
	maxSessionsPerDay  = 10000
	maxBackfillDays    = 30
	screenTimeDateSpec = "2006-01-02"
)

type logScreenTimeRequest struct {
	Date           *string `json:"date"`
	ForegroundSecs *int    `json:"foreground_secs"`
	Minutes        *int    `json:"minutes"` // legacy
	Sessions       *int    `json:"sessions"`
}

type screenTimeDay struct {
	Date     string `json:"date"`
	Minutes  int    `json:"minutes"`
	Sessions int    `json:"sessions"`
}

type screenTimeView struct {
	Range          string          `json:"range"`
	Days           []screenTimeDay `json:"days"`
	TodayMinutes   int             `json:"today_minutes"`
	DailyLimitMins *int            `json:"daily_limit_mins"`
}

// utcDate truncates t to a UTC calendar day (midnight UTC).
func utcDate(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// validateScreenTime resolves the day and the totals to store.
func validateScreenTime(req logScreenTimeRequest, now time.Time) (date time.Time, minutes, sessions int, err error) {
	today := utcDate(now)
	date = today
	if req.Date != nil && strings.TrimSpace(*req.Date) != "" {
		d, perr := time.ParseInLocation(screenTimeDateSpec, strings.TrimSpace(*req.Date), time.UTC)
		if perr != nil {
			return date, 0, 0, &wellbeingError{"INVALID_DATE", "date must be \"YYYY-MM-DD\""}
		}
		if d.After(today) {
			return date, 0, 0, &wellbeingError{"INVALID_DATE", "date may not be in the future"}
		}
		if today.Sub(d) > time.Duration(maxBackfillDays)*24*time.Hour {
			return date, 0, 0, &wellbeingError{"INVALID_DATE",
				fmt.Sprintf("date may not be more than %d days old", maxBackfillDays)}
		}
		date = d
	}

	switch {
	case req.ForegroundSecs != nil:
		secs := *req.ForegroundSecs
		if secs < 0 || secs > maxForegroundSecs {
			return date, 0, 0, &wellbeingError{"INVALID_SCREEN_TIME",
				fmt.Sprintf("foreground_secs must be between 0 and %d", maxForegroundSecs)}
		}
		minutes = (secs + 59) / 60
	case req.Minutes != nil:
		minutes = *req.Minutes
		if minutes < 0 || minutes > maxMinutesPerDay {
			return date, 0, 0, &wellbeingError{"INVALID_SCREEN_TIME",
				fmt.Sprintf("minutes must be between 0 and %d", maxMinutesPerDay)}
		}
	default:
		return date, 0, 0, &wellbeingError{"INVALID_SCREEN_TIME", "foreground_secs is required"}
	}

	if req.Sessions != nil {
		sessions = *req.Sessions
		if sessions < 0 || sessions > maxSessionsPerDay {
			return date, 0, 0, &wellbeingError{"INVALID_SCREEN_TIME",
				fmt.Sprintf("sessions must be between 0 and %d", maxSessionsPerDay)}
		}
	}
	return date, minutes, sessions, nil
}

// fillScreenTimeDays lays the stored rows onto every day from..to
// (inclusive, ascending), with zeros for days that have no row.
func fillScreenTimeDays(logs []store.ScreenTimeLog, from, to time.Time) []screenTimeDay {
	byDate := make(map[string]store.ScreenTimeLog, len(logs))
	for _, l := range logs {
		byDate[utcDate(l.Date).Format(screenTimeDateSpec)] = l
	}
	from, to = utcDate(from), utcDate(to)
	days := []screenTimeDay{}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		key := d.Format(screenTimeDateSpec)
		day := screenTimeDay{Date: key}
		if l, ok := byDate[key]; ok {
			day.Minutes = l.Minutes
			day.Sessions = l.SessionCount
		}
		days = append(days, day)
	}
	return days
}
