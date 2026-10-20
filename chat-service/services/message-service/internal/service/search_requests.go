package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// --- Search (§6) ---

// ErrSearchQueryTooShort is q under two characters.
var ErrSearchQueryTooShort = errors.New("q must be at least 2 characters")

// SearchMessageHit is one message match.
type SearchMessageHit struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	MessageID      uuid.UUID `json:"message_id"`
	Snippet        string    `json:"snippet"`
	SenderID       uuid.UUID `json:"sender_id"`
	At             time.Time `json:"at"`
}

// ChatSearchResult is GET /v1/chat/search.
type ChatSearchResult struct {
	People        []json.RawMessage      `json:"people"`
	Conversations []ConversationResponse `json:"conversations"`
	Messages      []SearchMessageHit     `json:"messages"`
	NextCursor    string                 `json:"next_cursor,omitempty"`
}

// SetSearchService wires search-service (SEARCH_SERVICE_URL). Empty: search
// returns no people and no messages; conversation matches still work.
func (s *Service) SetSearchService(base string) { s.searchServiceURL = strings.TrimRight(base, "/") }

const searchConversationCap = 10

// SearchChat answers the inbox search. people come from search-service
// users; conversations are the caller's own (title / peer match); messages
// come from search-service's chat index and are RE-CHECKED here: current
// active membership, the caller's cut-off, delete-for-me, and no request
// conversations — the index is never trusted on its own.
func (s *Service) SearchChat(ctx context.Context, userID uuid.UUID, q, cursor string) (*ChatSearchResult, error) {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < 2 {
		return nil, ErrSearchQueryTooShort
	}
	if len([]rune(q)) > 100 {
		q = string([]rune(q)[:100])
	}
	out := &ChatSearchResult{People: []json.RawMessage{}, Conversations: []ConversationResponse{}, Messages: []SearchMessageHit{}}

	if cursor == "" {
		out.People = s.searchPeople(ctx, userID, q)
		if ps, ok := s.parity(); ok {
			ids, err := ps.SearchMyConversations(ctx, userID, q, searchConversationCap)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				row, err := s.getConversationResponseFor(ctx, userID, id)
				if err == nil {
					out.Conversations = append(out.Conversations, *row)
				}
			}
		}
	}

	hits, next, err := s.searchMessageIndex(ctx, userID, q, cursor)
	if err != nil {
		s.log.Warn("chat message search unavailable", "err", err)
		return out, nil
	}
	out.NextCursor = next
	out.Messages = s.recheckSearchHits(ctx, userID, hits)
	return out, nil
}

func (s *Service) recheckSearchHits(ctx context.Context, userID uuid.UUID, hits []SearchMessageHit) []SearchMessageHit {
	kept := []SearchMessageHit{}
	ps, ok := s.parity()
	if !ok || len(hits) == 0 {
		return kept
	}
	convIDs := make([]uuid.UUID, 0, len(hits))
	msgIDs := make([]uuid.UUID, 0, len(hits))
	for _, h := range hits {
		convIDs = append(convIDs, h.ConversationID)
		msgIDs = append(msgIDs, h.MessageID)
	}
	states, err := ps.GetMemberSearchStates(ctx, userID, convIDs)
	if err != nil {
		return kept
	}
	hidden, err := ps.HiddenAmong(ctx, userID, msgIDs)
	if err != nil {
		return kept
	}
	for _, h := range hits {
		st, member := states[h.ConversationID]
		if !member || st.IsRequest || hidden[h.MessageID] {
			continue
		}
		if st.ClearedBefore != nil && !h.At.After(*st.ClearedBefore) {
			continue
		}
		kept = append(kept, h)
	}
	return kept
}

