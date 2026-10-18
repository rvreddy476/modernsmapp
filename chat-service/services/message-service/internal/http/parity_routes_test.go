package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/service"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Chat parity route inventory: every CONTRACT route is registered, the
// internal sticker routes are NOT reachable under /v1/chat (the gateway
// proxies /v1/chat), and nothing parity-shaped is registered unclassified.
var parityRoutes = map[string]bool{
	"GET /v1/chat/conversations":                                   true,
	"GET /v1/chat/conversations/:id":                               true,
	"PUT /v1/chat/conversations/:id/settings":                      true,
	"DELETE /v1/chat/conversations/:id/for-me":                     true,
	"POST /v1/chat/conversations/:id/clear":                        true,
	"GET /v1/chat/conversations/:id/shared":                        true,
	"POST /v1/chat/conversations/:id/messages":                     true,
	"DELETE /v1/chat/conversations/:id/messages/:messageId":        true,
	"POST /v1/chat/conversations/:id/messages/:messageId/forward":  true,
	"GET /v1/chat/conversations/:id/messages/:messageId/reactions": true,
	"POST /v1/chat/conversations/:id/messages/:messageId/report":   true,
	"GET /v1/chat/conversations/:id/read-summary":                  true,
	"POST /v1/chat/conversations/:id/typing":                       true,
	"GET /v1/chat/stickers":                                        true,
	"PUT /v1/chat/notes/me":                                        true,
	"DELETE /v1/chat/notes/me":                                     true,
	"GET /v1/chat/notes":                                           true,
	"GET /v1/chat/search":                                          true,
	"GET /v1/chat/requests":                                        true,
	"PUT /v1/chat/conversations/:id/unread":                        true,
	"POST /v1/chat/conversations/:id/report":                       true,
	"POST /internal/v1/chat/stickers/packs":                        true,
	"POST /internal/v1/chat/stickers":                              true,
}

func TestParityRouteInventory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(nil, nil).RegisterRoutes(router)
	registered := map[string]bool{}
	for _, r := range router.Routes() {
		registered[r.Method+" "+r.Path] = true
		if strings.HasPrefix(r.Path, "/v1/chat/") && strings.Contains(r.Path, "stickers/packs") {
			t.Fatalf("sticker admin route is reachable through the gateway prefix: %s %s", r.Method, r.Path)
		}
		if r.Path == "/v1/chat/gifs" {
			t.Fatal("GIF search must not exist server-side (Giphy terms; founder decision 18 Oct)")
		}
		if r.Method == http.MethodPost && strings.HasPrefix(r.Path, "/v1/chat/stickers") {
			t.Fatalf("sticker writes must be internal-only: %s", r.Path)
		}
	}
	for route := range parityRoutes {
		if !registered[route] {
			t.Errorf("parity route not registered: %s", route)
		}
	}
}

type (
	storeSticker = store.Sticker
	storeNote    = store.Note
)

// --- the parity routes reach the parity service with the right inputs ---

type stubParity struct {
	ChatService
	sent      []service.SendInput
	deleteErr error
	cleared   []string
}

