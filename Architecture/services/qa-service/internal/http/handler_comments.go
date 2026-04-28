package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

func (h *Handler) CreateComment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	aID, ok := parseUUID(c, "answerId")
	if !ok {
		return
	}

	var body struct {
		Body string `json:"body"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	comment, err := h.svc.CreateComment(c.Request.Context(), aID, userID, body.Body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "CREATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, comment, nil)
}

func (h *Handler) ListComments(c *gin.Context) {
	aID, ok := parseUUID(c, "answerId")
	if !ok {
		return
	}
	limit, offset := parsePagination(c)

	comments, err := h.svc.ListComments(c.Request.Context(), aID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QUERY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, comments, nil)
}

func (h *Handler) UpdateComment(c *gin.Context) {
	cID, ok := parseUUID(c, "commentId")
	if !ok {
		return
	}

	var body struct {
		Body string `json:"body"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	comment, err := h.svc.UpdateComment(c.Request.Context(), cID, body.Body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, comment, nil)
}

func (h *Handler) DeleteComment(c *gin.Context) {
	cID, ok := parseUUID(c, "commentId")
	if !ok {
		return
	}

	if err := h.svc.DeleteComment(c.Request.Context(), cID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "DELETE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}
