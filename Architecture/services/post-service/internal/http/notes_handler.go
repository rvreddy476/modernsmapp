package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Echo short notes (docs/designs/echo-contract.md). Every route sits behind
// ECHO_ENABLED: switched off, each answers 503 ECHO_NOT_ENABLED before it
// reads a header or a body.

// maxNoteBodyBytes caps a create: 25 entries of 500 runes (4 bytes each)
// plus media ids is well under 128 KiB.
const maxNoteBodyBytes = 128 * 1024

// Note error codes on the wire.
const (
	CodeEchoNotEnabled          = "ECHO_NOT_ENABLED"
	CodeEmptyNote               = "EMPTY_NOTE"
	CodeNoteTooLong             = "NOTE_TOO_LONG"
	CodeTooManyMedia            = "TOO_MANY_MEDIA"
	CodeMediaNotAllowed         = "MEDIA_NOT_ALLOWED"
	CodePremiumRequired         = "PREMIUM_REQUIRED"
	CodePremiumCheckUnavailable = "PREMIUM_CHECK_UNAVAILABLE"
	CodeAccountTooNew           = "ACCOUNT_TOO_NEW"
	CodeAccountCheckUnavailable = "ACCOUNT_CHECK_UNAVAILABLE"
	CodeReplyNotAllowed         = "REPLY_NOT_ALLOWED"
	CodeNoteNotFound            = "NOTE_NOT_FOUND"
	CodeInvalidThread           = "INVALID_THREAD"
	CodeEditWindowClosed        = "EDIT_WINDOW_CLOSED"
	CodeNotAuthor               = "NOT_AUTHOR"
)

func (h *Handler) registerNoteRoutes(r *gin.Engine) {
	notes := r.Group("/v1/notes", h.requireEcho)
	{
		notes.POST("", h.CreateNote)
		notes.POST("/thread", h.CreateNoteThread)
		// Static segments before /:id, so "me" and "users" are never ids.
		notes.GET("/me/eligibility", h.NoteEligibility)
		notes.GET("/users/:userId", h.ListUserNotes)
		notes.GET("/:id", h.GetNote)
		notes.GET("/:id/replies", h.ListNoteReplies)
		notes.GET("/:id/conversation", h.NoteConversation)
		notes.PATCH("/:id", h.PatchNote)
		notes.DELETE("/:id", h.DeleteNote)
	}
}

// requireEcho is the ECHO_ENABLED switch.
func (h *Handler) requireEcho(c *gin.Context) {
	if !h.svc.EchoEnabled() {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
			CodeEchoNotEnabled, "Echo is not available yet", nil)
		c.Abort()
		return
	}
	c.Next()
}

// writeNoteError maps a note error onto the contract's codes; any other
// error is a 500.
func writeNoteError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	e := func(status int, code, msg string) { api.ErrorWithContext(ctx, c.Writer, status, code, msg, nil) }
	switch {
	case writeStandingError(c, err):
	case errors.Is(err, service.ErrEmptyNote):
		e(http.StatusBadRequest, CodeEmptyNote, err.Error())
	case errors.Is(err, service.ErrNoteTooLong):
		e(http.StatusBadRequest, CodeNoteTooLong, err.Error())
	case errors.Is(err, service.ErrTooManyNoteMedia):
		e(http.StatusBadRequest, CodeTooManyMedia, err.Error())
	case errors.Is(err, service.ErrNoteMediaNotAllowed):
		e(http.StatusBadRequest, CodeMediaNotAllowed, err.Error())
	case errors.Is(err, service.ErrInvalidNoteThread):
		e(http.StatusBadRequest, CodeInvalidThread, err.Error())
	case errors.Is(err, service.ErrInvalidNoteRequest), errors.Is(err, service.ErrInvalidNoteTab):
		e(http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case errors.Is(err, service.ErrPremiumRequired):
		e(http.StatusForbidden, CodePremiumRequired, "Posting on Echo needs Momentum Premium")
	case errors.Is(err, service.ErrPremiumCheckUnavailable):
		e(http.StatusServiceUnavailable, CodePremiumCheckUnavailable, "Please try again in a moment")
	case errors.Is(err, service.ErrAccountTooNew):
		e(http.StatusForbidden, CodeAccountTooNew, "Your account must be at least 7 days old to reply")
	case errors.Is(err, service.ErrAccountCheckUnavailable):
		e(http.StatusServiceUnavailable, CodeAccountCheckUnavailable, "Please try again in a moment")
	case errors.Is(err, service.ErrReplyNotAllowed):
		e(http.StatusForbidden, CodeReplyNotAllowed, "You cannot reply to this note")
	case errors.Is(err, service.ErrNoteNotFound):
		e(http.StatusNotFound, CodeNoteNotFound, "Note not found")
	case errors.Is(err, service.ErrNoteRateLimited):
		c.Header("Retry-After", "3600")
		e(http.StatusTooManyRequests, "RATE_LIMITED", err.Error())
	case errors.Is(err, service.ErrEditWindowClosed):
		e(http.StatusForbidden, CodeEditWindowClosed, err.Error())
	case errors.Is(err, service.ErrNotNoteAuthor):
		e(http.StatusForbidden, CodeNotAuthor, err.Error())
	case writeCreateGuardError(c, err):
		// media ownership / readiness, IDEMPOTENCY_KEY_REUSED
	default:
		e(http.StatusInternalServerError, "INTERNAL_ERROR", "Something went wrong")
	}
}

