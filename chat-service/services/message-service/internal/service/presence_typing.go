// Presence and typing: per-conversation presence with its disclosure filter,
// the typing indicator (which follows the send rules), and the graph-backed
// visibility check behind online status.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ConversationPresence describes who's currently viewing or typing in
// a conversation. activeCount is always present; activeUsers is only
// populated for small conversations (≤100 members) per the production
// realtime architecture rules — large groups get count-only to avoid
// leaking a viewer list.
type ConversationPresence struct {
	ActiveCount int64    `json:"active_count"`
	ActiveUsers []string `json:"active_users,omitempty"`
	TypingUsers []string `json:"typing_users,omitempty"`
	IsBigGroup  bool     `json:"is_big_group"`
}

// GetConversationPresence returns who's actively viewing convID right
// now. Caller must already be a member. The user-list-vs-count decision
// is made server-side based on the group size cap so a client can't
// scrape big channels.
func (s *Service) GetConversationPresence(ctx context.Context, userID, convID uuid.UUID) (*ConversationPresence, error) {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}

	const smallGroupCap = 100
	members, err := s.convStore.GetMembers(ctx, convID)
	if err != nil {
		return nil, err
	}
	isBig := len(members) > smallGroupCap

	count, err := s.pres.ActiveCount(ctx, convID.String())
	if err != nil {
		return nil, err
	}
	out := &ConversationPresence{ActiveCount: count, IsBigGroup: isBig}
	if !isBig {
		users, err := s.pres.ActiveUsers(ctx, convID.String(), smallGroupCap)
		if err != nil {
			return nil, err
		}
		out.ActiveUsers = s.filterPresenceDisclosure(ctx, userID, users)
		typing, err := s.pres.TypingUsers(ctx, convID.String(), smallGroupCap)
		if err != nil {
			return nil, err
		}
		out.TypingUsers = s.filterPresenceDisclosure(ctx, userID, typing)
	}
	return out, nil
}

// filterPresenceDisclosure keeps only roster entries whose owner discloses
// online state to the requester (P0-2 correction): membership alone never
// justified handing out an identity roster, because who_can_see_online_status
// is a per-target setting. The check is the same graph-gated, 30s-cached,
// fail-closed decision the direct-presence endpoint uses; the requester
// always sees themself.
func (s *Service) filterPresenceDisclosure(ctx context.Context, requesterID uuid.UUID, ids []string) []string {
	if len(ids) == 0 {
		return ids
	}
	parsed := make([]uuid.UUID, len(ids))
	valid := make([]bool, len(ids))
	ask := make([]uuid.UUID, 0, len(ids))
	for i, raw := range ids {
		id, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		parsed[i], valid[i] = id, true
		if id != requesterID {
			ask = append(ask, id)
		}
	}
	var visible map[uuid.UUID]bool
	if len(ask) > 0 {
		visible = s.discloseOnlineToMany(ctx, requesterID, ask)
	}
	filtered := make([]string, 0, len(ids))
	for i, raw := range ids {
		if !valid[i] {
			continue
		}
		if parsed[i] == requesterID || visible[parsed[i]] {
			filtered = append(filtered, raw)
		}
	}
	return filtered
}

// SetTyping publishes a typing indicator to all conversation members via Redis PubSub.
func (s *Service) SetTyping(ctx context.Context, userID, conversationID uuid.UUID) error {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotMember
	}
	// Typing is an ACTOR-side disclosure (directive §3.2 setting 7): the
	// sender's own toggle gates it, a paused sender publishes nothing, and an
	// UNKNOWN policy fails closed — silence is always safe. Returns success
	// so a denied indicator never surfaces as a client error.
	if p := s.GetChatPolicy(ctx, userID); !p.Known || p.ChatPaused || !p.SendTypingIndicators {
		return nil
	}
	// Account control: a deactivated / deletion-scheduled user is silent.
	if s.isHidden(ctx, userID) {
		return nil
	}

	// DM consent: typing is the same right as writing. Nobody types into a
	// closed request, and the requester of a pending one is a stranger to
	// the recipient until accepted: they get their one message, never a live
	// "typing…". The recipient may type (replying accepts).
	if !s.mayTypeIn(ctx, userID, conversationID) {
		return nil
	}

	// Broadcast to members
	members, _ := s.convStore.GetMembers(ctx, conversationID)
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "typing",
		"payload": map[string]interface{}{
			"conversation_id": conversationID.String(),
			"user_id":         userID.String(),
			"is_typing":       true,
		},
	})

	// One pipelined round trip for the key and every member's frame (audit
	// M4): one call per member made a keystroke in a large group cost a
	// Redis round trip per member, and every member typing made it
	// O(members²).
	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, fmt.Sprintf("typing:%s:%s", conversationID, userID), "1", 3*time.Second)
	for _, m := range members {
		if m.UserID == userID {
			continue
		}
		publishPersonal(ctx, pipe, m, payload)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		s.log.Debug("typing fan-out failed", "err", err, "conversation_id", conversationID)
	}
	return nil
}

