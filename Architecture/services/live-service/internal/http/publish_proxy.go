package http

import (
	"errors"
	"io"
	stdhttp "net/http"
	"time"

	"github.com/atpost/live-service/internal/service"
	"github.com/atpost/live-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var publishProxyClient = &stdhttp.Client{
	Timeout: 45 * time.Second,
}

func (h *Handler) ProxyPublishWHIP(c *gin.Context) {
	setPublishCORSHeaders(c.Writer.Header())

	switch c.Request.Method {
	case stdhttp.MethodOptions:
		c.Header("Allow", "POST, OPTIONS")
		c.Status(stdhttp.StatusNoContent)
		return
	case stdhttp.MethodPost:
	default:
		c.Header("Allow", "POST, OPTIONS")
		c.String(stdhttp.StatusMethodNotAllowed, "method not allowed")
		return
	}

	hostID, ok := parseUserID(c)
	if !ok {
		return
	}
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		c.String(stdhttp.StatusBadRequest, "invalid stream id")
		return
	}

	target, err := h.svc.ResolvePublishEndpointURL(c.Request.Context(), streamID, hostID)
	if err != nil {
		writePublishError(c, err)
		return
	}
	target.RawQuery = c.Request.URL.RawQuery

	req, err := stdhttp.NewRequestWithContext(c.Request.Context(), stdhttp.MethodPost, target.String(), c.Request.Body)
	if err != nil {
		c.String(stdhttp.StatusBadGateway, "failed to prepare publish request")
		return
	}
	copyPublishHeaders(req.Header, c.Request.Header)
	req.Host = target.Host

	resp, err := publishProxyClient.Do(req)
	if err != nil {
		c.String(stdhttp.StatusBadGateway, "failed to publish stream")
		return
	}
	defer resp.Body.Close()

	copyPublishHeaders(c.Writer.Header(), resp.Header)
	c.Writer.Header().Del("Location")
	setPublishCORSHeaders(c.Writer.Header())
	c.Status(resp.StatusCode)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		_ = c.Error(err)
	}
}

func writePublishError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		c.String(stdhttp.StatusNotFound, "stream not found")
	case errors.Is(err, service.ErrPublishUnavailable):
		c.String(stdhttp.StatusConflict, "publish unavailable")
	case errors.Is(err, service.ErrPublishNotConfigured):
		c.String(stdhttp.StatusServiceUnavailable, "publish origin is not configured")
	default:
		c.String(stdhttp.StatusBadGateway, "failed to resolve publish target")
	}
}

func setPublishCORSHeaders(headers stdhttp.Header) {
	headers.Set("Access-Control-Allow-Origin", "*")
	headers.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	headers.Set("Access-Control-Allow-Headers", "Accept, Authorization, Origin, Content-Type, X-Requested-With, X-CSRF-Token")
	headers.Set("Access-Control-Expose-Headers", "Content-Type")
}

func copyPublishHeaders(dst, src stdhttp.Header) {
	dst.Del("Content-Length")
	copyProxyHeaders(dst, src)
}