func (s *stubParity) ListInbox(context.Context, uuid.UUID, string, int, *service.ConversationCursor) ([]service.ConversationResponse, *service.ConversationCursor, error) {
	return nil, nil, nil
}
func (s *stubParity) DeleteConversationForMe(_ context.Context, _, conv uuid.UUID) error {
	s.cleared = append(s.cleared, "hide:"+conv.String())
	return s.deleteErr
}
func (s *stubParity) ClearConversationForMe(_ context.Context, _, conv uuid.UUID) error {
	s.cleared = append(s.cleared, "clear:"+conv.String())
	return s.deleteErr
}
func (s *stubParity) ListShared(context.Context, uuid.UUID, uuid.UUID, string, *service.SharedCursor, int) (interface{}, *service.SharedCursor, error) {
	return []service.SharedPost{}, nil, nil
}
func (s *stubParity) SendRich(_ context.Context, userID, convID uuid.UUID, in service.SendInput) (*service.MessageResponse, error) {
	s.sent = append(s.sent, in)
	return &service.MessageResponse{ConversationID: convID, SenderID: userID, Type: in.Type}, nil
}
func (s *stubParity) DeleteMessageForMe(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, *time.Time) error {
	return s.deleteErr
}
func (s *stubParity) LocateMessage(context.Context, uuid.UUID, uuid.UUID) (string, time.Time, error) {
	return "", time.Time{}, service.ErrMessageNotFound
}
func (s *stubParity) ForwardMessage(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, *time.Time, []uuid.UUID, string) (*service.ForwardResponse, error) {
	return &service.ForwardResponse{}, nil
}
func (s *stubParity) ListReactions(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, *time.Time) ([]service.ReactionGroup, error) {
	return nil, service.ErrNotMember
}
func (s *stubParity) ReportMessage(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, *time.Time, string, string) error {
	return nil
}
func (s *stubParity) ReadSummaryFor(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, *time.Time) (*service.ReadSummary, error) {
	return &service.ReadSummary{SeenBy: []service.SeenBy{}}, nil
}
func (s *stubParity) ListStickers(context.Context, uuid.UUID) (*service.StickerCatalogue, error) {
	return &service.StickerCatalogue{Packs: []service.StickerPackOut{}}, nil
}
func (s *stubParity) UpsertStickerPackAdmin(context.Context, uuid.UUID, string, int, bool) (uuid.UUID, error) {
	return uuid.New(), nil
}
func (s *stubParity) UpsertStickerAdmin(context.Context, storeSticker, bool) (uuid.UUID, error) {
	return uuid.New(), nil
}
func (s *stubParity) PutMyNote(context.Context, uuid.UUID, string, string) (*storeNote, error) {
	return nil, service.ErrNoteRejected
}
func (s *stubParity) DeleteMyNote(context.Context, uuid.UUID) error { return nil }
func (s *stubParity) NotesFeed(context.Context, uuid.UUID) (*service.NotesFeed, error) {
	return &service.NotesFeed{Others: []service.NoteFeedItem{}}, nil
}
func (s *stubParity) SearchChat(context.Context, uuid.UUID, string, string) (*service.ChatSearchResult, error) {
	return nil, service.ErrSearchQueryTooShort
}
func (s *stubParity) ListRequestsFiltered(context.Context, uuid.UUID, string, int, int) ([]service.RequestRow, error) {
	return []service.RequestRow{}, nil
}
func (s *stubParity) ListRequestsDirected(context.Context, uuid.UUID, string, string, int, int) ([]service.RequestRow, error) {
	return []service.RequestRow{}, nil
}
func (s *stubParity) SetTypingState(context.Context, uuid.UUID, uuid.UUID, bool) error { return nil }

func parityRouter(svc ChatService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if u := c.GetHeader("X-Test-User"); u != "" {
			c.Set(userIDKey, u)
		}
		c.Next()
	})
	New(svc, nil).WithInternalServiceKey("k").RegisterRoutes(r)
	return r
}

