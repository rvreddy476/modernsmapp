package search

// Chat message search index (chat parity programme, CONTRACT §6, lane B5).
//
// chat_messages_v1 holds the TEXT of chat messages so message-service can
// answer "search my chats". It is fed by chat-service's outbox events
// (internal/events/chat.go) and read ONLY through the internal route
// GET /internal/v1/search/chat-messages, which message-service calls with
// the caller's id and then re-checks current membership and the clear /
// hide cut-offs before anything reaches a client.
//
// ─── WHY EVERY WRITE IS A SCRIPTED UPSERT ───────────────────────────────
//
// chat-service's outbox relay publishes with an empty partition key, so the
// events of ONE message (created, edited, deleted) can land on different
// partitions and arrive in any order, and any of them can be redelivered.
// A plain index / delete pair would get this wrong in the case that
// matters most: an unsend that arrives before (or is followed by a replay
// of) its create would put the unsent text back into search for good.
//
// So a delete writes a TOMBSTONE that every later write refuses to touch,
// edits carry a text revision (edited_at in ms; the create is revision 0)
// and only a higher revision replaces the text, and the identity fields
// plus member_ids are written once — a replayed create cannot undo a
// membership removal that was applied after it.
//
// The query side fails closed on top of that: a hit needs the caller in
// member_ids, is never a tombstone and never a message request.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

const (
	// DefaultChatMessagesIndex is the production index name. Override with
	// CHAT_MESSAGES_INDEX (tests use a name ending in _test).
	DefaultChatMessagesIndex = "chat_messages_v1"
	// EnvChatMessagesIndex names the index override.
	EnvChatMessagesIndex = "CHAT_MESSAGES_INDEX"

	// ChatSnippetMaxRunes bounds the snippet returned per hit.
	ChatSnippetMaxRunes = 140
	// ChatMessageMaxRunes bounds the text stored per message. Chat text is
	// already capped by message-service; this is a backstop so a hostile
	// producer cannot push megabyte documents into the index.
	ChatMessageMaxRunes = 4000
	// ChatQueryMinRunes / ChatQueryMaxRunes bound the search text.
	ChatQueryMinRunes = 2
	ChatQueryMaxRunes = 200
	// ChatSearchDefaultLimit / ChatSearchMaxLimit bound a page.
	ChatSearchDefaultLimit = 20
	ChatSearchMaxLimit     = 50
)

// ErrInvalidChatCursor is returned for a cursor this service did not mint.
var ErrInvalidChatCursor = errors.New("invalid cursor")

// ChatMessagesIndexFromEnv returns the configured index name.
func ChatMessagesIndexFromEnv() string {
	if v := strings.TrimSpace(os.Getenv(EnvChatMessagesIndex)); v != "" {
		return v
	}
	return DefaultChatMessagesIndex
}

// ChatMessagesIndex returns the index this store reads and writes.
func (s *Store) ChatMessagesIndex() string {
	if s.chatIndex == "" {
		return DefaultChatMessagesIndex
	}
	return s.chatIndex
}

// WithChatMessagesIndex points the store at another index name (tests).
// Call EnsureChatMessagesIndex afterwards to create it.
func (s *Store) WithChatMessagesIndex(name string) *Store {
	s.chatIndex = strings.TrimSpace(name)
	return s
}

// chatMessagesProperties is the mapping. `dynamic: strict` keeps a
// malformed producer from inventing fields (a text-typed member_ids would
// silently break the membership filter).
const chatMessagesProperties = `{
	"conversation_id": { "type": "keyword" },
	"message_id":      { "type": "keyword" },
	"sender_id":       { "type": "keyword" },
	"member_ids":      { "type": "keyword" },
	"text": {
		"type": "text",
		"analyzer": "standard",
		"fields": {
			"exact": { "type": "keyword", "normalizer": "chat_lowercase", "ignore_above": 256 }
		}
	},
	"text_rev":   { "type": "long" },
	"created_at": { "type": "date" },
	"edited_at":  { "type": "date" },
	"is_request": { "type": "boolean" },
	"deleted":    { "type": "boolean" }
}`

