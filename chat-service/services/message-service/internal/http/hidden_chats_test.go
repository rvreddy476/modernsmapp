package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/service"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/atpost/chat-shared/accessauth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Hidden chats: the open-token gate, the hidden routes' wire shapes, the
// step-up rule on reset, and the route inventory.

type stubHidden struct {
	*stubParity
	locked      map[string]bool // conv|user
	goodOpen    string
	goodDevice  string
	revealCalls int
	seenDevice  string
	fresh       []bool
	msgReads    int
	mediaOK     bool
	lockedOK    bool
}

func newStubHidden() *stubHidden {
	return &stubHidden{stubParity: &stubParity{}, locked: map[string]bool{}, goodOpen: "open-ok", goodDevice: "sid:s1"}
}

func lk(conv, user uuid.UUID) string { return conv.String() + "|" + user.String() }

func (s *stubHidden) GetHiddenChatSettings(context.Context, uuid.UUID) (*service.HiddenChatSettingsView, error) {
	return &service.HiddenChatSettingsView{Configured: true}, nil
}
func (s *stubHidden) PutHiddenChatSettings(_ context.Context, _ service.HiddenAttempt, in service.HiddenSettingsInput) (*service.HiddenChatSettingsView, error) {
	v := &service.HiddenChatSettingsView{Configured: true}
	if in.Shortcut != nil && *in.Shortcut != "" {
		v.Shortcut = in.Shortcut
	}
	return v, nil
}
func (s *stubHidden) ResetHiddenChats(_ context.Context, _ uuid.UUID, fresh bool, _ service.HiddenResetInput) (*service.HiddenResetResult, error) {
	s.fresh = append(s.fresh, fresh)
	if !fresh {
		return nil, service.ErrStepUpRequired
	}
	return &service.HiddenResetResult{}, nil
}
func (s *stubHidden) RevealHiddenChats(_ context.Context, who service.HiddenAttempt, key string) (*service.HiddenToken, error) {
	s.revealCalls++
	s.seenDevice = who.DeviceKey
	if key == "Locked99" {
		return nil, &service.HiddenLockedError{RetryAfter: 90*time.Second + time.Millisecond}
	}
	if key != "Mango42" {
		return nil, service.ErrHiddenNotFound
	}
	return &service.HiddenToken{Token: "reveal-ok", ExpiresAt: time.Now().Add(15 * time.Minute)}, nil
}
func (s *stubHidden) OpenHiddenChats(_ context.Context, _ service.HiddenAttempt, reveal, pw string) (*service.HiddenToken, error) {
	if reveal != "reveal-ok" {
		return nil, service.ErrHiddenNotFound
	}
	if pw != "river-stone" {
		return nil, &service.HiddenWrongPasswordError{AttemptsLeft: 3}
	}
	return &service.HiddenToken{Token: s.goodOpen, ExpiresAt: time.Now().Add(15 * time.Minute)}, nil
}
func (s *stubHidden) LockHiddenChats(context.Context, uuid.UUID, string) error { return nil }
func (s *stubHidden) ListHiddenConversations(_ context.Context, _ uuid.UUID, _ string, reveal string, _ int, _ *service.ConversationCursor) ([]service.ConversationResponse, *service.ConversationCursor, time.Time, error) {
	if reveal != "reveal-ok" {
		return nil, nil, time.Time{}, service.ErrHiddenNotFound
	}
	return []service.ConversationResponse{{ID: uuid.New(), Locked: true}}, nil, time.Now(), nil
}
func (s *stubHidden) HideConversation(_ context.Context, u, c uuid.UUID) error {
	s.locked[lk(c, u)] = true
	return nil
}
func (s *stubHidden) UnhideConversation(_ context.Context, u, c uuid.UUID) error {
	delete(s.locked, lk(c, u))
	return nil
}
func (s *stubHidden) IsConversationLockedFor(_ context.Context, u, c uuid.UUID) (bool, error) {
	return s.locked[lk(c, u)], nil
}
func (s *stubHidden) LockedConversationsAmong(_ context.Context, u uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		if s.locked[lk(id, u)] {
			out[id] = true
		}
	}
	return out, nil
}
func (s *stubHidden) ValidateOpenToken(_ context.Context, _ uuid.UUID, device, token string) (time.Time, bool, error) {
	s.seenDevice = device
	return time.Now().Add(15 * time.Minute), token == s.goodOpen && device == s.goodDevice, nil
}
func (s *stubHidden) ViewerMayAccessLockedChatMedia(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return s.lockedOK, nil
}