func callParity(r http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", uuid.NewString())
	req.Header.Set("Idempotency-Key", "k-"+uuid.NewString())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSendRichFieldsReachTheService(t *testing.T) {
	stub := &stubParity{}
	r := parityRouter(stub)
	conv := "/v1/chat/conversations/" + uuid.NewString() + "/messages"
	note, sticker, user := uuid.New(), uuid.New(), uuid.New()
	w := callParity(r, "POST", conv, fmt.Sprintf(`{"type":"voice","media_id":%q,"duration_ms":900,"waveform":[1,2,3]}`, uuid.New()), nil)
	if w.Code != 200 || len(stub.sent) != 1 || stub.sent[0].DurationMs != 900 || len(stub.sent[0].Waveform) != 3 {
		t.Fatalf("voice: %d %s %+v", w.Code, w.Body, stub.sent)
	}
	callParity(r, "POST", conv, fmt.Sprintf(`{"type":"sticker","sticker_id":%q}`, sticker), nil)
	callParity(r, "POST", conv, fmt.Sprintf(`{"type":"profile","user_id":%q}`, user), nil)
	callParity(r, "POST", conv, fmt.Sprintf(`{"type":"text","text":"hi","reply_note_id":%q}`, note), nil)
	gif := `{"type":"gif","gif":{"provider":"giphy","id":"abc","url":"https://i.giphy.com/a.gif?x=1&y=2","width":10,"height":20,"still_url":"https://i.giphy.com/a_s.gif"}}`
	callParity(r, "POST", conv, gif, nil)
	if len(stub.sent) != 5 || *stub.sent[1].StickerID != sticker || *stub.sent[2].ProfileUserID != user ||
		*stub.sent[3].ReplyNoteID != note || stub.sent[4].GIF == nil || stub.sent[4].GIF.URL != "https://i.giphy.com/a.gif?x=1&y=2" || stub.sent[4].GIF.StillURL == "" {
		t.Fatalf("rich fields lost: %+v", stub.sent)
	}
	if w := callParity(r, "POST", conv, fmt.Sprintf(`{"type":"voice","media_id":%q,"duration_ms":9,"waveform":[%s]}`, uuid.New(), strings.TrimSuffix(strings.Repeat("1,", 65), ",")), nil); w.Code != 400 {
		t.Fatalf("65-value waveform: %d", w.Code)
	}
	if w := callParity(r, "POST", conv, fmt.Sprintf(`{"type":"text","text":"x","reply_note_id":%q,"reply_to_id":%q}`, note, uuid.New()), nil); w.Code != 400 {
		t.Fatalf("note + message reply: %d", w.Code)
	}
}

func TestParityHandlersMapErrors(t *testing.T) {
	stub := &stubParity{deleteErr: service.ErrNotMember}
	r := parityRouter(stub)
	conv := "/v1/chat/conversations/" + uuid.NewString()
	if w := callParity(r, "DELETE", conv+"/for-me", "", nil); w.Code != 404 {
		t.Fatalf("hide as non-member: %d", w.Code)
	}
	stub.deleteErr = nil
	if w := callParity(r, "DELETE", conv+"/for-me", "", nil); w.Code != 204 {
		t.Fatalf("hide: %d", w.Code)
	}
	if w := callParity(r, "POST", conv+"/clear", "", nil); w.Code != 204 || len(stub.cleared) != 3 || !strings.HasPrefix(stub.cleared[2], "clear:") {
		t.Fatalf("clear: %d %v", w.Code, stub.cleared)
	}
	if w := callParity(r, "GET", conv+"/messages/"+uuid.NewString()+"/reactions", "", nil); w.Code != 404 {
		t.Fatalf("reactions as non-member: %d", w.Code)
	}
	if w := callParity(r, "PUT", "/v1/chat/notes/me", `{"text":"x"}`, nil); w.Code != 422 {
		t.Fatalf("rejected note: %d", w.Code)
	}
	if w := callParity(r, "GET", "/v1/chat/search?q=a", "", nil); w.Code != 400 {
		t.Fatalf("short search: %d", w.Code)
	}
	if w := callParity(r, "POST", conv+"/messages/"+uuid.NewString()+"/report", `{"reason":"spam"}`, nil); w.Code != 202 {
		t.Fatalf("report: %d", w.Code)
	}
	if w := callParity(r, "DELETE", conv+"/messages/"+uuid.NewString(), `{"scope":"all"}`, nil); w.Code != 400 {
		t.Fatalf("bad delete scope: %d", w.Code)
	}
	if w := callParity(r, "DELETE", conv+"/messages/"+uuid.NewString(), `{}`, nil); w.Code != 404 {
		t.Fatalf("unsend of an unknown message without ts: %d", w.Code)
	}
	// Sticker admin: three gates.
	body := `{"name":"pack"}`
	if w := callParity(r, "POST", "/internal/v1/chat/stickers/packs", body, map[string]string{"X-User-Id": uuid.NewString(), "X-Internal-Service-Key": "k"}); w.Code != 403 {
		t.Fatalf("admin with user identity: %d", w.Code)
	}
}

func TestStickerAdminGates(t *testing.T) {
	r := parityRouter(&stubParity{})
	send := func(key string) int {
		req := httptest.NewRequest("POST", "/internal/v1/chat/stickers/packs", strings.NewReader(`{"name":"pack"}`))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if c := send(""); c != 401 {
		t.Fatalf("no key: %d", c)
	}
	if c := send("wrong"); c != 401 {
		t.Fatalf("wrong key: %d", c)
	}
	if c := send("k"); c != 200 {
		t.Fatalf("right key: %d", c)
	}
	unconfigured := gin.New()
	New(&stubParity{}, nil).RegisterRoutes(unconfigured)
	req := httptest.NewRequest("POST", "/internal/v1/chat/stickers/packs", strings.NewReader(`{"name":"pack"}`))
	req.Header.Set("X-Internal-Service-Key", "")
	w := httptest.NewRecorder()
	unconfigured.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatalf("unconfigured key must fail closed: %d", w.Code)
	}
}

// The golden fixtures are written by the integration suite from real
// handler output; here every one must exist and be a {"data": ...} envelope.
func TestContractFixturesExist(t *testing.T) {
	names := []string{"conversations_list", "conversation_detail", "message_voice", "message_sticker", "message_gif",
		"message_profile", "message_sound", "message_live", "message_media", "message_note_reply", "forward_result",
		"reactions_list", "read_summary", "notes_feed", "stickers", "search_result", "shared_posts", "shared_media",
		"requests_filtered",
		// fix pass 2
		"message_gif_sticker", "requests_list", "request_detail", "conversation_marked_unread", "message_media_sensitive"}
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join("testdata", "contracts", "chat", n+".json"))
		if err != nil {
			t.Errorf("fixture %s: %v", n, err)
			continue
		}
		var env map[string]json.RawMessage
		if err := json.Unmarshal(raw, &env); err != nil || env["data"] == nil {
			t.Errorf("fixture %s is not a data envelope", n)
		}
	}
}
