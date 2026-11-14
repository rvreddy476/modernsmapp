package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Echo v2 routes (docs/designs/echo-v2-contract.md §2), behind the same
// ECHO_ENABLED switch as every /v1/notes route (registerNoteRoutes).

func (h *Handler) writeNotePage(c *gin.Context, page *service.NotePage, err error) {
	if err != nil {
		writeNoteError(c, err)
		return
	}
	if page.Items == nil {
		page.Items = []*service.NoteView{}
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

// VoteNotePoll is POST /v1/notes/:id/poll/vote {option_index}.
func (h *Handler) VoteNotePoll(c *gin.Context) {
	voter, ok := requireViewer(c)
	if !ok {
		return
	}
	id, ok := noteIDParam(c, "id")
	if !ok {
		return
	}
	var req struct {
		OptionIndex *int `json:"option_index"`
	}
	if !bindNoteBody(c, &req) {
		return
	}
	if req.OptionIndex == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidPoll, "option_index is required", nil)
		return
	}
	v, err := h.svc.VoteNotePoll(c.Request.Context(), id, voter, *req.OptionIndex)
	if err != nil {
		writeNoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, v, nil)
}

// ListMyScheduledNotes is GET /v1/notes/me/scheduled.
func (h *Handler) ListMyScheduledNotes(c *gin.Context) {
	caller, ok := requireViewer(c)
	if !ok {
		return
	}
	page, err := h.svc.ListMyScheduledNotes(c.Request.Context(), caller, c.Query("cursor"), noteLimit(c))
	h.writeNotePage(c, page, err)
}

// RescheduleNote is PATCH /v1/notes/:id/schedule {scheduled_at}.
func (h *Handler) RescheduleNote(c *gin.Context) {
	caller, ok := requireViewer(c)
	if !ok {
		return
	}
	id, ok := noteIDParam(c, "id")
	if !ok {
		return
	}
	var req struct {
		ScheduledAt *time.Time `json:"scheduled_at"`
	}
	if !bindNoteBody(c, &req) {
		return
	}
	if req.ScheduledAt == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidSchedule, "scheduled_at is required", nil)
		return
	}
	v, err := h.svc.RescheduleNote(c.Request.Context(), id, caller, *req.ScheduledAt)
	if err != nil {
		writeNoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, v, nil)
}

// DeleteScheduledNote is DELETE /v1/notes/:id/schedule.
func (h *Handler) DeleteScheduledNote(c *gin.Context) {
	caller, ok := requireViewer(c)
	if !ok {
		return
	}
	id, ok := noteIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteScheduledNote(c.Request.Context(), id, caller); err != nil {
		writeNoteError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) setNoteBookmark(c *gin.Context, on bool) {
	viewer, ok := requireViewer(c)
	if !ok {
		return
	}
	id, ok := noteIDParam(c, "id")
	if !ok {
		return
	}
	st, err := h.svc.SetNoteBookmark(c.Request.Context(), id, viewer, on)
	if err != nil {
		writeNoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

// BookmarkNote is POST /v1/notes/:id/bookmark.
func (h *Handler) BookmarkNote(c *gin.Context) { h.setNoteBookmark(c, true) }

// UnbookmarkNote is DELETE /v1/notes/:id/bookmark.
func (h *Handler) UnbookmarkNote(c *gin.Context) { h.setNoteBookmark(c, false) }

// ListNoteBookmarks is GET /v1/notes/bookmarks.
func (h *Handler) ListNoteBookmarks(c *gin.Context) {
	viewer, ok := requireViewer(c)
	if !ok {
		return
	}
	page, err := h.svc.ListNoteBookmarks(c.Request.Context(), viewer, c.Query("cursor"), noteLimit(c))
	h.writeNotePage(c, page, err)
}

// RecordNoteViews is POST /v1/notes/views {note_ids}. Signed out is
// accepted (204) and not counted.
func (h *Handler) RecordNoteViews(c *gin.Context) {
	var req struct {
		NoteIDs []uuid.UUID `json:"note_ids"`
	}
	if !bindNoteBody(c, &req) {
		return
	}
	if err := h.svc.RecordNoteViews(c.Request.Context(), optionalCallerID(c), req.NoteIDs); err != nil {
		writeNoteError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListNoteQuotes is GET /v1/notes/:id/quotes.
func (h *Handler) ListNoteQuotes(c *gin.Context) {
	id, ok := noteIDParam(c, "id")
	if !ok {
		return
	}
	page, err := h.svc.ListNoteQuotes(c.Request.Context(), id, optionalCallerID(c), c.Query("cursor"), noteLimit(c))
	h.writeNotePage(c, page, err)
}

// NoteTrends is GET /v1/notes/trends?limit=.
func (h *Handler) NoteTrends(c *gin.Context) {
	items, err := h.svc.NoteTrends(c.Request.Context(), noteLimit(c))
	if err != nil {
		writeNoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}

// NotesByHashtag is GET /v1/notes/hashtags/:tag?tab=top|latest.
func (h *Handler) NotesByHashtag(c *gin.Context) {
	page, err := h.svc.NotesByHashtag(c.Request.Context(), c.Param("tag"), c.Query("tab"), optionalCallerID(c),
		c.Query("cursor"), noteLimit(c))
	h.writeNotePage(c, page, err)
}

// SearchNotes is GET /v1/notes/search?q=&tab=top|latest.
func (h *Handler) SearchNotes(c *gin.Context) {
	page, err := h.svc.SearchNotes(c.Request.Context(), c.Query("q"), c.Query("tab"), optionalCallerID(c),
		c.Query("cursor"), noteLimit(c))
	h.writeNotePage(c, page, err)
}

// NotesBatchInternal is GET /internal/notes/batch?ids=a,b,c with an
// optional X-User-Id viewer: {"items":[Note]} in order, without the notes
// that viewer may not see (and never a scheduled one). At most 50 ids.
func (h *Handler) NotesBatchInternal(c *gin.Context) {
	var ids []uuid.UUID
	for _, raw := range strings.Split(c.Query("ids"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid note id", nil)
			return
		}
		ids = append(ids, id)
	}
	items, err := h.svc.HydrateNotes(c.Request.Context(), ids, optionalCallerID(c))
	if err != nil {
		writeNoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}
