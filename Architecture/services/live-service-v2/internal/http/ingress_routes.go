package http

// Going live from streaming software or a camera (1 Oct 2026).
//
//	POST   /v1/livestream/streams/:id/ingress
//	DELETE /v1/livestream/streams/:id/ingress
//
// Both are the host's own. The stream key in the POST answer is a
// credential: it is in no other response, stream row, event or log, and the
// answer is marked no-store.

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/atpost/shared/api"
)

// CreateIngress — POST /v1/livestream/streams/:id/ingress
//
// 200 {"data":{"server_url","stream_key","ingress_id"}} — the same ingress
// on every call while it exists. 403 LIVE_NOT_ENABLED / LIVE_BANNED (the
// start gate), 403 FORBIDDEN (not the host), 404 NOT_FOUND, 422
// VALIDATION_ERROR (a device stream), 409 STREAM_STATE_CONFLICT (the stream
// has ended), 502 INGRESS_UNAVAILABLE. server_url and stream_key are
// separate values, as OBS's Custom service takes them.
func (h *Handler) CreateIngress(c *gin.Context) {
	hostID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	res, err := h.svc.CreateIngress(c.Request.Context(), streamID, hostID)
	if err != nil {
		writeServiceErr(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// DeleteIngress — DELETE /v1/livestream/streams/:id/ingress
//
// 200 {"data":{"status":"deleted"}}, also when there was none. The key
// stops working; the next POST issues a new one. 403 FORBIDDEN (not the
// host), 404 NOT_FOUND, 502 INGRESS_UNAVAILABLE (the ingress is kept).
func (h *Handler) DeleteIngress(c *gin.Context) {
	hostID, ok := requireUserID(c)
	if !ok {
		return
	}
	streamID, ok := requireUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteIngress(c.Request.Context(), streamID, hostID); err != nil {
		writeServiceErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"status": "deleted"}, nil)
}
