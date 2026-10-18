//go:build integration

package events

// Live OpenSearch proof of the chat_messages_v1 Painless (lane B5). The
// unit fake cannot execute Painless, so ordering, tombstones and the
// membership filter are proven here against a real cluster, in an index
// whose name ends in _test (created and deleted by the test).
//
//	OPENSEARCH_URL=http://localhost:9200 go test -tags integration -run Chat ./internal/events/

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/google/uuid"
)

func liveChatStore(t *testing.T) *search.Store {
	t.Helper()
	url := os.Getenv("OPENSEARCH_URL")
	if url == "" {
		t.Skip("OPENSEARCH_URL not set")
	}
	index := "chat_messages_v1_it_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "") + "_test"
	// Before search.New, so the boot-time ensure targets the test index and
	// never touches chat_messages_v1.
	t.Setenv(search.EnvChatMessagesIndex, index)
	store, err := search.New(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if store.ChatMessagesIndex() != index {
		t.Fatalf("store index = %s, want %s", store.ChatMessagesIndex(), index)
	}
	if err := store.EnsureChatMessagesIndex(context.Background()); err != nil {
		t.Fatalf("ensure (second run must be idempotent): %v", err)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodDelete, strings.TrimRight(url, "/")+"/"+index, nil)
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
		}
	})
	return store
}

