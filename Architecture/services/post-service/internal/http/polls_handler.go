// polls_handler.go holds the poll routes: read a poll, the two vote routes,
// poll results, and the mapper from vote refusals to stable error codes.
// Moved out of handler.go unchanged.

package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) GetPoll(c *gin.Context) {
	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	viewerID := c.GetHeader("X-User-Id")
	var viewerUUID *uuid.UUID
	if id, err := uuid.Parse(viewerID); err == nil {
		viewerUUID = &id
	}

	// The post detail's read gate (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, postID, viewerUUID) {
		return
	}
	poll, err := h.svc.GetPoll(c.Request.Context(), postID, viewerUUID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if poll == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Poll not found", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, poll, nil)
}

type VoteRequest struct {
	OptionID string `json:"option_id" binding:"required"`
}

func (h *Handler) CastVote(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	var req VoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	optionID, err := uuid.Parse(req.OptionID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid option ID", nil)
		return
	}

	// The post detail's read gate (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, postID, &userID) {
		return
	}
	if err := h.svc.CastVote(c.Request.Context(), postID, optionID, userID); err != nil {
		h.writePollVoteError(c, err)
		return
	}

	// The body stays {"status":"voted"} rather than converging on
	// /poll/vote's {"ok":true}: the behaviour is now shared, but the response
	// shape is what a shipped client parses.
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "voted"}, nil)
}

// writePollVoteError is the one place either vote route turns a refusal into
// a status and a code. Both routes use it, which is the point — they used to
// disagree, and a caller had to know which URL it had picked to know what a
// failure would look like.
//
// Everything a client can fix is a 400 with its own code, so the codes are
// the contract and the messages are only for humans. The default case does
// NOT echo err.Error(): that is how the pgx string, complete with
// `poll_votes_pkey` and `(SQLSTATE 23505)`, reached the browser. The
// request_id in the envelope is the handle for finding the real error in the
// logs.
func (h *Handler) writePollVoteError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrPollNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "POLL_NOT_FOUND",
			"this post has no poll", nil)
	case errors.Is(err, service.ErrPollEnded):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "POLL_ENDED",
			"this poll has ended", nil)
	case errors.Is(err, service.ErrPollOptionInvalid):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "POLL_OPTION_INVALID",
			"that option does not belong to this poll", nil)
	case errors.Is(err, service.ErrPollAlreadyVoted):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "POLL_ALREADY_VOTED",
			"you have already voted on this poll", nil)
	default:
		slog.Error("poll vote failed",
			"post_id", c.Param("postId"),
			"error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR",
			"could not record your vote", nil)
	}
}

// ============================================================
// Poll Results Handlers
// ============================================================

func (h *Handler) CastPollVote(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	var req struct {
		OptionID uuid.UUID `json:"option_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	// The post detail's read gate (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, postID, &userID) {
		return
	}
	if err := h.svc.CastPollVote(c.Request.Context(), postID, req.OptionID, userID); err != nil {
		h.writePollVoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"ok": true}, nil)
}

func (h *Handler) GetPollResults(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	// The post detail's read gate (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, postID, optionalCallerID(c)) {
		return
	}
	results, err := h.svc.GetPollResults(c.Request.Context(), postID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if results == nil {
		results = []postgres.PollVoteResult{}
	}
	api.JSON(c.Writer, http.StatusOK, results, nil)
}
