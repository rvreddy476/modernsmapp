package search

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// The membership term is the guard that keeps one person's messages out of
// another person's search. Pin it in the exact query sent.
func TestBuildChatSearchBody_FiltersToCallerMembership(t *testing.T) {
	body, err := BuildChatSearchBody(ChatSearchQuery{UserID: "user-a", Q: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body)
	var parsed struct {
		Query struct {
			Bool struct {
				Filter  []map[string]map[string]any `json:"filter"`
				MustNot []map[string]map[string]any `json:"must_not"`
			} `json:"bool"`
		} `json:"query"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	foundMember := false
	for _, f := range parsed.Query.Bool.Filter {
		if f["term"]["member_ids"] == "user-a" {
			foundMember = true
		}
	}
	if !foundMember {
		t.Fatalf("query has no member_ids=user-a filter: %s", raw)
	}
	excluded := map[string]bool{}
	for _, f := range parsed.Query.Bool.MustNot {
		for k, v := range f["term"] {
			if v == true {
				excluded[k] = true
			}
		}
	}
	if !excluded["deleted"] || !excluded["is_request"] {
		t.Fatalf("query must exclude tombstones and message requests: %s", raw)
	}
}

func TestBuildChatSearchBody_RejectsEmptyUser(t *testing.T) {
	if _, err := BuildChatSearchBody(ChatSearchQuery{UserID: " ", Q: "hello"}); err == nil {
		t.Fatal("empty user id must not produce an unfiltered query")
	}
}

func TestBuildChatSearchBody_LimitAndCursor(t *testing.T) {
	body, err := BuildChatSearchBody(ChatSearchQuery{UserID: "u", Q: "hi", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if body["size"].(int) != ChatSearchMaxLimit+1 {
		t.Fatalf("size = %v, want max+1", body["size"])
	}
	cur := encodeChatCursor(1700000000000, "m-9")
	body, err = BuildChatSearchBody(ChatSearchQuery{UserID: "u", Q: "hi", Cursor: cur})
	if err != nil {
		t.Fatal(err)
	}
	sa := body["search_after"].([]any)
	if sa[0].(int64) != 1700000000000 || sa[1].(string) != "m-9" {
		t.Fatalf("search_after = %v", sa)
	}
	for _, bad := range []string{"%%%", "e30", encodeChatCursor(0, "x"), encodeChatCursor(5, "")} {
		if _, err := BuildChatSearchBody(ChatSearchQuery{UserID: "u", Q: "hi", Cursor: bad}); !errors.Is(err, ErrInvalidChatCursor) {
			t.Fatalf("cursor %q: err = %v, want ErrInvalidChatCursor", bad, err)
		}
	}
}

func TestChatSnippet_BoundedAndContainsMatch(t *testing.T) {
	long := strings.Repeat("filler words here ", 30) + "the Secret plan is ready " + strings.Repeat("more trailing text ", 30)
	s := ChatSnippet(long, "secret", ChatSnippetMaxRunes)
	if n := utf8.RuneCountInString(s); n > ChatSnippetMaxRunes {
		t.Fatalf("snippet is %d runes, max %d", n, ChatSnippetMaxRunes)
	}
	if !strings.Contains(s, "Secret") {
		t.Fatalf("snippet does not contain the match: %q", s)
	}
	if !strings.HasPrefix(s, "…") || !strings.HasSuffix(s, "…") {
		t.Fatalf("a mid-text window should be marked on both sides: %q", s)
	}
	if got := ChatSnippet("short  text\nhere", "x", 140); got != "short text here" {
		t.Fatalf("short text = %q", got)
	}
	// Multi-byte text stays within the bound and valid UTF-8.
	hindi := strings.Repeat("नमस्ते दोस्त ", 40)
	hs := ChatSnippet(hindi, "दोस्त", ChatSnippetMaxRunes)
	if utf8.RuneCountInString(hs) > ChatSnippetMaxRunes || !utf8.ValidString(hs) {
		t.Fatalf("multi-byte snippet invalid: %d runes", utf8.RuneCountInString(hs))
	}
}

func TestChatMessagesIndexFromEnv(t *testing.T) {
	t.Setenv(EnvChatMessagesIndex, "")
	if got := ChatMessagesIndexFromEnv(); got != DefaultChatMessagesIndex {
		t.Fatalf("default = %q", got)
	}
	t.Setenv(EnvChatMessagesIndex, "chat_messages_v1_test")
	if got := ChatMessagesIndexFromEnv(); got != "chat_messages_v1_test" {
		t.Fatalf("override = %q", got)
	}
}
