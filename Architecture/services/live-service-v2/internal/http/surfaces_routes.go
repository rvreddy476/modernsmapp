package http

// Live surfaces, free hearts, top supporters and the founding creator badge
// (2 Oct 2026). All under /v1/livestream, behind the internal key like the
// rest of the public family; the viewer is the gateway's X-User-Id, absent
// for a signed-out caller (who sees public streams only).
//
//	PATCH  /streams/:id                      host, only while scheduled
//	GET    /streams?status=live              + orientation, category, following, sort (handler.go)
//	GET    /streams/upcoming                 orientation, category, following, cursor, limit
//	GET    /categories/live?orientation=
//	GET    /creators/live?limit=&orientation=
//	GET    /users/:userId/streams?status=live|upcoming|past
//	GET    /users/:userId/badges
//	PUT    /streams/:id/reminder             signed in
//	DELETE /streams/:id/reminder             signed in
//	POST   /streams/:id/hearts {count}       signed in
//	GET    /streams/:id/supporters?limit=
//
// and, outside that group:
//
//	GET    /v1/livestream/internal/streams/:id/reminders?after=&limit=     internal key (handler.go)
//	DELETE /v1/livestream/internal/admin/users/:userId/badges/:badge       admin token, live:users.ban

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/service"
	"github.com/atpost/shared/api"
)

func (h *Handler) registerSurfaceRoutes(v1 *gin.RouterGroup) {
	v1.PATCH("/streams/:id", h.UpdateStream)
	v1.GET("/streams/upcoming", h.ListUpcoming)
	v1.GET("/categories/live", h.ListLiveCategories)
	v1.GET("/creators/live", h.ListLiveCreators)
	v1.GET("/users/:userId/streams", h.ListUserStreams)
	v1.GET("/users/:userId/badges", h.ListUserBadges)
	v1.PUT("/streams/:id/reminder", h.SetReminder)
	v1.DELETE("/streams/:id/reminder", h.DeleteReminder)
	v1.POST("/streams/:id/hearts", h.SendHearts)
	v1.GET("/streams/:id/supporters", h.ListSupporters)
}

// discoverParams reads the listing filters. following is true only for
// "true" or "1".
func discoverParams(c *gin.Context) service.DiscoverParams {
	p := service.DiscoverParams{
		Orientation: c.Query("orientation"),
		Category:    c.Query("category"),
		Sort:        c.Query("sort"),
		Cursor:      c.Query("cursor"),
		Limit:       20,
	}
	if f, err := strconv.ParseBool(strings.TrimSpace(c.Query("following"))); err == nil {
		p.Following = f
	}
	if n, err := strconv.Atoi(c.Query("limit")); err == nil {
		p.Limit = n
	}
	return p
}

func writeStreamPage(c *gin.Context, res *service.ListLiveResult) {
	meta := &api.Meta{}
	if res.NextCursor != "" {
		meta.NextCursor = res.NextCursor
	}
	api.JSON(c.Writer, http.StatusOK, res.Streams, meta)
}

// ListUpcoming — GET /v1/livestream/streams/upcoming
// Scheduled streams still ahead, soonest first; each row carries
// reminder_count and, for a signed-in caller, reminder_set.
func (h *Handler) ListUpcoming(c *gin.Context) {
	res, err := h.svc.DiscoverUpcoming(c.Request.Context(), optionalUserID(c), discoverParams(c))
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	writeStreamPage(c, res)
}

// ListLiveCategories — GET /v1/livestream/categories/live
func (h *Handler) ListLiveCategories(c *gin.Context) {
	rows, err := h.svc.LiveCategories(c.Request.Context(), optionalUserID(c), c.Query("orientation"))
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rows, nil)
}

// ListLiveCreators — GET /v1/livestream/creators/live?limit= (default 10, max 50)
func (h *Handler) ListLiveCreators(c *gin.Context) {
	rows, err := h.svc.LiveCreators(c.Request.Context(), optionalUserID(c), queryLimit(c, 10), c.Query("orientation"))
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rows, nil)
}

// ListUserStreams — GET /v1/livestream/users/:userId/streams?status=live|upcoming|past
func (h *Handler) ListUserStreams(c *gin.Context) {
	userID, ok := requireUUID(c, "userId")
	if !ok {
		return
	}
	res, err := h.svc.UserStreams(c.Request.Context(), optionalUserID(c), userID, c.Query("status"), queryLimit(c, 20), c.Query("cursor"))
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	writeStreamPage(c, res)
}

// ListUserBadges — GET /v1/livestream/users/:userId/badges (public)
// {"data":{"badges":[{"badge":"founding_creator","granted_at":"…"}]}}
func (h *Handler) ListUserBadges(c *gin.Context) {
	userID, ok := requireUUID(c, "userId")
	if !ok {
		return
	}
	badges, err := h.svc.UserBadges(c.Request.Context(), userID)
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"badges": badges}, nil)
}