// EnsureChatMessagesIndex creates the index when absent and otherwise
// re-applies the mapping (put-mapping is additive, so this is idempotent on
// every boot and picks up fields added later).
func (s *Store) EnsureChatMessagesIndex(ctx context.Context) error {
	index := s.ChatMessagesIndex()
	exists, err := opensearchapi.IndicesExistsRequest{Index: []string{index}}.Do(ctx, s.client)
	if err != nil {
		return fmt.Errorf("check %s: %w", index, err)
	}
	exists.Body.Close()

	if exists.StatusCode == 404 {
		var settings map[string]any
		if err := json.Unmarshal([]byte(opensearchSettingsJSON()), &settings); err != nil {
			return fmt.Errorf("settings for %s: %w", index, err)
		}
		settings["analysis"] = map[string]any{
			"normalizer": map[string]any{
				"chat_lowercase": map[string]any{"type": "custom", "filter": []string{"lowercase"}},
			},
		}
		var props map[string]any
		if err := json.Unmarshal([]byte(chatMessagesProperties), &props); err != nil {
			return fmt.Errorf("mapping for %s: %w", index, err)
		}
		body, _ := json.Marshal(map[string]any{
			"settings": settings,
			"mappings": map[string]any{"dynamic": "strict", "properties": props},
		})
		res, err := opensearchapi.IndicesCreateRequest{Index: index, Body: bytes.NewReader(body)}.Do(ctx, s.client)
		if err != nil {
			return fmt.Errorf("create %s: %w", index, err)
		}
		defer res.Body.Close()
		// A concurrent boot may have created it first; that is success.
		if res.IsError() && !strings.Contains(res.String(), "resource_already_exists_exception") {
			return fmt.Errorf("create %s: %s", index, res.String())
		}
		return nil
	}

	body := `{"properties":` + chatMessagesProperties + `}`
	res, err := opensearchapi.IndicesPutMappingRequest{
		Index: []string{index},
		Body:  strings.NewReader(body),
	}.Do(ctx, s.client)
	if err != nil {
		return fmt.Errorf("put mapping %s: %w", index, err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("put mapping %s: %s", index, res.String())
	}
	return nil
}

// ChatMessageDoc is one text message as the create event describes it.
type ChatMessageDoc struct {
	MessageID      string
	ConversationID string
	SenderID       string
	MemberIDs      []string
	Text           string
	CreatedAt      time.Time
	IsRequest      bool
}

// The scripts. Each refuses to touch a tombstone.
const (
	chatUpsertScript = `
if (ctx._source.deleted == true) { ctx.op = 'none'; return; }
if (ctx._source.message_id == null) { ctx._source.message_id = params.d.message_id; }
if (ctx._source.conversation_id == null) { ctx._source.conversation_id = params.d.conversation_id; }
if (ctx._source.sender_id == null) { ctx._source.sender_id = params.d.sender_id; }
if (ctx._source.member_ids == null) { ctx._source.member_ids = params.d.member_ids; }
if (ctx._source.created_at == null) { ctx._source.created_at = params.d.created_at; }
if (ctx._source.is_request == null) { ctx._source.is_request = params.d.is_request; }
long cur = ctx._source.text_rev == null ? -1L : ((Number) ctx._source.text_rev).longValue();
if (0L > cur) { ctx._source.text = params.d.text; ctx._source.text_rev = 0L; }
`
	chatEditScript = `
if (ctx._source.deleted == true) { ctx.op = 'none'; return; }
long cur = ctx._source.text_rev == null ? -1L : ((Number) ctx._source.text_rev).longValue();
if (params.rev <= cur) { ctx.op = 'none'; return; }
if (ctx._source.message_id == null) { ctx._source.message_id = params.message_id; }
if (ctx._source.conversation_id == null) { ctx._source.conversation_id = params.conversation_id; }
ctx._source.text = params.text;
ctx._source.text_rev = params.rev;
ctx._source.edited_at = params.edited_at;
`
	chatTombstoneScript = `
ctx._source.clear();
ctx._source.message_id = params.message_id;
ctx._source.conversation_id = params.conversation_id;
ctx._source.deleted = true;
`
	chatRemoveMemberScript = `
if (ctx._source.member_ids != null) { ctx._source.member_ids.removeIf(m -> m == params.user_id); }
`
	chatClearRequestScript = `ctx._source.is_request = false;`
)

// UpsertChatMessage indexes a created text message. Idempotent and safe
// against reordering — see the file comment.
func (s *Store) UpsertChatMessage(ctx context.Context, d ChatMessageDoc) error {
	if d.MessageID == "" || d.ConversationID == "" {
		return fmt.Errorf("upsert chat message: missing ids")
	}
	members := dedupeNonEmpty(d.MemberIDs)
	if len(members) == 0 {
		return fmt.Errorf("upsert chat message %s: no members", d.MessageID)
	}
	created := d.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	return s.scriptedUpsert(ctx, d.MessageID, chatUpsertScript, map[string]any{
		"d": map[string]any{
			"message_id":      d.MessageID,
			"conversation_id": d.ConversationID,
			"sender_id":       d.SenderID,
			"member_ids":      members,
			"text":            ClampRunes(d.Text, ChatMessageMaxRunes),
			"created_at":      created.UTC().Format(time.RFC3339Nano),
			"is_request":      d.IsRequest,
		},
	})
}

// EditChatMessage replaces the text when editedAt is newer than what is
// stored. An edit that arrives before its create leaves a document with no
// member_ids, which no query can return until the create fills them in.
func (s *Store) EditChatMessage(ctx context.Context, messageID, conversationID, text string, editedAt time.Time) error {
	if messageID == "" {
		return fmt.Errorf("edit chat message: missing id")
	}
	if editedAt.IsZero() {
		editedAt = time.Now().UTC()
	}
	rev := editedAt.UnixMilli()
	if rev < 1 {
		rev = 1 // always above the create's revision 0
	}
	return s.scriptedUpsert(ctx, messageID, chatEditScript, map[string]any{
		"message_id":      messageID,
		"conversation_id": conversationID,
		"text":            ClampRunes(text, ChatMessageMaxRunes),
		"rev":             rev,
		"edited_at":       editedAt.UTC().Format(time.RFC3339Nano),
	})
}

// TombstoneChatMessage removes the text and marks the message deleted for
// good: later creates, edits and replays become no-ops.
func (s *Store) TombstoneChatMessage(ctx context.Context, messageID, conversationID string) error {
	if messageID == "" {
		return nil
	}
	return s.scriptedUpsert(ctx, messageID, chatTombstoneScript, map[string]any{
		"message_id":      messageID,
		"conversation_id": conversationID,
	})
}

func (s *Store) scriptedUpsert(ctx context.Context, id, source string, params map[string]any) error {
	body, err := json.Marshal(map[string]any{
		"scripted_upsert": true,
		"upsert":          map[string]any{},
		"script": map[string]any{
			"lang":   "painless",
			"source": source,
			"params": params,
		},
	})
	if err != nil {
		return err
	}
	retry := 3
	res, err := opensearchapi.UpdateRequest{
		Index:           s.ChatMessagesIndex(),
		DocumentID:      id,
		Body:            bytes.NewReader(body),
		RetryOnConflict: &retry,
	}.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("chat message %s update: %s", id, res.String())
	}
	return nil
}