func (s *Service) searchGet(ctx context.Context, path string, params url.Values, viewer *uuid.UUID) ([]byte, error) {
	if s.searchServiceURL == "" || s.httpClient == nil {
		return nil, errors.New("search not configured")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, s.searchServiceURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if viewer != nil {
		req.Header.Set("X-User-Id", viewer.String())
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search-service returned %d", resp.StatusCode)
	}
	return raw, nil
}

// searchPeople is search-service GET /v1/search/users as the viewer (it
// applies the viewer's block scope). Rows pass through unchanged.
func (s *Service) searchPeople(ctx context.Context, userID uuid.UUID, q string) []json.RawMessage {
	raw, err := s.searchGet(ctx, "/v1/search/users", url.Values{"q": {q}, "limit": {"10"}}, &userID)
	if err != nil {
		return []json.RawMessage{}
	}
	var env struct {
		Data struct {
			Items []json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Data.Items == nil {
		return []json.RawMessage{}
	}
	return env.Data.Items
}

// searchMessageIndex is lane B5's internal route
// GET /internal/v1/search/chat-messages?user_id=&q=&cursor=&limit=
// → {data:[{conversation_id,message_id,snippet,sender_id,at}], next_cursor}.
func (s *Service) searchMessageIndex(ctx context.Context, userID uuid.UUID, q, cursor string) ([]SearchMessageHit, string, error) {
	if s.searchServiceURL == "" {
		return nil, "", nil
	}
	params := url.Values{"user_id": {userID.String()}, "q": {q}, "limit": {"20"}}
	if cursor != "" {
		params.Set("cursor", cursor)
	}
	raw, err := s.searchGet(ctx, "/internal/v1/search/chat-messages", params, nil)
	if err != nil {
		return nil, "", err
	}
	var env struct {
		Data       json.RawMessage `json:"data"`
		NextCursor string          `json:"next_cursor"`
		Meta       struct {
			NextCursor string `json:"next_cursor"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, "", err
	}
	var hits []SearchMessageHit
	if err := json.Unmarshal(env.Data, &hits); err != nil {
		// Tolerate {data:{items:[...], next_cursor}} as well.
		var wrapped struct {
			Items      []SearchMessageHit `json:"items"`
			NextCursor string             `json:"next_cursor"`
		}
		if err2 := json.Unmarshal(env.Data, &wrapped); err2 != nil {
			return nil, "", err
		}
		hits = wrapped.Items
		if env.NextCursor == "" {
			env.NextCursor = wrapped.NextCursor
		}
	}
	next := env.NextCursor
	if next == "" {
		next = env.Meta.NextCursor
	}
	return hits, next, nil
}

// --- Requests filtering (§7) ---

// RequestRow is one entry of GET /v1/chat/requests: the legacy conversation
// shape plus whether safe requests filtered it.
type RequestRow struct {
	postgres.Conversation
	Filtered bool `json:"filtered"`
	// Sender is who sent the request (fix pass 2): clients show "Deleted
	// account" when deleted, else display_name, else @username.
	Sender *RequestSender `json:"sender"`
	// Direction is "incoming" (the caller must answer it) or "outgoing" (the
	// caller sent it). The list holds both; only incoming ones are waiting
	// for the caller.
	Direction string `json:"direction"`
	// DM consent model (additive): the request's one message ("" until
	// it is sent) and when the request was made — the list order.
	Preview     string    `json:"preview"`
	RequestedAt time.Time `json:"requested_at"`
}

// requestDirection says whether the viewer received ("incoming") or sent
// ("outgoing") a request. A request row with no receiver (its sender was
// purged) falls back to the conversation creator as the sender.
func requestDirection(receiverID, senderID uuid.UUID, createdBy *uuid.UUID, viewer uuid.UUID) string {
	if receiverID == viewer {
		return "incoming"
	}
	if senderID == viewer {
		return "outgoing"
	}
	if receiverID == uuid.Nil && createdBy != nil && *createdBy != viewer {
		return "incoming"
	}
	return "outgoing"
}

// newAccountAge is the "new account" request signal (CONTRACT §7).
const newAccountAge = 7 * 24 * time.Hour

// requestFiltered decides whether an incoming request goes to the filtered
// sub-list: a filtered keyword always does; with safe mode on, a link or a
// sender account younger than 7 days does too. A first request message is
// text-only, so the sensitive-media signal cannot apply to it. An unknown
// account age does not count as new.
func requestFiltered(preview string, policy ChatPolicy, senderCreatedAt *time.Time, now time.Time) bool {
	if matchesKeyword(preview, policy.DMFilteredKeywords) {
		return true
	}
	if !policy.DMSafeMode {
		return false
	}
	if containsLink(preview) {
		return true
	}
	return senderCreatedAt != nil && now.Sub(*senderCreatedAt) < newAccountAge
}