// UpdateStream — PATCH /v1/livestream/streams/:id
//
// Body: any of title, description, category, cover_media_id, scheduled_at,
// visibility, orientation. A field that is absent is left alone; "" clears
// category and description (title cannot be emptied: 422);
// cover_media_id and scheduled_at are cleared by null or "". Host only (403),
// only while the stream is scheduled (409 STREAM_STATE_CONFLICT).
func (h *Handler) UpdateStream(c *gin.Context) {
	hostID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 64<<10))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "unreadable body", nil)
		return
	}
	p, msg := parseStreamPatch(raw)
	if msg != "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", msg, nil)
		return
	}
	st, err := h.svc.UpdateStream(c.Request.Context(), streamID, hostID, p)
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

// parseStreamPatch reads the PATCH body field by field, so "absent" (leave
// alone) and "null" (clear) stay different. msg is non-empty for a body that
// is not a JSON object or a field of the wrong type.
func parseStreamPatch(raw []byte) (p service.UpdateStreamParams, msg string) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return p, "body must be a JSON object"
	}
	isNull := func(v json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(v), []byte("null")) }
	str := func(name string, dst **string) bool {
		v, ok := fields[name]
		if !ok || isNull(v) {
			return true // null on a text field is "leave alone"
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			msg = name + " must be a string"
			return false
		}
		*dst = &s
		return true
	}
	if !str("title", &p.Title) || !str("description", &p.Description) || !str("category", &p.Category) ||
		!str("visibility", &p.Visibility) || !str("orientation", &p.Orientation) {
		return p, msg
	}
	// The two fields that can be cleared: null or "" clears, a value sets.
	clears := func(v json.RawMessage) bool {
		return isNull(v) || bytes.Equal(bytes.TrimSpace(v), []byte(`""`))
	}
	if v, ok := fields["cover_media_id"]; ok {
		p.SetCoverMediaID = true
		if !clears(v) {
			var id uuid.UUID
			if err := json.Unmarshal(v, &id); err != nil {
				return p, `cover_media_id must be a uuid, null or ""`
			}
			p.CoverMediaID = &id
		}
	}
	if v, ok := fields["scheduled_at"]; ok {
		p.SetScheduledAt = true
		if !clears(v) {
			var t time.Time
			if err := json.Unmarshal(v, &t); err != nil {
				return p, `scheduled_at must be an RFC3339 time, null or ""`
			}
			p.ScheduledAt = &t
		}
	}
	return p, ""
}

// SetReminder — PUT /v1/livestream/streams/:id/reminder
// {"data":{"reminder_set":true,"reminder_count":n}}
func (h *Handler) SetReminder(c *gin.Context) { h.reminder(c, true) }

// DeleteReminder — DELETE /v1/livestream/streams/:id/reminder
// {"data":{"reminder_set":false,"reminder_count":n}}
func (h *Handler) DeleteReminder(c *gin.Context) { h.reminder(c, false) }

func (h *Handler) reminder(c *gin.Context, set bool) {
	viewerID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	res, err := h.svc.SetReminder(c.Request.Context(), streamID, viewerID, set)
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// InternalStreamReminders — GET /v1/livestream/internal/streams/:id/reminders?after=&limit=
// (internal key, ALWAYS required). {"data":{"user_ids":[…],"next_after":"…","has_more":bool}}:
// who set a reminder, by user id, after the `after` cursor. limit defaults
// to and is capped at 1000.
func (h *Handler) InternalStreamReminders(c *gin.Context) {
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	after := uuid.Nil
	if raw := c.Query("after"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid after", nil)
			return
		}
		after = id
	}
	page, err := h.svc.ReminderUserIDs(c.Request.Context(), streamID, after, queryLimit(c, service.ReminderPageMax))
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

type heartsRequest struct {
	Count *int `json:"count"`
}

// SendHearts — POST /v1/livestream/streams/:id/hearts {"count": n}
// {"data":{"heart_count": <stream total>}}. n must be 1..20 (422).
func (h *Handler) SendHearts(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	var body heartsRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	n := 0 // a missing count is out of range like any other
	if body.Count != nil {
		n = *body.Count
	}
	total, err := h.svc.SendHearts(c.Request.Context(), streamID, userID, n)
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"heart_count": total}, nil)
}

// ListSupporters — GET /v1/livestream/streams/:id/supporters?limit= (default 10, max 50)
// {"data":[{"user":{user_id,name,handle,avatar_url,badges},"hearts":n,"messages":m,"rank":k}]}
func (h *Handler) ListSupporters(c *gin.Context) {
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	rows, err := h.svc.ListSupporters(c.Request.Context(), streamID, optionalUserID(c), queryLimit(c, 10))
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rows, nil)
}

// AdminRevokeBadge — DELETE .../admin/users/:userId/badges/:badge {reason}
// (live:users.ban). The reason is required; the badge is kept as revoked and
// never granted again.
func (h *Handler) AdminRevokeBadge(c *gin.Context) {
	actor, _ := tokenActor(c)
	userID, ok := requireUUID(c, "userId")
	if !ok {
		return
	}
	reason, ok := bindOptionalReason(c)
	if !ok {
		return
	}
	res, err := h.svc.AdminRevokeBadge(c.Request.Context(), actor, userID, c.Param("badge"), reason)
	if err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}
