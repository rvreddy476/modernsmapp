package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Presence: heartbeat and online status. Who may see whom is presence_visibility.go.

// Heartbeat sets the calling user as online with a 90-second TTL.
// Clients should call this every 30 seconds to maintain online status.
func (h *Handler) Heartbeat(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	if err := h.presenceStore.SetOnline(c.Request.Context(), userID.String()); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update presence", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

// GetOnlineStatus returns whether the target user is online.
// Only a requester graph-service's see_online_status permission admits
// (presenceVisibleTo) receives a truthful answer; everyone else receives
// {"online": false} regardless of actual status (fail-closed).
func (h *Handler) GetOnlineStatus(c *gin.Context) {
	requesterIDStr := c.GetHeader("X-User-Id")
	requesterID, err := uuid.Parse(requesterIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	targetIDStr := c.Param("userId")
	targetID, err := uuid.Parse(targetIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	// The self-check is always truthful; anyone else needs graph-service's
	// see_online_status permission. This used to read an `is_friend` key that
	// /v1/graph/relationship has never returned (it says is_connection), so
	// every other user was reported offline.
	visible := h.presenceVisibleTo(c.Request.Context(), requesterID, []string{targetID.String()})
	if !visible[targetID.String()] {
		api.JSON(c.Writer, http.StatusOK, map[string]bool{"online": false}, nil)
		return
	}

	online, err := h.presenceStore.IsOnline(c.Request.Context(), targetID.String())
	if err != nil {
		// Fail-closed: on Redis error, report offline rather than leaking status
		api.JSON(c.Writer, http.StatusOK, map[string]bool{"online": false}, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]bool{"online": online}, nil)
}

// GetOnlineStatusBatch returns online status for many users in a single call,
// so a friends list does not need one request per friend. Privacy mirrors
// GetOnlineStatus: only targets whose see_online_status permission admits the
// requester (and the requester themselves) get a truthful answer; everyone
// else is reported offline (fail-closed). The batch is capped at 100 ids.
func (h *Handler) GetOnlineStatusBatch(c *gin.Context) {
	requesterID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body", nil)
		return
	}

	// Validate, dedupe and cap the batch.
	const maxBatch = 100
	targets := make([]string, 0, len(req.UserIDs))
	seen := make(map[string]struct{}, len(req.UserIDs))
	for _, idStr := range req.UserIDs {
		if len(targets) >= maxBatch {
			break
		}
		if _, dup := seen[idStr]; dup {
			continue
		}
		if _, perr := uuid.Parse(idStr); perr != nil {
			continue
		}
		seen[idStr] = struct{}{}
		targets = append(targets, idStr)
	}

	// Default every requested user to offline.
	result := make(map[string]bool, len(targets))
	for _, t := range targets {
		result[t] = false
	}
	if len(targets) == 0 {
		api.JSON(c.Writer, http.StatusOK, map[string]any{"online": result}, nil)
		return
	}

	// Privacy gate: graph-service's see_online_status decision per target
	// (the requester themselves always passes). It used to be "one of the
	// requester's legacy connections", a list that can no longer grow now
	// that connections are retired (24 Oct 2026). Any error leaves a target
	// hidden — fail-closed, leak nothing.
	admitted := h.presenceVisibleTo(c.Request.Context(), requesterID, targets)

	// Resolve presence for the visible subset only — one Redis round-trip.
	visible := make([]string, 0, len(targets))
	for _, t := range targets {
		if admitted[t] {
			visible = append(visible, t)
		}
	}
	if len(visible) > 0 {
		if online, oerr := h.presenceStore.AreOnline(c.Request.Context(), visible); oerr == nil {
			for id, on := range online {
				result[id] = on
			}
		}
	}

	api.JSON(c.Writer, http.StatusOK, map[string]any{"online": result}, nil)
}