// ChatService methods the gated routes reach.
func (s *stubHidden) GetMessages(context.Context, uuid.UUID, uuid.UUID, *scylla.MessageCursor, int) ([]service.MessageResponse, *scylla.MessageCursor, error) {
	s.msgReads++
	return []service.MessageResponse{}, nil, nil
}
func (s *stubHidden) IssueSubscriptionEntitlement(_ context.Context, _, conv uuid.UUID) (*service.SubscriptionEntitlement, error) {
	return &service.SubscriptionEntitlement{Token: "ent", ConversationID: conv.String()}, nil
}
func (s *stubHidden) ViewerMayAccessChatMedia(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return s.mediaOK, nil
}
func (s *stubHidden) CreateDirectConversation(_ context.Context, _, _ uuid.UUID, _ string) (*service.ConversationResponse, error) {
	return &service.ConversationResponse{ID: hiddenDM, LastMessagePreview: "secret plans", UnreadCount: 3, HasUnread: true}, nil
}

var hiddenDM = uuid.MustParse("0d000000-0000-4000-8000-0000000000dd")

const hiddenTestSID = "s1"

// hiddenRouter authenticates as X-Test-User with session id s1 (the device).
func hiddenRouter(svc ChatService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if u := c.GetHeader("X-Test-User"); u != "" {
			c.Set(userIDKey, u)
			c.Set(accessIdentityKey, accessIdentityForTest(u, hiddenTestSID))
		}
		c.Next()
	})
	New(svc, slog.Default()).WithInternalServiceKey("k").RegisterRoutes(r)
	return r
}

func callAs(r http.Handler, user uuid.UUID, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", user.String())
	req.Header.Set("Idempotency-Key", "k-"+uuid.NewString())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code, body.Error.Details
}

func TestOpenTokenIsRequiredOnEveryLockedConversationRoute(t *testing.T) {
	svc := newStubHidden()
	r := hiddenRouter(svc)
	hider, other := uuid.New(), uuid.New()
	conv := uuid.New()
	svc.locked[lk(conv, hider)] = true
	base := "/v1/chat/conversations/" + conv.String()

	routes := []struct{ method, path, body string }{
		{http.MethodGet, base + "/messages", ""},
		{http.MethodPost, base + "/messages", `{"type":"text","text":"hi"}`},
		{http.MethodPost, base + "/subscription", `{}`},
		{http.MethodGet, base, ""},
		{http.MethodGet, base + "/shared?kind=media", ""},
		{http.MethodPost, base + "/typing", `{"is_typing":true}`},
		{http.MethodPost, base + "/read", `{}`},
		{http.MethodPost, base + "/unhide", ``},
		{http.MethodGet, base + "/pins", ""},
	}
	for _, rt := range routes {
		w := callAs(r, hider, rt.method, rt.path, rt.body, nil)
		if code, _ := errCode(t, w); w.Code != http.StatusForbidden || code != "CHAT_LOCKED" {
			t.Errorf("%s %s without X-Chat-Open: %d %s", rt.method, rt.path, w.Code, code)
		}
		// A token for ANOTHER device does not open it either.
		svc.goodDevice = "sid:other"
		w = callAs(r, hider, rt.method, rt.path, rt.body, map[string]string{"X-Chat-Open": "open-ok"})
		svc.goodDevice = "sid:" + hiddenTestSID
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s with another device's token: %d", rt.method, rt.path, w.Code)
		}
	}
	if svc.msgReads != 0 {
		t.Fatal("a locked conversation's messages were read without an open token")
	}
	// With the open token the gate stands aside (and reports the slid expiry).
	w := callAs(r, hider, http.MethodGet, base+"/messages", "", map[string]string{"X-Chat-Open": "open-ok"})
	if w.Code != http.StatusOK || svc.msgReads != 1 || w.Header().Get("X-Chat-Open-Expires-At") == "" {
		t.Fatalf("with open token: %d reads=%d", w.Code, svc.msgReads)
	}
	if svc.seenDevice != "sid:"+hiddenTestSID {
		t.Fatalf("token checked against device %q, want the session id", svc.seenDevice)
	}
	// The OTHER member is unaffected.
	if w := callAs(r, other, http.MethodGet, base+"/messages", "", nil); w.Code != http.StatusOK {
		t.Fatalf("other member blocked: %d", w.Code)
	}
	// Hide itself needs no token (idempotent on an already hidden chat).
	if w := callAs(r, hider, http.MethodPost, base+"/hide", "", nil); w.Code != http.StatusOK {
		t.Fatalf("hide: %d", w.Code)
	}
}

