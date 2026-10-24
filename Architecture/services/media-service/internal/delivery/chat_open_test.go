package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Hidden chats: the chat authorizer, and only it, forwards the request's
// open token with its device binding to chat-service's media-access call.

type capturedAccess struct {
	bodies []map[string]string
}

func accessServer(t *testing.T, got *capturedAccess) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		got.bodies = append(got.bodies, body)
		// chat-service allows only with the open token bound to sid-1.
		allowed := body["open_token"] == "open-ok" && body["session_id"] == "sid-1"
		if !allowed {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": false})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": true})
	}))
	t.Cleanup(srv.Close)
	return srv
}

const chatViewer = "11111111-1111-4111-8111-111111111111"
const chatMedia = "22222222-2222-4222-8222-222222222222"

func TestChatAuthorizerForwardsTheOpenTokenWithTheSession(t *testing.T) {
	got := &capturedAccess{}
	chat := NewHTTPChatAuthorizer(accessServer(t, got).URL, "k", nil)

	// Without a token: chat-service is asked the plain question and says no.
	if err := chat.Authorize(context.Background(), chatViewer, chatMedia); err != ErrDeliveryDenied {
		t.Fatalf("no token: %v", err)
	}
	if _, ok := got.bodies[0]["open_token"]; ok {
		t.Fatalf("open_token sent without one: %v", got.bodies[0])
	}

	ctx := WithChatOpen(context.Background(), ChatOpen{Token: " open-ok ", SessionID: "sid-1", DeviceID: "dev-9"})
	if err := chat.Authorize(ctx, chatViewer, chatMedia); err != nil {
		t.Fatalf("with token: %v", err)
	}
	last := got.bodies[len(got.bodies)-1]
	if last["open_token"] != "open-ok" || last["session_id"] != "sid-1" || last["device_id"] != "" ||
		last["viewer_id"] != chatViewer || last["media_id"] != chatMedia {
		t.Fatalf("payload = %v", last)
	}
}

func TestChatAuthorizerFallsBackToTheDeviceID(t *testing.T) {
	got := &capturedAccess{}
	chat := NewHTTPChatAuthorizer(accessServer(t, got).URL, "k", nil)
	ctx := WithChatOpen(context.Background(), ChatOpen{Token: "open-ok", DeviceID: "dev-9"})
	_ = chat.Authorize(ctx, chatViewer, chatMedia)
	if b := got.bodies[0]; b["device_id"] != "dev-9" || b["session_id"] != "" || b["open_token"] != "open-ok" {
		t.Fatalf("payload = %v", b)
	}
}

func TestOnlyChatIsSentTheOpenToken(t *testing.T) {
	ctx := WithChatOpen(context.Background(), ChatOpen{Token: "open-ok", SessionID: "sid-1"})
	for name, build := range map[string]func(string) *HTTPContentAuthorizer{
		"post":     func(u string) *HTTPContentAuthorizer { return NewHTTPContentAuthorizer(u, "k", nil) },
		"group":    func(u string) *HTTPContentAuthorizer { return NewHTTPGroupAuthorizer(u, "k", nil) },
		"profile":  func(u string) *HTTPContentAuthorizer { return NewHTTPProfileAuthorizer(u, "k", nil) },
		"commerce": func(u string) *HTTPContentAuthorizer { return NewHTTPCommerceAuthorizer(u, "k", nil) },
	} {
		got := &capturedAccess{}
		a := build(accessServer(t, got).URL)
		_ = a.Authorize(ctx, chatViewer, chatMedia)
		_ = a.AuthorizePoster(ctx, chatViewer, chatMedia)
		for _, b := range got.bodies {
			if _, ok := b["open_token"]; ok {
				t.Fatalf("%s was sent the chat open token: %v", name, b)
			}
			if _, ok := b["session_id"]; ok {
				t.Fatalf("%s was sent the session id: %v", name, b)
			}
		}
	}
}

func TestChatOpenThroughAnyContentAuthorizer(t *testing.T) {
	// The real composition: post-service denies (it is chat media), chat
	// allows with the token.
	chatGot := &capturedAccess{}
	composite := AnyContentAuthorizer{
		denyAll{},
		NewHTTPChatAuthorizer(accessServer(t, chatGot).URL, "k", nil),
	}
	ctx := WithChatOpen(context.Background(), ChatOpen{Token: "open-ok", SessionID: "sid-1"})
	if err := composite.Authorize(ctx, chatViewer, chatMedia); err != nil {
		t.Fatalf("hidden-chat media with the token through the composite: %v", err)
	}
	if err := composite.Authorize(context.Background(), chatViewer, chatMedia); err != ErrDeliveryDenied {
		t.Fatalf("hidden-chat media without the token: %v", err)
	}
}

type denyAll struct{}

func (denyAll) Authorize(context.Context, string, string) error { return ErrDeliveryDenied }

func TestWithChatOpenIgnoresAnEmptyToken(t *testing.T) {
	if _, ok := ChatOpenFrom(WithChatOpen(context.Background(), ChatOpen{Token: "  ", SessionID: "sid-1"})); ok {
		t.Fatal("an empty token was attached")
	}
}
