package http

import (
	"errors"
	"io"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/atpost/live-service/internal/service"
	"github.com/atpost/live-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var playbackProxyClient = &stdhttp.Client{
	Timeout: 30 * time.Second,
}

var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Proxy-Connection":    {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func (h *Handler) ProxyPlayback(c *gin.Context) {
	setPlaybackCORSHeaders(c.Writer.Header())

	switch c.Request.Method {
	case stdhttp.MethodOptions:
		c.Header("Allow", "GET, HEAD, OPTIONS")
		c.Status(stdhttp.StatusNoContent)
		return
	case stdhttp.MethodGet, stdhttp.MethodHead:
	default:
		c.Header("Allow", "GET, HEAD, OPTIONS")
		c.String(stdhttp.StatusMethodNotAllowed, "method not allowed")
		return
	}

	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil {
		c.String(stdhttp.StatusBadRequest, "invalid stream id")
		return
	}

	target, err := h.svc.ResolvePlaybackAssetURL(c.Request.Context(), streamID, c.Param("asset"))
	if err != nil {
		writePlaybackError(c, err)
		return
	}
	target.RawQuery = c.Request.URL.RawQuery

	req, err := stdhttp.NewRequestWithContext(c.Request.Context(), c.Request.Method, target.String(), nil)
	if err != nil {
		c.String(stdhttp.StatusBadGateway, "failed to prepare playback request")
		return
	}
	copyProxyHeaders(req.Header, c.Request.Header)
	req.Host = target.Host

	resp, err := playbackProxyClient.Do(req)
	if err != nil {
		c.String(stdhttp.StatusBadGateway, "failed to load playback asset")
		return
	}
	defer resp.Body.Close()

	copyProxyHeaders(c.Writer.Header(), resp.Header)
	setPlaybackCORSHeaders(c.Writer.Header())
	c.Status(resp.StatusCode)
	if c.Request.Method == stdhttp.MethodHead {
		return
	}
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		_ = c.Error(err)
	}
}

func writePlaybackError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		c.String(stdhttp.StatusNotFound, "stream not found")
	case errors.Is(err, service.ErrInvalidPlaybackAssetPath):
		c.String(stdhttp.StatusBadRequest, "invalid playback asset path")
	case errors.Is(err, service.ErrPlaybackUnavailable):
		c.String(stdhttp.StatusConflict, "playback unavailable")
	case errors.Is(err, service.ErrPlaybackNotConfigured):
		c.String(stdhttp.StatusServiceUnavailable, "playback origin is not configured")
	default:
		c.String(stdhttp.StatusBadGateway, "failed to resolve playback asset")
	}
}

func setPlaybackCORSHeaders(headers stdhttp.Header) {
	headers.Set("Access-Control-Allow-Origin", "*")
	headers.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	headers.Set("Access-Control-Allow-Headers", "Accept, Origin, Range, Content-Type")
	headers.Set("Access-Control-Expose-Headers", "Accept-Ranges, Content-Length, Content-Range, Content-Type")
}

func copyProxyHeaders(dst, src stdhttp.Header) {
	connectionTokens := parseConnectionTokens(src.Values("Connection"))
	for key, values := range src {
		canonical := stdhttp.CanonicalHeaderKey(key)
		if _, skip := hopByHopHeaders[canonical]; skip {
			continue
		}
		if _, skip := connectionTokens[canonical]; skip {
			continue
		}
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func parseConnectionTokens(values []string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			tokens[stdhttp.CanonicalHeaderKey(token)] = struct{}{}
		}
	}
	return tokens
}