func TestUnhideNeedsTheOpenToken(t *testing.T) {
	svc := newStubHidden()
	r := hiddenRouter(svc)
	u, conv := uuid.New(), uuid.New()
	svc.locked[lk(conv, u)] = true
	path := "/v1/chat/conversations/" + conv.String() + "/unhide"
	if w := callAs(r, u, http.MethodPost, path, "", nil); w.Code != http.StatusForbidden || !svc.locked[lk(conv, u)] {
		t.Fatalf("unhide without token: %d (still locked %v)", w.Code, svc.locked[lk(conv, u)])
	}
	if w := callAs(r, u, http.MethodPost, path, "", map[string]string{"X-Chat-Open": "open-ok"}); w.Code != http.StatusOK || svc.locked[lk(conv, u)] {
		t.Fatalf("unhide with token: %d", w.Code)
	}
}

func TestForwardIntoAHiddenChatNeedsTheOpenToken(t *testing.T) {
	svc := newStubHidden()
	r := hiddenRouter(svc)
	u, src, target := uuid.New(), uuid.New(), uuid.New()
	svc.locked[lk(target, u)] = true
	path := "/v1/chat/conversations/" + src.String() + "/messages/" + uuid.NewString() + "/forward"
	body := `{"conversation_ids":["` + target.String() + `"]}`
	if w := callAs(r, u, http.MethodPost, path, body, nil); w.Code != http.StatusForbidden {
		t.Fatalf("forward into hidden chat without token: %d", w.Code)
	}
	if w := callAs(r, u, http.MethodPost, path, body, map[string]string{"X-Chat-Open": "open-ok"}); w.Code != http.StatusOK {
		t.Fatalf("forward with token: %d %s", w.Code, w.Body.String())
	}
}

func TestOpeningAHiddenDMReturnsItRedacted(t *testing.T) {
	svc := newStubHidden()
	r := hiddenRouter(svc)
	u := uuid.New()
	svc.locked[lk(hiddenDM, u)] = true
	w := callAs(r, u, http.MethodPost, "/v1/chat/conversations/direct", `{"other_user_id":"`+uuid.NewString()+`"}`, nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "secret plans") || !strings.Contains(w.Body.String(), `"locked":true`) {
		t.Fatalf("redaction: %d %s", w.Code, w.Body.String())
	}
	w = callAs(r, u, http.MethodPost, "/v1/chat/conversations/direct", `{"other_user_id":"`+uuid.NewString()+`"}`, map[string]string{"X-Chat-Open": "open-ok"})
	if !strings.Contains(w.Body.String(), "secret plans") {
		t.Fatalf("with open token the row is whole: %s", w.Body.String())
	}
}