// mayTypeIn applies send's request rule (requestSideOf) to typing. Any
// lookup failure is silence: typing is best-effort and silence is safe.
func (s *Service) mayTypeIn(ctx context.Context, userID, conversationID uuid.UUID) bool {
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil || conv == nil {
		return false
	}
	if !conv.IsRequest {
		return true
	}
	mr, err := s.convStore.GetMessageRequestByConversation(ctx, conversationID)
	if err != nil {
		return false
	}
	return requestSideOf(mr, userID, time.Now()) == requestRecipient
}

// (discloseReceiptTo — graph's see_read_receipts for connections_only —
// was removed with the ticks work: graph grants it only on a legacy
// accepted connection, which withheld nearly every receipt. The rule is
// readReceiptVisible in receipt_ticks.go.)

// checkVisibilityPermission resolves one disclosure action via graph.
// ANY failure returns false — disclosure fails closed.
func (s *Service) checkVisibilityPermission(ctx context.Context, actorID, targetID uuid.UUID, action string) bool {
	if s.graphServiceURL == "" {
		return false
	}
	url := fmt.Sprintf("%s/v1/permissions/check?target_user_id=%s&actions=%s", s.graphServiceURL, targetID, action)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-User-Id", actorID.String())
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	var envelope struct {
		Data struct {
			Decisions map[string]struct {
				Allowed bool `json:"allowed"`
			} `json:"decisions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	return envelope.Data.Decisions[action].Allowed
}

// GetPresence checks which of the given user IDs are currently online,
// GATED per target by their who_can_see_online_status (directive §3.5).
//
// The requester is the actor; each target's own privacy decides disclosure.
// A denied or unknown decision reports OFFLINE — indistinguishable from a
// genuinely offline user, which is the point: privacy denial must not leak.
func (s *Service) GetPresence(ctx context.Context, requesterID uuid.UUID, userIDs []uuid.UUID) (map[string]bool, error) {
	if len(userIDs) == 0 {
		return map[string]bool{}, nil
	}
	if len(userIDs) > 50 {
		userIDs = userIDs[:50]
	}

	keys := make([]string, len(userIDs))
	for i, id := range userIDs {
		keys[i] = "presence:" + id.String()
	}

	results, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		s.log.Warn("failed to check presence", "err", err)
		return map[string]bool{}, nil
	}

	// Account control: hidden users are always offline to everyone.
	hidden := map[uuid.UUID]bool{}
	if s.hiddenUsers != nil {
		if m, err := s.hiddenUsers.HiddenUsers(ctx, userIDs); err != nil {
			s.log.Warn("hidden-user lookup failed; reporting all offline", "err", err)
			return map[string]bool{}, nil
		} else {
			hidden = m
		}
	}

	// Only targets that are actually online (and not hidden) need a
	// disclosure decision; they are resolved in one batched graph lookup
	// (audit L10) instead of one serial call each.
	presence := make(map[string]bool, len(userIDs))
	ask := make([]uuid.UUID, 0, len(userIDs))
	for i, id := range userIDs {
		online := results[i] != nil && !hidden[id]
		presence[id.String()] = online
		if online && id != requesterID {
			ask = append(ask, id)
		}
	}
	if len(ask) > 0 {
		visible := s.discloseOnlineToMany(ctx, requesterID, ask)
		for _, id := range ask {
			if !visible[id] {
				presence[id.String()] = false
			}
		}
	}
	return presence, nil
}

// discloseOnlineTo asks graph about online-status visibility (cached per pair).
func (s *Service) discloseOnlineTo(ctx context.Context, viewerID, targetID uuid.UUID) bool {
	if s.rdb == nil {
		return s.checkVisibilityPermission(ctx, viewerID, targetID, "see_online_status")
	}
	cacheKey := onlineVisibilityCacheKey(viewerID, targetID)
	if cached, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil {
		return cached == "1"
	}
	allowed := s.checkVisibilityPermission(ctx, viewerID, targetID, actionSeeOnlineStatus)
	value := "0"
	if allowed {
		value = "1"
	}
	s.rdb.Set(ctx, cacheKey, value, 30*time.Second)
	return allowed
}