// noteCreateKey reads the required Idempotency-Key (a UUID).
func noteCreateKey(c *gin.Context) (string, bool) {
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"MISSING_IDEMPOTENCY_KEY", "Idempotency-Key header is required", nil)
		return "", false
	}
	if _, err := uuid.Parse(key); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_IDEMPOTENCY_KEY", "Idempotency-Key must be a UUID", nil)
		return "", false
	}
	return key, true
}

// bindNoteBody caps the body and decodes it into dst.
func bindNoteBody(c *gin.Context, dst any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxNoteBodyBytes)
	if err := c.ShouldBindJSON(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusRequestEntityTooLarge,
				"PAYLOAD_TOO_LARGE", "Request body exceeds the maximum size", nil)
			return false
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return false
	}
	return true
}

func noteIDParam(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		// An unparseable id names no note.
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, CodeNoteNotFound, "Note not found", nil)
		return uuid.Nil, false
	}
	return id, true
}

func noteLimit(c *gin.Context) int {
	n, _ := strconv.Atoi(c.Query("limit"))
	return n // the service clamps: 0 → 20, >50 → 50
}

// CreateNoteRequest is POST /v1/notes.
type CreateNoteRequest struct {
	Text        string      `json:"text"`
	MediaIDs    []uuid.UUID `json:"media_ids"`
	ReplyToID   *uuid.UUID  `json:"reply_to_id"`
	QuoteOfID   *uuid.UUID  `json:"quote_of_id"`
	ReplyPolicy string      `json:"reply_policy"`
}

// CreateNoteThreadRequest is POST /v1/notes/thread.
type CreateNoteThreadRequest struct {
	Entries []struct {
		Text     string      `json:"text"`
		MediaIDs []uuid.UUID `json:"media_ids"`
	} `json:"entries"`
	ReplyPolicy string `json:"reply_policy"`
}

func (h *Handler) CreateNote(c *gin.Context) {
	author, ok := requireViewer(c)
	if !ok {
		return
	}
	key, ok := noteCreateKey(c)
	if !ok {
		return
	}
	var req CreateNoteRequest
	if !bindNoteBody(c, &req) {
		return
	}
	v, err := h.svc.CreateNote(c.Request.Context(), &service.CreateNoteInput{
		AuthorID: author, Text: req.Text, MediaIDs: req.MediaIDs, ReplyToID: req.ReplyToID,
		QuoteOfID: req.QuoteOfID, ReplyPolicy: req.ReplyPolicy, IdempotencyKey: key,
	})
	if err != nil {
		writeNoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, v, nil)
}

func (h *Handler) CreateNoteThread(c *gin.Context) {
	author, ok := requireViewer(c)
	if !ok {
		return
	}
	key, ok := noteCreateKey(c)
	if !ok {
		return
	}
	var req CreateNoteThreadRequest
	if !bindNoteBody(c, &req) {
		return
	}
	in := &service.CreateNoteThreadInput{AuthorID: author, ReplyPolicy: req.ReplyPolicy, IdempotencyKey: key}
	for _, e := range req.Entries {
		in.Entries = append(in.Entries, service.ThreadEntryInput{Text: e.Text, MediaIDs: e.MediaIDs})
	}
	views, err := h.svc.CreateNoteThread(c.Request.Context(), in)
	if err != nil {
		writeNoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, gin.H{"notes": views}, nil)
}

func (h *Handler) GetNote(c *gin.Context) {
	id, ok := noteIDParam(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.GetNote(c.Request.Context(), id, optionalCallerID(c))
	if err != nil {
		writeNoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, v, nil)
}

func (h *Handler) ListNoteReplies(c *gin.Context) {
	id, ok := noteIDParam(c, "id")
	if !ok {
		return
	}
	page, err := h.svc.ListNoteReplies(c.Request.Context(), id, optionalCallerID(c), c.Query("cursor"), noteLimit(c))
	if err != nil {
		writeNoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

func (h *Handler) NoteConversation(c *gin.Context) {
	id, ok := noteIDParam(c, "id")
	if !ok {
		return
	}
	items, err := h.svc.NoteConversation(c.Request.Context(), id, optionalCallerID(c))
	if err != nil {
		writeNoteError(c, err)
		return
	}
	if items == nil {
		items = []*service.NoteView{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}

func (h *Handler) ListUserNotes(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	page, err := h.svc.ListUserNotes(c.Request.Context(), userID, optionalCallerID(c),
		c.DefaultQuery("tab", "notes"), c.Query("cursor"), noteLimit(c))
	if err != nil {
		writeNoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

// NotePatchRequest is PATCH /v1/notes/:id.
type NotePatchRequest struct {
	Text        *string `json:"text"`
	ReplyPolicy *string `json:"reply_policy"`
}

func (h *Handler) PatchNote(c *gin.Context) {
	caller, ok := requireViewer(c)
	if !ok {
		return
	}
	id, ok := noteIDParam(c, "id")
	if !ok {
		return
	}
	var req NotePatchRequest
	if !bindNoteBody(c, &req) {
		return
	}
	v, err := h.svc.PatchNote(c.Request.Context(), id, caller, service.NotePatch{Text: req.Text, ReplyPolicy: req.ReplyPolicy})
	if err != nil {
		writeNoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, v, nil)
}

func (h *Handler) DeleteNote(c *gin.Context) {
	caller, ok := requireViewer(c)
	if !ok {
		return
	}
	id, ok := noteIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteNote(c.Request.Context(), id, caller); err != nil {
		writeNoteError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) NoteEligibility(c *gin.Context) {
	caller, ok := requireViewer(c)
	if !ok {
		return
	}
	v, err := h.svc.NoteEligibility(c.Request.Context(), caller)
	if err != nil {
		writeNoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, v, nil)
}
