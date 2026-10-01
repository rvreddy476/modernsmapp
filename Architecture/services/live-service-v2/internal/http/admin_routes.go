package http

// Admin token family (1 Oct 2026). Every route needs an admin-service token
// for audience "live" carrying the route's permission (admin_token.go).
//
//	GET    /v1/livestream/internal/admin/streams?status=live|reconnecting|starting|all   live:streams.read
//	POST   /v1/livestream/internal/admin/streams/:id/stop {reason}                     live:streams.stop
//	DELETE /v1/livestream/internal/admin/streams/:id/chat/:messageId {reason?}         live:chat.moderate
//	GET    /v1/livestream/internal/admin/reports?status=open|resolved|all              live:reports.read
//	POST   /v1/livestream/internal/admin/reports/:id/resolve {action, reason}          live:reports.act
//	       (+ live:users.ban for action=ban_user, + live:chat.moderate for action=remove_message)
//	POST   /v1/livestream/internal/admin/users/:userId/live-ban {reason}               live:users.ban
//	DELETE /v1/livestream/internal/admin/users/:userId/live-ban {reason?}              live:users.ban
//	GET    /v1/livestream/internal/admin/bans?limit=&offset=                           live:users.ban
//	DELETE /v1/livestream/internal/admin/users/:userId/badges/founding_creator {reason} live:users.ban

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/service"
	"github.com/atpost/shared/api"
)

func (h *Handler) registerAdminTokenRoutes(r *gin.Engine) {
	g := r.Group(InternalAdminPrefix)
	gate := h.requireAdminToken

	g.GET("/streams", gate(PermStreamsRead), h.AdminListStreams)
	g.POST("/streams/:id/stop", gate(PermStreamsStop), h.AdminStopStream)
	g.DELETE("/streams/:id/chat/:messageId", gate(PermChatModerate), h.AdminRemoveChatMessage)
	g.GET("/reports", gate(PermReportsRead), h.AdminListReports)
	g.POST("/reports/:id/resolve", gate(PermReportsAct), h.AdminResolveReport)
	g.POST("/users/:userId/live-ban", gate(PermUsersBan), h.AdminLiveBan)
	g.DELETE("/users/:userId/live-ban", gate(PermUsersBan), h.AdminLiveUnban)
	g.GET("/bans", gate(PermUsersBan), h.AdminListLiveBans)
	// Founding creator badge (surfaces_routes.go): revoke, audited.
	g.DELETE("/users/:userId/badges/:badge", gate(PermUsersBan), h.AdminRevokeBadge)
}

type adminReasonBody struct {
	Reason string `json:"reason"`
}

// bindOptionalReason reads {reason} from a body that may be empty, or
// ?reason= when there is no body.
func bindOptionalReason(c *gin.Context) (string, bool) {
	var body adminReasonBody
	if c.Request.ContentLength != 0 && c.Request.Body != nil {
		if err := c.ShouldBindJSON(&body); err != nil && err.Error() != "EOF" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
			return "", false
		}
	}
	if body.Reason == "" {
		body.Reason = c.Query("reason")
	}
	return body.Reason, true
}

func queryLimit(c *gin.Context, def int) int {
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 {
		return v
	}
	return def
}

// AdminListStreams — GET .../admin/streams?status=&limit=
func (h *Handler) AdminListStreams(c *gin.Context) {
	rows, err := h.svc.AdminListStreams(c.Request.Context(), c.Query("status"), queryLimit(c, 100))
	if err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rows, nil)
}

// AdminStopStream — POST .../admin/streams/:id/stop {reason}
func (h *Handler) AdminStopStream(c *gin.Context) {
	actor, _ := tokenActor(c)
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	var body adminReasonBody
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	res, err := h.svc.AdminStopStream(c.Request.Context(), actor, streamID, body.Reason)
	if err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// AdminRemoveChatMessage — DELETE .../admin/streams/:id/chat/:messageId
func (h *Handler) AdminRemoveChatMessage(c *gin.Context) {
	actor, _ := tokenActor(c)
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	msgID, ok := requireUUID(c, "messageId")
	if !ok {
		return
	}
	reason, ok := bindOptionalReason(c)
	if !ok {
		return
	}
	if err := h.svc.AdminRemoveChatMessage(c.Request.Context(), actor, streamID, msgID, reason); err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"status": "removed", "message_id": msgID}, nil)
}

