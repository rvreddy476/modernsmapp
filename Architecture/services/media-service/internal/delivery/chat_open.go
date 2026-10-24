package delivery

import (
	"context"
	"strings"
)

// Hidden chats (chat-service, contract pinned 24 Oct 2026).
//
// Media that rides only conversations the viewer hid is allowed by
// chat-service's /internal/v1/chat/media-access only with the viewer's open
// token, bound to the device it was issued to. An <img>/<video> URL cannot
// send headers, so the web client puts the token on the media URL as the
// `chat_open` query parameter; the HTTP layer lifts it off the request (and
// off the URL, so nothing downstream logs it) into the request context, and
// the CHAT authorizer — and only it — forwards it with the device binding:
//
//	open_token  the chat_open value
//	session_id  the `sid` claim of the viewer's verified access token, or
//	device_id   the viewer's X-Device-Id when the token carries no sid
//
// post-, group-, profile- and commerce-service are never sent it: the token
// is chat's secret, not theirs.

// MaxChatOpenTokenLength bounds the chat_open value accepted from a URL.
const MaxChatOpenTokenLength = 512

// ChatOpen is one request's hidden-chat open token and its device binding.
type ChatOpen struct {
	Token     string
	SessionID string
	DeviceID  string
}

type chatOpenKey struct{}

// WithChatOpen attaches the open token to ctx. An empty token attaches
// nothing.
func WithChatOpen(ctx context.Context, open ChatOpen) context.Context {
	open.Token = strings.TrimSpace(open.Token)
	if open.Token == "" {
		return ctx
	}
	open.SessionID = strings.TrimSpace(open.SessionID)
	open.DeviceID = strings.TrimSpace(open.DeviceID)
	return context.WithValue(ctx, chatOpenKey{}, open)
}

// ChatOpenFrom returns the request's open token, if any.
func ChatOpenFrom(ctx context.Context) (ChatOpen, bool) {
	open, ok := ctx.Value(chatOpenKey{}).(ChatOpen)
	return open, ok && open.Token != ""
}

// chatMediaAccessPath is chat-service's media-access route.
const chatMediaAccessPath = "/internal/v1/chat/media-access"

// addChatOpen puts the open token and its device binding on a chat
// media-access payload. Any other authority's payload is left alone.
func addChatOpen(ctx context.Context, path string, payload map[string]string) {
	if path != chatMediaAccessPath {
		return
	}
	open, ok := ChatOpenFrom(ctx)
	if !ok {
		return
	}
	payload["open_token"] = open.Token
	switch {
	case open.SessionID != "":
		payload["session_id"] = open.SessionID
	case open.DeviceID != "":
		payload["device_id"] = open.DeviceID
	}
}
