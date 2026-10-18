package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/gin-gonic/gin"
)

const chatTestKey = "test-internal-key"

func chatRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.registerChatRoutes(r)
	return r
}

func chatGet(t *testing.T, r *gin.Engine, target, key string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

const chatUser = "11111111-1111-4111-8111-111111111111"

func TestChatSearch_RequiresConfiguredKey(t *testing.T) {
	store, f := newTubeFakeOS(t)
	r := chatRouter(New(store, nil)) // no key configured
	code, _ := chatGet(t, r, ChatMessagesPath+"?user_id="+chatUser+"&q=hello", "")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("no key configured: status %d, want 503", code)
	}
	r = chatRouter(New(store, nil).WithInternalKey(chatTestKey))
	for _, key := range []string{"", "wrong"} {
		if code, _ := chatGet(t, r, ChatMessagesPath+"?user_id="+chatUser+"&q=hello", key); code != http.StatusUnauthorized {
			t.Fatalf("key %q: status %d, want 401", key, code)
		}
	}
	if f.searchCount() != 0 {
		t.Fatalf("an unauthenticated call reached OpenSearch")
	}
}

func TestChatSearch_ValidatesBeforeSearching(t *testing.T) {
	store, f := newTubeFakeOS(t)
	r := chatRouter(New(store, nil).WithInternalKey(chatTestKey))
	for _, target := range []string{
		"?user_id=" + chatUser + "&q=a",          // 1 char
		"?user_id=" + chatUser + "&q=%20b%20",    // 1 char after trim
		"?user_id=" + chatUser,                   // no q
		"?user_id=not-a-uuid&q=hello",            // bad user
		"?q=hello",                               // no user
		"?user_id=" + chatUser + "&q=hi&limit=0", // bad limit
		"?user_id=" + chatUser + "&q=hi&limit=51",
		"?user_id=" + chatUser + "&q=" + strings.Repeat("x", 201),
		"?user_id=" + chatUser + "&q=hi&cursor=garbage",
	} {
		code, _ := chatGet(t, r, ChatMessagesPath+target, chatTestKey)
		if code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", target, code)
		}
	}
	if f.searchCount() != 0 {
		t.Fatalf("an invalid request reached OpenSearch (%d searches)", f.searchCount())
	}
}

func TestChatSearch_FiltersByCallerAndShapesRows(t *testing.T) {
	store, f := newTubeFakeOS(t)
	index := search.DefaultChatMessagesIndex
	// limit=1 and two hits: the second only proves a next page exists.
	f.respond(index, `{"hits":{"hits":[
		{"_source":{"conversation_id":"c1","message_id":"m2","sender_id":"s1","created_at":"2026-10-18T10:00:00Z","text":"hello there"},"sort":[1760781600000,"m2"]},
		{"_source":{"conversation_id":"c1","message_id":"m1","sender_id":"s2","created_at":"2026-10-18T09:00:00Z","text":"hello again"},"sort":[1760778000000,"m1"]}
	]}}`)
	r := chatRouter(New(store, nil).WithInternalKey(chatTestKey))
	code, out := chatGet(t, r, ChatMessagesPath+"?user_id="+chatUser+"&q=hello&limit=1", chatTestKey)
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}

	// The membership guard reached OpenSearch with THIS caller's id.
	sent, _ := json.Marshal(f.body(index))
	if !strings.Contains(string(sent), `"member_ids":"`+chatUser+`"`) {
		t.Fatalf("search body lacks the caller membership filter: %s", sent)
	}

	rows, _ := out["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("data = %v, want 1 row", out["data"])
	}
	row := rows[0].(map[string]any)
	for _, k := range []string{"conversation_id", "message_id", "snippet", "sender_id", "at"} {
		if _, ok := row[k]; !ok {
			t.Fatalf("row missing %q: %v", k, row)
		}
	}
	if row["message_id"] != "m2" || row["snippet"] != "hello there" || row["at"] != "2026-10-18T10:00:00Z" {
		t.Fatalf("row = %v", row)
	}
	next, _ := out["next_cursor"].(string)
	if next == "" {
		t.Fatalf("next_cursor missing with a further page: %v", out)
	}

	// Following the cursor sends search_after from the last returned row.
	f.respond(index, `{"hits":{"hits":[]}}`)
	code, out = chatGet(t, r, ChatMessagesPath+"?user_id="+chatUser+"&q=hello&limit=1&cursor="+next, chatTestKey)
	if code != http.StatusOK {
		t.Fatalf("page 2 status %d: %v", code, out)
	}
	sa, _ := f.body(index)["search_after"].([]any)
	if len(sa) != 2 || sa[1] != "m2" {
		t.Fatalf("search_after = %v, want [ts, m2]", sa)
	}
	if out["next_cursor"] != nil {
		t.Fatalf("last page next_cursor = %v, want null", out["next_cursor"])
	}
	if rows, _ := out["data"].([]any); rows == nil || len(rows) != 0 {
		t.Fatalf("empty page must be [], got %v", out["data"])
	}
}