// AdminListReports — GET .../admin/reports?status=&limit=
func (h *Handler) AdminListReports(c *gin.Context) {
	rows, err := h.svc.AdminListReports(c.Request.Context(), c.Query("status"), queryLimit(c, 100))
	if err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rows, nil)
}

type resolveReportBody struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// AdminResolveReport — POST .../admin/reports/:id/resolve {action, reason}.
// The route needs live:reports.act; ban_user also needs live:users.ban and
// remove_message also needs live:chat.moderate, both checked on the token.
func (h *Handler) AdminResolveReport(c *gin.Context) {
	actor, _ := tokenActor(c)
	reportID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	var body resolveReportBody
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	switch body.Action {
	case service.ResolveDismiss, service.ResolveBanUser, service.ResolveRemoveMessage:
	default:
		writeModerationErr(c, service.ErrInvalidAction)
		return
	}
	if extra := service.ResolvePermissionExtra(body.Action); extra != "" && !h.requireExtraPermission(c, extra) {
		return
	}
	rep, err := h.svc.AdminResolveReport(c.Request.Context(), actor, reportID, service.AdminResolveInput{Action: body.Action, Reason: body.Reason})
	if err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rep, nil)
}

// AdminLiveBan — POST .../admin/users/:userId/live-ban {reason}
func (h *Handler) AdminLiveBan(c *gin.Context) {
	actor, _ := tokenActor(c)
	userID, ok := requireUUID(c, "userId")
	if !ok {
		return
	}
	var body adminReasonBody
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	res, err := h.svc.AdminLiveBan(c.Request.Context(), actor, userID, body.Reason)
	if err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// AdminLiveUnban — DELETE .../admin/users/:userId/live-ban {reason?}
func (h *Handler) AdminLiveUnban(c *gin.Context) {
	actor, _ := tokenActor(c)
	userID, ok := requireUUID(c, "userId")
	if !ok {
		return
	}
	reason, ok := bindOptionalReason(c)
	if !ok {
		return
	}
	res, err := h.svc.AdminLiveUnban(c.Request.Context(), actor, userID, reason)
	if err != nil {
		writeModerationErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// AdminListLiveBans — GET .../admin/bans?limit=&offset=
func (h *Handler) AdminListLiveBans(c *gin.Context) {
	limit := queryLimit(c, 50)
	offset, _ := strconv.Atoi(c.Query("offset"))
	rows, err := h.svc.AdminListLiveBans(c.Request.Context(), limit, offset)
	if err != nil {
		writeModerationErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows, "meta": gin.H{"limit": limit, "offset": offset, "count": len(rows)}})
}

// --- internal viewer route (ws-gateway) ---

// InternalStreamViewer — GET /v1/livestream/internal/streams/:id/viewer?user_id=
// (internal key, ALWAYS required). {"data":{"allowed":bool}}: the same gate
// as the viewer token. 503 when the gate could not decide.
func (h *Handler) InternalStreamViewer(c *gin.Context) {
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	userID, err := uuid.Parse(c.Query("user_id"))
	if err != nil || userID == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user_id", nil)
		return
	}
	allowed, err := h.svc.MayWatch(c.Request.Context(), streamID, userID)
	if err != nil {
		if errors.Is(err, service.ErrAuthorityUnavailable) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "AUTHORITY_UNAVAILABLE", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "viewer check failed", nil)
		return
	}
	// Exactly {"data":{"allowed":bool}} — the ws-gateway refuses any other shape.
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"allowed": allowed}})
}