// RemoveChatMember drops userID from member_ids on every message of the
// conversation, so a person who left or was removed stops finding its
// messages even before message-service's membership re-check.
func (s *Store) RemoveChatMember(ctx context.Context, conversationID, userID string) error {
	if conversationID == "" || userID == "" {
		return nil
	}
	return s.chatUpdateByQuery(ctx, map[string]any{
		"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"conversation_id": conversationID}},
			map[string]any{"term": map[string]any{"member_ids": userID}},
		}},
	}, chatRemoveMemberScript, map[string]any{"user_id": userID})
}

// ClearChatRequest marks a conversation's messages as no longer a message
// request (the request was accepted), making them searchable.
func (s *Store) ClearChatRequest(ctx context.Context, conversationID string) error {
	if conversationID == "" {
		return nil
	}
	return s.chatUpdateByQuery(ctx, map[string]any{
		"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"conversation_id": conversationID}},
			map[string]any{"term": map[string]any{"is_request": true}},
		}},
	}, chatClearRequestScript, nil)
}

// RefreshChatMessages makes recent writes visible to search and to
// _update_by_query (which only sees refreshed segments).
func (s *Store) RefreshChatMessages(ctx context.Context) error {
	index := s.ChatMessagesIndex()
	res, err := opensearchapi.IndicesRefreshRequest{Index: []string{index}}.Do(ctx, s.client)
	if err != nil {
		return fmt.Errorf("refresh %s: %w", index, err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("refresh %s: %s", index, res.String())
	}
	return nil
}

func (s *Store) chatUpdateByQuery(ctx context.Context, query map[string]any, source string, params map[string]any) error {
	// Refresh first, or a message indexed inside the refresh interval is
	// invisible to the update and keeps the removed member.
	if err := s.RefreshChatMessages(ctx); err != nil {
		return err
	}
	script := map[string]any{"lang": "painless", "source": source}
	if params != nil {
		script["params"] = params
	}
	body, err := json.Marshal(map[string]any{"query": query, "script": script})
	if err != nil {
		return err
	}
	// Conflicts abort (the default): a concurrent write turns into an error,
	// the consumer retries, and the update — idempotent — runs again.
	res, err := s.client.UpdateByQuery(
		[]string{s.ChatMessagesIndex()},
		s.client.UpdateByQuery.WithContext(ctx),
		s.client.UpdateByQuery.WithBody(bytes.NewReader(body)),
		s.client.UpdateByQuery.WithRefresh(true),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("chat update_by_query: %s", res.String())
	}
	return nil
}

// DeleteChatMessagesBySender erases every message a user sent (account
// purge). A missing index is not an error.
func (s *Store) DeleteChatMessagesBySender(ctx context.Context, senderID string) error {
	if senderID == "" {
		return nil
	}
	if err := s.RefreshChatMessages(ctx); err != nil && !strings.Contains(err.Error(), "index_not_found") {
		return err
	}
	body, _ := json.Marshal(map[string]any{
		"query": map[string]any{"term": map[string]any{"sender_id": senderID}},
	})
	res, err := s.client.DeleteByQuery(
		[]string{s.ChatMessagesIndex()},
		bytes.NewReader(body),
		s.client.DeleteByQuery.WithContext(ctx),
		s.client.DeleteByQuery.WithConflicts("proceed"),
		s.client.DeleteByQuery.WithRefresh(true),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return nil
	}
	if res.IsError() {
		return fmt.Errorf("chat delete_by_query sender %s: %s", senderID, res.String())
	}
	var out struct {
		VersionConflicts int   `json:"version_conflicts"`
		Failures         []any `json:"failures"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err == nil && (out.VersionConflicts > 0 || len(out.Failures) > 0) {
		return fmt.Errorf("chat delete_by_query sender %s: %d conflicts, %d failures; will retry",
			senderID, out.VersionConflicts, len(out.Failures))
	}
	return nil
}

// ─── Query side ─────────────────────────────────────────────────────────

// ChatSearchQuery is one page request.
type ChatSearchQuery struct {
	UserID string
	Q      string
	Cursor string
	Limit  int
}

// ChatMessageHit is one result row (the internal route's wire shape).
type ChatMessageHit struct {
	ConversationID string    `json:"conversation_id"`
	MessageID      string    `json:"message_id"`
	Snippet        string    `json:"snippet"`
	SenderID       string    `json:"sender_id"`
	At             time.Time `json:"at"`
}

type chatCursor struct {
	T  int64  `json:"t"`
	ID string `json:"id"`
}

func encodeChatCursor(t int64, id string) string {
	b, _ := json.Marshal(chatCursor{T: t, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeChatCursor(s string) (chatCursor, error) {
	var c chatCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(b, &c) != nil || c.ID == "" || c.T <= 0 {
		return chatCursor{}, ErrInvalidChatCursor
	}
	return c, nil
}

// BuildChatSearchBody is the whole query. Exported so the member filter is
// pinned by a test without a cluster.
//
// The caller-membership term is the guard that makes this safe to answer
// for any user id: drop it and every person's messages match every query.
func BuildChatSearchBody(q ChatSearchQuery) (map[string]any, error) {
	if strings.TrimSpace(q.UserID) == "" {
		return nil, fmt.Errorf("chat search: empty user id")
	}
	text := strings.TrimSpace(q.Q)
	limit := q.Limit
	if limit <= 0 {
		limit = ChatSearchDefaultLimit
	}
	if limit > ChatSearchMaxLimit {
		limit = ChatSearchMaxLimit
	}
	body := map[string]any{
		"size":             limit + 1,
		"track_total_hits": false,
		"_source":          []string{"conversation_id", "message_id", "sender_id", "created_at", "text"},
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{"member_ids": q.UserID}},
				},
				"must_not": []any{
					map[string]any{"term": map[string]any{"deleted": true}},
					map[string]any{"term": map[string]any{"is_request": true}},
				},
				"must": []any{
					map[string]any{"bool": map[string]any{
						"should": []any{
							map[string]any{"match": map[string]any{"text": map[string]any{"query": text, "operator": "and"}}},
							map[string]any{"match_phrase_prefix": map[string]any{"text": map[string]any{"query": text}}},
							map[string]any{"term": map[string]any{"text.exact": strings.ToLower(text)}},
						},
						"minimum_should_match": 1,
					}},
				},
			},
		},
		"sort": []any{
			map[string]any{"created_at": map[string]any{"order": "desc"}},
			map[string]any{"message_id": map[string]any{"order": "desc"}},
		},
	}
	if q.Cursor != "" {
		c, err := decodeChatCursor(q.Cursor)
		if err != nil {
			return nil, err
		}
		body["search_after"] = []any{c.T, c.ID}
	}
	return body, nil
}

// SearchChatMessages runs one page and returns the hits and the cursor for
// the next page ("" when this is the last).
func (s *Store) SearchChatMessages(ctx context.Context, q ChatSearchQuery) ([]ChatMessageHit, string, error) {
	body, err := BuildChatSearchBody(q)
	if err != nil {
		return nil, "", err
	}
	limit := body["size"].(int) - 1
	data, _ := json.Marshal(body)
	res, err := s.client.Search(
		s.client.Search.WithContext(ctx),
		s.client.Search.WithIndex(s.ChatMessagesIndex()),
		s.client.Search.WithBody(bytes.NewReader(data)),
	)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		// Index not created yet: nothing has been indexed, so nothing matches.
		return []ChatMessageHit{}, "", nil
	}
	if res.IsError() {
		return nil, "", fmt.Errorf("chat search: %s", res.String())
	}
	var out struct {
		Hits struct {
			Hits []struct {
				Source struct {
					ConversationID string    `json:"conversation_id"`
					MessageID      string    `json:"message_id"`
					SenderID       string    `json:"sender_id"`
					CreatedAt      time.Time `json:"created_at"`
					Text           string    `json:"text"`
				} `json:"_source"`
				Sort []any `json:"sort"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, "", fmt.Errorf("chat search decode: %w", err)
	}
	hits := make([]ChatMessageHit, 0, len(out.Hits.Hits))
	var next string
	for i, h := range out.Hits.Hits {
		if i == limit {
			// The extra row only proves there is another page; the cursor is
			// the last row actually returned.
			last := hits[len(hits)-1]
			next = encodeChatCursor(last.At.UnixMilli(), last.MessageID)
			break
		}
		hits = append(hits, ChatMessageHit{
			ConversationID: h.Source.ConversationID,
			MessageID:      h.Source.MessageID,
			SenderID:       h.Source.SenderID,
			At:             h.Source.CreatedAt.UTC(),
			Snippet:        ChatSnippet(h.Source.Text, q.Q, ChatSnippetMaxRunes),
		})
	}
	return hits, next, nil
}

// ChatSnippet returns at most max runes of text, positioned so the first
// case-insensitive occurrence of a query term is inside it. No markup: the
// client highlights.
func ChatSnippet(text, query string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	// Lower-case rune by rune so indexes stay aligned with runes.
	lower := lowerRunes(runes)
	at := -1
	for _, term := range strings.FieldsFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	}) {
		if i := runeIndex(lower, lowerRunes([]rune(term))); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	start := 0
	if at > max/3 {
		start = at - max/3
	}
	end := start + max
	if end > len(runes) {
		end = len(runes)
		start = end - max
	}
	// Ellipses count toward the bound.
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "…"
		start++
	}
	if end < len(runes) {
		suffix = "…"
		end--
	}
	return prefix + string(runes[start:end]) + suffix
}

func lowerRunes(in []rune) []rune {
	out := make([]rune, len(in))
	for i, r := range in {
		out[i] = unicode.ToLower(r)
	}
	return out
}

func runeIndex(hay, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(hay) {
		return -1
	}
outer:
	for i := 0; i+len(needle) <= len(hay); i++ {
		for j := range needle {
			if hay[i+j] != needle[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}

// ClampRunes truncates s to at most n runes.
func ClampRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func dedupeNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
