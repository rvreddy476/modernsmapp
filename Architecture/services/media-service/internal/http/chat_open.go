package http

import (
	"context"
	"net/url"
	"strings"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Hidden chats (2026-10-24): the `chat_open` query parameter on the media
// read routes (delivery/chat_open.go has the contract).
//
// chatOpenContext runs first on every /v1/media route. When the request
// names chat_open it:
//
//   - takes the parameter OFF the request URL (URL.RawQuery and RequestURI),
//     so no access log, error message or downstream reader ever sees it;
//   - for a signed-in viewer, binds it to the device: the `sid` of the
//     viewer's own verified access token (Authorization or the access_token
//     cookie, whose subject must be the gateway's X-User-Id), else the
//     X-Device-Id the gateway stamped from that token;
//   - puts it in the request context, where only the chat authorizer reads
//     it and forwards it to chat-service's media-access call.
//
// A signed-out request, an empty value or one longer than
// delivery.MaxChatOpenTokenLength carries nothing on: chat-service then
// answers as it would without a token. The value is never logged here.

// ChatOpenQueryParam is the query parameter a hidden chat's media URL carries.
const ChatOpenQueryParam = "chat_open"

// WithSessionKeys gives the handler the access-token keys it verifies a
// viewer's `sid` with (hidden-chat media, chat_open.go). Without them a
// chat_open is bound by X-Device-Id alone.
func (h *Handler) WithSessionKeys(keys JWTKeySet) *Handler {
	h.sessionKeys = &keys
	return h
}

func (h *Handler) chatOpenContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, present := takeChatOpen(c)
		if !present {
			c.Next()
			return
		}
		viewer := deliveryViewer(c)
		if token == "" || len(token) > delivery.MaxChatOpenTokenLength || viewer == uuid.Nil {
			c.Next()
			return
		}
		open := delivery.ChatOpen{Token: token, SessionID: h.viewerSessionID(c, viewer)}
		if open.SessionID == "" {
			open.DeviceID = c.GetHeader("X-Device-Id")
		}
		c.Request = c.Request.WithContext(delivery.WithChatOpen(c.Request.Context(), open))
		c.Next()
	}
}

// takeChatOpen removes chat_open from the request URL and returns its value.
func takeChatOpen(c *gin.Context) (string, bool) {
	if c.Request == nil || c.Request.URL == nil || !strings.Contains(c.Request.URL.RawQuery, ChatOpenQueryParam) {
		return "", false
	}
	q := c.Request.URL.Query()
	values, present := q[ChatOpenQueryParam]
	if !present {
		return "", false
	}
	q.Del(ChatOpenQueryParam)
	c.Request.URL.RawQuery = q.Encode()
	c.Request.RequestURI = c.Request.URL.RequestURI()
	if len(values) == 0 {
		return "", true
	}
	return strings.TrimSpace(values[0]), true
}

// viewerSessionID is the `sid` of the viewer's own verified access token, or
// "" when there is none, it does not verify, or it names someone else.
func (h *Handler) viewerSessionID(c *gin.Context, viewer uuid.UUID) string {
	if h.sessionKeys == nil {
		return ""
	}
	raw := extractToken(c)
	if raw == "" {
		return ""
	}
	claims, err := verifyJWTClaims(raw, *h.sessionKeys)
	if err != nil || claims.subject() != viewer.String() {
		return ""
	}
	return strings.TrimSpace(claims.Sid)
}

// carryChatOpen appends the request's chat_open to every child-playlist URI
// of an HLS master playlist (/v1/media/<id>/hls/<name>), so a hidden chat's
// video keeps its open token past the master. Segment URIs are signed object
// URLs and need nothing. Without a chat_open the body is returned unchanged.
func carryChatOpen(c *gin.Context, mediaID uuid.UUID, body []byte) []byte {
	open, ok := delivery.ChatOpenFrom(c.Request.Context())
	if !ok {
		return body
	}
	prefix := "/v1/media/" + mediaID.String() + "/hls/"
	suffix := "?" + ChatOpenQueryParam + "=" + url.QueryEscape(open.Token)
	lines := strings.Split(string(body), "\n")
	changed := false
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = line + suffix
			changed = true
		}
	}
	if !changed {
		return body
	}
	return []byte(strings.Join(lines, "\n"))
}

// hlsPlaylistService is the HLS playlist read; *service.Service satisfies it.
type hlsPlaylistService interface {
	GetHLSPlaylist(ctx context.Context, viewerID, mediaID uuid.UUID, playlist string) ([]byte, error)
}

func (h *Handler) hlsSvc() hlsPlaylistService {
	if h.hlsPlaylists != nil {
		return h.hlsPlaylists
	}
	return h.svc
}