func chatFind(t *testing.T, s *search.Store, user, q string) []search.ChatMessageHit {
	t.Helper()
	if err := s.RefreshChatMessages(context.Background()); err != nil {
		t.Fatal(err)
	}
	hits, _, err := s.SearchChatMessages(context.Background(), search.ChatSearchQuery{UserID: user, Q: q, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

// rawChatDoc reads one document's _source straight from the cluster.
func rawChatDoc(t *testing.T, s *search.Store, id string) map[string]any {
	t.Helper()
	url := strings.TrimRight(os.Getenv("OPENSEARCH_URL"), "/") + "/" + s.ChatMessagesIndex() + "/_doc/" + id
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Source map[string]any `json:"_source"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Source
}

func hasMessage(hits []search.ChatMessageHit, id string) bool {
	for _, h := range hits {
		if h.MessageID == id {
			return true
		}
	}
	return false
}

func TestChatLive_ConsumerEndToEnd(t *testing.T) {
	store := liveChatStore(t)
	c := chatConsumer(store)
	ctx := context.Background()
	a, b, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	conv := uuid.NewString()
	t0 := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)

	created := func(id, text string, at time.Time, extra map[string]any) {
		p := map[string]any{
			"message_id": id, "conversation_id": conv, "sender_id": a, "type": "text",
			"text": text, "recipient_ids": []string{b}, "created_at": at,
		}
		for k, v := range extra {
			p[k] = v
		}
		if err := c.processMessage(ctx, chatEnvelope(t, ChatMessageCreated, p)); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	// Members find it; an outsider does not.
	m1 := uuid.NewString()
	created(m1, "Planning the Goa trip tomorrow", t0, nil)
	created(m1, "Planning the Goa trip tomorrow", t0, nil) // redelivery
	if hits := chatFind(t, store, b, "goa"); len(hits) != 1 || hits[0].MessageID != m1 {
		t.Fatalf("member search = %+v, want exactly m1 once", hits)
	}
	if hits := chatFind(t, store, a, "plan"); !hasMessage(hits, m1) {
		t.Fatalf("prefix search by sender missed m1: %+v", hits)
	}
	if hits := chatFind(t, store, outsider, "goa"); len(hits) != 0 {
		t.Fatalf("NON-MEMBER found messages: %+v", hits)
	}

	// Unsend, then a replayed create: stays gone.
	created(uuid.NewString(), "unrelated filler", t0, nil)
	if err := c.processMessage(ctx, chatEnvelope(t, ChatMessageDeleted, map[string]any{"message_id": m1, "conversation_id": conv})); err != nil {
		t.Fatal(err)
	}
	created(m1, "Planning the Goa trip tomorrow", t0, nil)
	if err := c.processMessage(ctx, chatEnvelope(t, ChatMessageEdited, map[string]any{
		"message_id": m1, "conversation_id": conv, "text": "Goa again", "edited_at": time.Now().UTC(),
	})); err != nil {
		t.Fatal(err)
	}
	if hits := chatFind(t, store, b, "goa"); len(hits) != 0 {
		t.Fatalf("unsent message resurfaced: %+v", hits)
	}
	// The store guard on its own, not only the query-time must_not: the
	// tombstone holds no text after the replayed create and the edit.
	if src := rawChatDoc(t, store, m1); src["text"] != nil || src["deleted"] != true {
		t.Fatalf("tombstone was rewritten: %v", src)
	}

	// Delete that arrives BEFORE its create: never indexed.
	m2 := uuid.NewString()
	_ = c.processMessage(ctx, chatEnvelope(t, ChatMessageDeleted, map[string]any{"message_id": m2, "conversation_id": conv}))
	created(m2, "early unsend kerala", t0, nil)
	if hits := chatFind(t, store, b, "kerala"); len(hits) != 0 {
		t.Fatalf("delete-before-create indexed the text: %+v", hits)
	}
	if src := rawChatDoc(t, store, m2); src["text"] != nil || src["member_ids"] != nil {
		t.Fatalf("create after tombstone wrote fields: %v", src)
	}

	// Edit that arrives BEFORE its create: invisible until the create, and
	// the create does not overwrite the newer text.
	m3 := uuid.NewString()
	_ = c.processMessage(ctx, chatEnvelope(t, ChatMessageEdited, map[string]any{
		"message_id": m3, "conversation_id": conv, "text": "edited mumbai", "edited_at": time.Now().UTC(),
	}))
	if hits := chatFind(t, store, b, "mumbai"); len(hits) != 0 {
		t.Fatalf("an edit with no create must not be searchable: %+v", hits)
	}
	created(m3, "original pune", t0.Add(time.Minute), nil)
	if hits := chatFind(t, store, b, "mumbai"); !hasMessage(hits, m3) {
		t.Fatalf("edited text lost to a late create: %+v", hits)
	}
	if hits := chatFind(t, store, b, "pune"); hasMessage(hits, m3) {
		t.Fatalf("late create overwrote the edit: %+v", hits)
	}

	// Member removal: b stops finding the conversation; a replayed create
	// does not put b back.
	m4 := uuid.NewString()
	created(m4, "delhi plans", t0.Add(2*time.Minute), nil)
	if err := c.processMessage(ctx, chatEnvelope(t, ChatMemberRemoved, map[string]any{"conversation_id": conv, "user_id": b})); err != nil {
		t.Fatal(err)
	}
	created(m4, "delhi plans", t0.Add(2*time.Minute), nil)
	if hits := chatFind(t, store, b, "delhi"); len(hits) != 0 {
		t.Fatalf("removed member still finds messages: %+v", hits)
	}
	if hits := chatFind(t, store, a, "delhi"); !hasMessage(hits, m4) {
		t.Fatalf("remaining member lost the message: %+v", hits)
	}

	// Message request: hidden until accepted.
	reqConv := uuid.NewString()
	m5 := uuid.NewString()
	if err := c.processMessage(ctx, chatEnvelope(t, ChatMessageCreated, map[string]any{
		"message_id": m5, "conversation_id": reqConv, "sender_id": a, "type": "text",
		"text": "hello stranger chennai", "is_request": true, "recipient_ids": []string{outsider}, "created_at": t0,
	})); err != nil {
		t.Fatal(err)
	}
	if hits := chatFind(t, store, outsider, "chennai"); len(hits) != 0 {
		t.Fatalf("request message searchable before acceptance: %+v", hits)
	}
	if err := c.processMessage(ctx, chatEnvelope(t, ChatMessageRequestAccepted, map[string]any{"conversation_id": reqConv})); err != nil {
		t.Fatal(err)
	}
	if hits := chatFind(t, store, outsider, "chennai"); !hasMessage(hits, m5) {
		t.Fatalf("accepted request still hidden: %+v", hits)
	}

	// Purge: the sender's messages leave the index.
	if err := store.DeleteChatMessagesBySender(ctx, a); err != nil {
		t.Fatal(err)
	}
	if hits := chatFind(t, store, a, "delhi"); len(hits) != 0 {
		t.Fatalf("purged sender's messages remain: %+v", hits)
	}
}

func TestChatLive_PaginationIsStableNewestFirst(t *testing.T) {
	store := liveChatStore(t)
	c := chatConsumer(store)
	ctx := context.Background()
	a, b, conv := uuid.NewString(), uuid.NewString(), uuid.NewString()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	ids := make([]string, 5)
	for i := range ids {
		ids[i] = uuid.NewString()
		if err := c.processMessage(ctx, chatEnvelope(t, ChatMessageCreated, map[string]any{
			"message_id": ids[i], "conversation_id": conv, "sender_id": a, "type": "text",
			"text": "cricket score update", "recipient_ids": []string{b},
			"created_at": base.Add(time.Duration(i) * time.Minute),
		})); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RefreshChatMessages(ctx); err != nil {
		t.Fatal(err)
	}
	var got []string
	cursor := ""
	for page := 0; page < 5; page++ {
		hits, next, err := store.SearchChatMessages(ctx, search.ChatSearchQuery{UserID: b, Q: "cricket", Cursor: cursor, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range hits {
			got = append(got, h.MessageID)
			if len([]rune(h.Snippet)) > search.ChatSnippetMaxRunes {
				t.Fatalf("snippet too long: %q", h.Snippet)
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	want := []string{ids[4], ids[3], ids[2], ids[1], ids[0]}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pages = %v, want newest first %v", got, want)
	}
}
