package http

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// wellbeingStore is the slice of *store.Store the wellbeing routes need,
// narrowed so the handler can be exercised against a fake in tests.
type wellbeingStore interface {
	GetWellbeing(ctx context.Context, userID uuid.UUID) (*store.DigitalWellbeing, error)
	UpsertWellbeing(ctx context.Context, w *store.DigitalWellbeing) error
	UpsertScreenTimeLog(ctx context.Context, userID uuid.UUID, date time.Time, minutes, sessions int) error
	GetScreenTimeBetween(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]store.ScreenTimeLog, error)
}

// wellbeingHandler serves the wellbeing + screen-time routes. `now` is
// injectable so validation ("must be in the future", "today") is testable.
type wellbeingHandler struct {
	st  wellbeingStore
	now func() time.Time
}

func newWellbeingHandler(st wellbeingStore) *wellbeingHandler {
	return &wellbeingHandler{st: st, now: time.Now}
}

func (wh *wellbeingHandler) callerID(c *gin.Context) (uuid.UUID, bool) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return uuid.Nil, false
	}
	return userID, true
}

// writeValidation maps a *wellbeingError to a 400 with its code; anything
// else is a 500.
func writeValidation(c *gin.Context, err error) {
	var verr *wellbeingError
	if errors.As(err, &verr) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, verr.Code, verr.Message, nil)
		return
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
}

// GET /v1/users/me/wellbeing
func (wh *wellbeingHandler) Get(c *gin.Context) {
	userID, ok := wh.callerID(c)
	if !ok {
		return
	}
	w, err := wh.st.GetWellbeing(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, toWellbeingView(w), nil)
}

// PUT /v1/users/me/wellbeing — validate, replace the row, echo what is stored.
func (wh *wellbeingHandler) Update(c *gin.Context) {
	userID, ok := wh.callerID(c)
	if !ok {
		return
	}
	var req updateWellbeingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	row, err := validateWellbeing(userID, req, wh.now())
	if err != nil {
		writeValidation(c, err)
		return
	}
	if err := wh.st.UpsertWellbeing(c.Request.Context(), row); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	stored, err := wh.st.GetWellbeing(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, toWellbeingView(stored), nil)
}

// POST /v1/users/me/screen-time — idempotent per (user, date); replaces.
func (wh *wellbeingHandler) LogScreenTime(c *gin.Context) {
	userID, ok := wh.callerID(c)
	if !ok {
		return
	}
	var req logScreenTimeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	date, minutes, sessions, err := validateScreenTime(req, wh.now())
	if err != nil {
		writeValidation(c, err)
		return
	}
	if err := wh.st.UpsertScreenTimeLog(c.Request.Context(), userID, date, minutes, sessions); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, screenTimeDay{
		Date:     date.Format(screenTimeDateSpec),
		Minutes:  minutes,
		Sessions: sessions,
	}, nil)
}

// GET /v1/users/me/screen-time?range=today|week
func (wh *wellbeingHandler) GetScreenTime(c *gin.Context) {
	userID, ok := wh.callerID(c)
	if !ok {
		return
	}
	today := utcDate(wh.now())
	rng := c.DefaultQuery("range", "today")
	var from time.Time
	switch rng {
	case "today":
		from = today
	case "week":
		from = today.AddDate(0, 0, -6)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_RANGE", "range must be \"today\" or \"week\"", nil)
		return
	}

	ctx := c.Request.Context()
	logs, err := wh.st.GetScreenTimeBetween(ctx, userID, from, today)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w, err := wh.st.GetWellbeing(ctx, userID)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	days := fillScreenTimeDays(logs, from, today)
	api.JSON(c.Writer, http.StatusOK, screenTimeView{
		Range:          rng,
		Days:           days,
		TodayMinutes:   days[len(days)-1].Minutes,
		DailyLimitMins: w.DailyLimitMins,
	}, nil)
}
