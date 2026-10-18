package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/chat-message-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TikTok parity T-25: request-shape validation of the "post" message type.
// Every rejection below happens before the service is called.

type sendCall struct {
	msgType string
	text    string
	mediaID *uuid.UUID
	postID  *uuid.UUID
}

type stubSendService struct {
	ChatService // nil: any other method panics, which no test here reaches
	calls       []sendCall
	err         error
}

func (s *stubSendService) SendMessage(_ context.Context, userID, convID uuid.UUID, msgType, text string, mediaID, postID *uuid.UUID, _ *service.ReplyRef, _ string) (*service.MessageResponse, error) {
	s.calls = append(s.calls, sendCall{msgType, text, mediaID, postID})
	if s.err != nil {
		return nil, s.err
	}
	return &service.MessageResponse{ConversationID: convID, SenderID: userID, Type: msgType, Text: text, MediaID: mediaID, PostID: postID}, nil
}

func sendRouter(svc ChatService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc, nil)
	r.POST("/conversations/:id/messages", func(c *gin.Context) {
		c.Set(userIDKey, uuid.New().String())
		h.SendMessage(c)
	})
	return r
}

func postSend(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/conversations/"+uuid.New().String()+"/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "k-"+uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSendPostMessageValidation(t *testing.T) {
	post, media := uuid.New(), uuid.New()
	long := strings.Repeat("a", 2001)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"post ok", fmt.Sprintf(`{"type":"post","post_id":%q}`, post), http.StatusOK},
		{"post with caption", fmt.Sprintf(`{"type":"post","post_id":%q,"text":"look"}`, post), http.StatusOK},
		{"post missing post_id", `{"type":"post","text":"look"}`, http.StatusBadRequest},
		{"post malformed post_id", `{"type":"post","post_id":"not-a-uuid"}`, http.StatusBadRequest},
		{"post numeric post_id", `{"type":"post","post_id":42}`, http.StatusBadRequest},
		{"post nil post_id", fmt.Sprintf(`{"type":"post","post_id":%q}`, uuid.Nil), http.StatusBadRequest},
		{"post with media_id", fmt.Sprintf(`{"type":"post","post_id":%q,"media_id":%q}`, post, media), http.StatusBadRequest},
		{"post caption too long", fmt.Sprintf(`{"type":"post","post_id":%q,"text":%q}`, post, long), http.StatusBadRequest},
		{"text with post_id", fmt.Sprintf(`{"type":"text","text":"hi","post_id":%q}`, post), http.StatusBadRequest},
		{"media with post_id", fmt.Sprintf(`{"type":"media","media_id":%q,"post_id":%q}`, media, post), http.StatusBadRequest},
		{"unknown type", `{"type":"hologram","text":"hi"}`, http.StatusBadRequest},
		{"missing type", `{"text":"hi"}`, http.StatusBadRequest},
		{"plain text still ok", `{"type":"text","text":"hi"}`, http.StatusOK},
	}
	for _, tc := range cases {
		svc := &stubSendService{}
		w := postSend(t, sendRouter(svc), tc.body)
		if w.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, w.Code, tc.want, w.Body.String())
		}
		if tc.want != http.StatusOK && len(svc.calls) != 0 {
			t.Errorf("%s: a rejected request reached the service", tc.name)
		}
	}
}

func TestSendPostMessageForwardsThePostIDAndReturnsIt(t *testing.T) {
	svc := &stubSendService{}
	post := uuid.New()
	w := postSend(t, sendRouter(svc), fmt.Sprintf(`{"type":"post","post_id":%q,"text":"look"}`, post))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if len(svc.calls) != 1 || svc.calls[0].postID == nil || *svc.calls[0].postID != post || svc.calls[0].text != "look" || svc.calls[0].mediaID != nil {
		t.Fatalf("service call = %+v", svc.calls)
	}
	var env struct {
		Data struct {
			Type   string `json:"type"`
			PostID string `json:"post_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Type != "post" || env.Data.PostID != post.String() {
		t.Fatalf("response = %s", w.Body.String())
	}
}

func TestSendPostServiceRefusalsMapLikeText(t *testing.T) {
	post := uuid.New()
	body := fmt.Sprintf(`{"type":"post","post_id":%q}`, post)
	cases := []struct {
		err  error
		want int
	}{
		{fmt.Errorf("%w: posts are not allowed until the request is accepted", service.ErrRequestFirstMessageInvalid), http.StatusUnprocessableEntity},
		{service.ErrAwaitingRequestAcceptance, http.StatusConflict},
		{service.ErrMessagingNotAllowed, http.StatusForbidden},
		{fmt.Errorf("%w: post_id is required", service.ErrInvalidMessageContent), http.StatusBadRequest},
		{errors.New("not a conversation member"), http.StatusBadRequest},
	}
	for _, tc := range cases {
		w := postSend(t, sendRouter(&stubSendService{err: tc.err}), body)
		if w.Code != tc.want {
			t.Errorf("%v: status %d, want %d", tc.err, w.Code, tc.want)
		}
	}
}

// The binding's oneof list and service.KnownMessageTypes must agree, so a new
// type cannot be accepted on the wire but unknown to the service (or the
// reverse).
func TestSendMessageBindingMatchesKnownMessageTypes(t *testing.T) {
	field, ok := reflect.TypeOf(SendMessageRequest{}).FieldByName("Type")
	if !ok {
		t.Fatal("SendMessageRequest.Type missing")
	}
	var oneof []string
	for _, rule := range strings.Split(field.Tag.Get("binding"), ",") {
		if strings.HasPrefix(rule, "oneof=") {
			oneof = strings.Fields(strings.TrimPrefix(rule, "oneof="))
		}
	}
	known := append([]string(nil), service.KnownMessageTypes...)
	sort.Strings(oneof)
	sort.Strings(known)
	if !reflect.DeepEqual(oneof, known) {
		t.Fatalf("binding oneof %v != service.KnownMessageTypes %v", oneof, known)
	}
}
