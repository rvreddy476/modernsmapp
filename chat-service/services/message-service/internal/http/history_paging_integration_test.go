//go:build integration

package http

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// A run of messages the reader deleted for themselves must not end their
// history: the page keeps reading past them instead of coming back empty.
func TestHistoryPageReadsPastMessagesHiddenForMe(t *testing.T) {
	e := newParityEnv(t)

	code, raw := e.do("POST", "/v1/chat/conversations/direct", pA, map[string]string{"other_user_id": pB.String()})
	e.must(code, 200, raw, "create dm")
	var dm struct {
		ID uuid.UUID `json:"id"`
	}
	dataOf(t, raw, &dm)
	path := "/v1/chat/conversations/" + dm.ID.String()

	var ids []string
	for _, text := range []string{"oldest", "hidden one", "hidden two"} {
		code, raw := e.do("POST", path+"/messages", pA, map[string]any{"type": "text", "text": text})
		e.must(code, 200, raw, "send")
		var m map[string]any
		dataOf(t, raw, &m)
		ids = append(ids, m["msg_id"].(string))
	}
	for _, id := range ids[1:] {
		code, raw := e.do("DELETE", path+"/messages/"+id, pB, map[string]string{"scope": "me"})
		if code >= 300 {
			t.Fatalf("delete for me: %d %s", code, raw)
		}
	}

	var page []map[string]any
	if err := json.Unmarshal(mustGet(e, path+"/messages?limit=2", pB), &struct {
		Data *[]map[string]any `json:"data"`
	}{&page}); err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0]["msg_id"] != ids[0] {
		t.Fatalf("page after two hidden messages = %v, want only the oldest message", page)
	}
}