func TestLockedChatMediaNeedsTheOpenToken(t *testing.T) {
	svc := newStubHidden()
	svc.lockedOK = true // the media rides only a hidden chat
	r := hiddenRouter(svc)
	viewer, media := uuid.New(), uuid.New()
	call := func(extra string) int {
		req := httptest.NewRequest(http.MethodPost, "/internal/v1/chat/media-access",
			strings.NewReader(`{"viewer_id":"`+viewer.String()+`","media_id":"`+media.String()+`"`+extra+`}`))
		req.Header.Set("X-Internal-Service-Key", "k")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if code := call(""); code != http.StatusForbidden {
		t.Fatalf("hidden-chat media without open token: %d", code)
	}
	if code := call(`,"open_token":"open-ok","session_id":"elsewhere"`); code != http.StatusForbidden {
		t.Fatalf("hidden-chat media with another device's token: %d", code)
	}
	if code := call(`,"open_token":"open-ok","session_id":"` + hiddenTestSID + `"`); code != http.StatusOK {
		t.Fatalf("hidden-chat media with the open token: %d", code)
	}
}

func TestHiddenRouteWireShapes(t *testing.T) {
	svc := newStubHidden()
	r := hiddenRouter(svc)
	u := uuid.New()

	// Wrong key and not-configured: one 404 NOT_FOUND.
	w := callAs(r, u, http.MethodPost, "/v1/chat/hidden/reveal", `{"secret_key":"Papaya99"}`, nil)
	if code, _ := errCode(t, w); w.Code != http.StatusNotFound || code != "NOT_FOUND" {
		t.Fatalf("wrong key: %d %s", w.Code, code)
	}
	wrongBody := w.Body.String()
	// A search-shaped string that cannot be a key: same answer, no attempt.
	w = callAs(r, u, http.MethodPost, "/v1/chat/hidden/reveal", `{"secret_key":"dinner on friday"}`, nil)
	if w.Code != http.StatusNotFound || w.Body.String() != wrongBody {
		t.Fatalf("non-key search must answer exactly like a wrong key: %d %s", w.Code, w.Body.String())
	}
	if svc.revealCalls != 1 {
		t.Fatalf("a string that cannot be a key was tried as one (%d attempts): it would count toward the lockout", svc.revealCalls)
	}
	// Locked: 429 LOCKED, integer retry_after seconds + Retry-After.
	w = callAs(r, u, http.MethodPost, "/v1/chat/hidden/reveal", `{"secret_key":"Locked99"}`, nil)
	code, details := errCode(t, w)
	if w.Code != http.StatusTooManyRequests || code != "LOCKED" || details["retry_after"] != float64(91) || w.Header().Get("Retry-After") != "91" {
		t.Fatalf("locked: %d %s %v", w.Code, code, details)
	}
	// Right key → reveal token, bound to the session.
	w = callAs(r, u, http.MethodPost, "/v1/chat/hidden/reveal", `{"secret_key":"Mango42"}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"reveal_token":"reveal-ok"`) || svc.seenDevice != "sid:"+hiddenTestSID {
		t.Fatalf("reveal: %d %s device=%s", w.Code, w.Body.String(), svc.seenDevice)
	}
	// Hidden list: 404 without the reveal token, rows with it.
	if w := callAs(r, u, http.MethodGet, "/v1/chat/hidden/conversations", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("hidden list without token: %d", w.Code)
	}
	if w := callAs(r, u, http.MethodGet, "/v1/chat/hidden/conversations", "", map[string]string{"X-Chat-Reveal": "reveal-ok"}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"locked":true`) {
		t.Fatalf("hidden list: %d %s", w.Code, w.Body.String())
	}
	// Open: 401 WRONG_PASSWORD with attempts_left, then the open token.
	w = callAs(r, u, http.MethodPost, "/v1/chat/hidden/open", `{"chat_password":"nope-nope"}`, map[string]string{"X-Chat-Reveal": "reveal-ok"})
	code, details = errCode(t, w)
	if w.Code != http.StatusUnauthorized || code != "WRONG_PASSWORD" || details["attempts_left"] != float64(3) {
		t.Fatalf("wrong password: %d %s %v", w.Code, code, details)
	}
	w = callAs(r, u, http.MethodPost, "/v1/chat/hidden/open", `{"chat_password":"river-stone"}`, map[string]string{"X-Chat-Reveal": "reveal-ok"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"open_token":"open-ok"`) {
		t.Fatalf("open: %d %s", w.Code, w.Body.String())
	}
	// Settings: partial body with shortcut null.
	if w := callAs(r, u, http.MethodPut, "/v1/chat/hidden/settings", `{"shortcut":null,"current_chat_password":"x"}`, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"shortcut":null`) {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	if w := callAs(r, u, http.MethodGet, "/v1/chat/hidden/settings", "", nil); w.Code != http.StatusOK ||
		strings.Contains(w.Body.String(), "hash") || !strings.Contains(w.Body.String(), `"configured":true`) {
		t.Fatalf("GET settings: %s", w.Body.String())
	}
}

// --- step-up on reset: the real auth middleware over signed tokens ---

func TestResetNeedsAStepUpOrAFreshSignIn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubHidden()
	const secret = "hidden-test-secret"
	r := gin.New()
	r.Use(AuthMiddlewareWithKeys(JWTKeySet{ActiveSecret: secret, ActiveKID: "v1"}, slog.Default()))
	New(svc, slog.Default()).RegisterRoutes(r)
	user := uuid.NewString()
	now := time.Now().Unix()
	mint := func(extra map[string]any) string {
		claims := map[string]any{"sub": user, "exp": now + 600, "sid": "s1", "typ": "access"}
		for k, v := range extra {
			claims[k] = v
		}
		return signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, claims, secret)
	}
	reset := func(bearer string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/hidden/reset", strings.NewReader(`{"secret_key":"Kiwi7777","chat_password":"new-secret"}`))
		req.Header.Set("Authorization", "Bearer "+bearer)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	cases := []struct {
		name  string
		token string
		hdr   map[string]string
		ok    bool
	}{
		{"plain session token", mint(map[string]any{"auth_time": now - 3600}), nil, false},
		{"stale step-up", mint(map[string]any{"step_up_at": now - 301}), nil, false},
		{"future step-up beyond skew", mint(map[string]any{"step_up_at": now + 120}), nil, false},
		{"fresh step-up (POST /v1/auth/step-up)", mint(map[string]any{"step_up_at": now - 30}), nil, true},
		{"fresh sign-in (auth_time)", mint(map[string]any{"auth_time": now - 60}), nil, true},
		{"separate X-Step-Up-Token of the same user", mint(nil), map[string]string{"X-Step-Up-Token": mint(map[string]any{"step_up_at": now})}, true},
	}
	for _, tc := range cases {
		w := reset(tc.token, tc.hdr)
		code, _ := errCode(t, w)
		if tc.ok && w.Code != http.StatusOK {
			t.Errorf("%s: %d %s", tc.name, w.Code, code)
		}
		if !tc.ok && (w.Code != http.StatusForbidden || code != "STEP_UP_REQUIRED") {
			t.Errorf("%s: want 403 STEP_UP_REQUIRED, got %d %s", tc.name, w.Code, code)
		}
	}
	// Another user's step-up token is ignored.
	other := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"},
		map[string]any{"sub": uuid.NewString(), "exp": now + 600, "sid": "s1", "typ": "access", "step_up_at": now}, secret)
	if w := reset(mint(nil), map[string]string{"X-Step-Up-Token": other}); w.Code != http.StatusForbidden {
		t.Fatalf("someone else's step-up token reset this account: %d", w.Code)
	}
}

// --- inventory: every /v1/chat route is classified for hidden chats ---

// hiddenRouteClass classifies every route that is NOT under
// /v1/chat/conversations/:id (those are behind the open-token gate by
// construction — asserted below). A new route fails the test until it is
// classified here.
var hiddenRouteClass = map[string]string{
	"GET /v1/chat/health":                                             "no data",
	"POST /v1/chat/conversations/direct":                              "returns a hidden DM redacted (redactIfLocked)",
	"POST /v1/chat/conversations/group":                               "creates a new conversation (never hidden)",
	"GET /v1/chat/conversations":                                      "list: store excludes locked (ListInboxConversations / ListConversationsByUser)",
	"POST /v1/chat/presence":                                          "per-user presence by id, no conversation",
	"POST /v1/chat/folders":                                           "folder metadata only",
	"GET /v1/chat/folders":                                            "folder metadata only (contents: GetFolderConversations excludes locked)",
	"DELETE /v1/chat/folders/:folderId":                               "folder metadata only",
	"POST /v1/chat/folders/:folderId/conversations":                   "write; listing excludes locked",
	"DELETE /v1/chat/folders/:folderId/conversations/:conversationId": "write; listing excludes locked",
	"GET /v1/chat/requests":                                           "list: ListRequestEnvelopesPage / ListMessageRequests exclude locked",
	"GET /v1/chat/requests/count":                                     "badge: counts ListRequestEnvelopesPage (excludes locked)",
	"GET /v1/chat/invitations":                                        "group offers to a NON-member (nothing to hide yet)",
	"POST /v1/chat/invitations/:invitationId/accept":                  "non-member join",
	"POST /v1/chat/invitations/:invitationId/decline":                 "non-member decline",
	"GET /v1/chat/invites/:code":                                      "public group preview by link",
	"POST /v1/chat/invites/:code/join":                                "returns a hidden group redacted (redactIfLocked)",
	"DELETE /v1/chat/messages/:messageId/star":                        "deletes the caller's own star row; reveals nothing",
	"GET /v1/chat/starred":                                            "list: GetStarredMessages excludes locked",
	"POST /v1/chat/backup":                                            "backup job record, no content",
	"GET /v1/chat/backup/latest":                                      "backup job record, no content",
	"DELETE /v1/chat/scheduled/:msgId":                                "cancels the caller's own scheduled row; reveals nothing",
	"GET /v1/chat/messages/:messageId/translation":                    "message content: requireOpen on the translation's conversation",
	"GET /v1/chat/stickers":                                           "catalogue",
	"PUT /v1/chat/notes/me":                                           "own note",
	"DELETE /v1/chat/notes/me":                                        "own note",
	"GET /v1/chat/notes":                                              "list: DirectPeerIDs excludes locked DMs",
	"GET /v1/chat/search":                                             "list: SearchMyConversations / GetMemberSearchStates exclude locked",
	"GET /v1/chat/hidden/settings":                                    "hidden chats",
	"PUT /v1/chat/hidden/settings":                                    "hidden chats",
	"POST /v1/chat/hidden/reset":                                      "hidden chats (step-up)",
	"POST /v1/chat/hidden/reveal":                                     "hidden chats",
	"GET /v1/chat/hidden/conversations":                               "hidden chats (reveal token)",
	"POST /v1/chat/hidden/open":                                       "hidden chats (reveal token)",
	"POST /v1/chat/hidden/lock":                                       "hidden chats",
}

func TestHiddenChatRouteInventoryIsComplete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(nil, nil).RegisterRoutes(router)
	seen := map[string]bool{}
	for _, rt := range router.Routes() {
		key := rt.Method + " " + rt.Path
		seen[key] = true
		if !strings.HasPrefix(rt.Path, "/v1/chat/") {
			continue
		}
		if strings.HasPrefix(rt.Path, "/v1/chat/conversations/:id") {
			if hiddenGateExempt[key] && key != http.MethodPost+" /v1/chat/conversations/:id/hide" {
				t.Errorf("%s is exempt from the open-token gate", key)
			}
			continue
		}
		if _, ok := hiddenRouteClass[key]; !ok {
			t.Errorf("route %s is not classified for hidden chats: does it list conversations (exclude locked) or read one (requireOpen)?", key)
		}
	}
	for key := range hiddenRouteClass {
		if !seen[key] {
			t.Errorf("stale classification %s", key)
		}
	}
	if len(hiddenGateExempt) != 1 {
		t.Fatalf("gate exemptions grew: %v", hiddenGateExempt)
	}
}

// The gate is installed on the /v1/chat group before any route, so every
// /conversations/:id route runs it — proven on a route registered LAST.
func TestGateRunsOnRoutesRegisteredLast(t *testing.T) {
	svc := newStubHidden()
	r := hiddenRouter(svc)
	u, conv := uuid.New(), uuid.New()
	svc.locked[lk(conv, u)] = true
	// unhide is registered by the very last registration call.
	if w := callAs(r, u, http.MethodPost, "/v1/chat/conversations/"+conv.String()+"/unhide", "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("gate skipped a late route: %d", w.Code)
	}
}

// The access log never carries the query string (the secret key may be
// typed into chat search).
func TestAccessLogNeverCarriesTheSearchQuery(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(LoggerMiddleware(log))
	r.GET("/v1/chat/search", func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/search?q=Mango42", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	if strings.Contains(buf.String(), "Mango42") || !strings.Contains(buf.String(), "/v1/chat/search") {
		t.Fatalf("access log: %s", buf.String())
	}
}

func accessIdentityForTest(user, sid string) accessauth.Identity {
	return accessauth.Identity{UserID: user, SessionID: sid}
}

var translationConv = uuid.MustParse("0e000000-0000-4000-8000-0000000000ee")

func (s *stubHidden) GetTranslation(_ context.Context, _, messageID uuid.UUID, lang string) (*store.MessageTranslation, error) {
	return &store.MessageTranslation{MessageID: messageID, ConversationID: translationConv, TargetLang: lang, TranslatedText: "secret translated"}, nil
}

func TestTranslationOfAHiddenChatMessageNeedsTheOpenToken(t *testing.T) {
	svc := newStubHidden()
	r := hiddenRouter(svc)
	u := uuid.New()
	svc.locked[lk(translationConv, u)] = true
	path := "/v1/chat/messages/" + uuid.NewString() + "/translation?lang=en"
	if w := callAs(r, u, http.MethodGet, path, "", nil); w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "secret translated") {
		t.Fatalf("translation without open token: %d %s", w.Code, w.Body.String())
	}
	if w := callAs(r, u, http.MethodGet, path, "", map[string]string{"X-Chat-Open": "open-ok"}); w.Code != http.StatusOK {
		t.Fatalf("translation with token: %d", w.Code)
	}
}
