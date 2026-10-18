package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// Chat parity fix pass 2 (lane M, 2026-10-18): mark unread, the requests'
// sender block, conversation reports and the media sensitivity backfill.

// parityFix2Store is the fix-pass-2 surface of the Postgres store, reached
// by a checked assertion like parityStore.
type parityFix2Store interface {
	SetMarkedUnread(ctx context.Context, conversationID, userID uuid.UUID, unread bool) error
	RequestSenderIDs(ctx context.Context, convIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error)
}

func (s *Service) parityFix2() (parityFix2Store, bool) {
	ps, ok := s.convStore.(parityFix2Store)
	return ps, ok
}

// --- PUT /v1/chat/conversations/:id/unread ---

// SetConversationUnread marks the conversation unread for the caller (true)
// or clears that mark (false). The mark lasts until the caller next reads
// the conversation (MarkRead clears it with the unread counter).
func (s *Service) SetConversationUnread(ctx context.Context, userID, convID uuid.UUID, unread bool) error {
	ps, ok := s.parityFix2()
	if !ok {
		return errors.New("not supported")
	}
	if err := ps.SetMarkedUnread(ctx, convID, userID, unread); err != nil {
		if errors.Is(err, postgres.ErrNotActiveMember) {
			return ErrNotMember
		}
		return err
	}
	return nil
}

// --- requests: the sender block ---

// RequestSender is who sent a message request. A deleted, deactivated or
// unknown account has deleted=true and no name, username or photo: clients
// show "Deleted account" for it, else display_name, else @username.
type RequestSender struct {
	UserID      *uuid.UUID `json:"user_id"`
	DisplayName string     `json:"display_name"`
	Username    string     `json:"username"`
	AvatarURL   string     `json:"avatar_url"`
	Deleted     bool       `json:"deleted"`
}

// requestSenders resolves the sender block for each conversation id in
// senders (conversation -> sender id; uuid.Nil = unknown) as the viewer sees
// it. A fixed number of queries per page plus the bounded user-service
// refresh the inbox already does.
func (s *Service) requestSenders(ctx context.Context, viewerID uuid.UUID, senders map[uuid.UUID]uuid.UUID) map[uuid.UUID]*RequestSender {
	out := make(map[uuid.UUID]*RequestSender, len(senders))
	if len(senders) == 0 {
		return out
	}
	seen := map[uuid.UUID]bool{}
	var ids []uuid.UUID
	for _, id := range senders {
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	profiles := map[uuid.UUID]postgres.UserProfile{}
	hidden := map[uuid.UUID]bool{}
	extras := map[uuid.UUID]postgres.ProfileExtras{}
	var urls map[string]string
	if len(ids) > 0 {
		if got, err := s.convStore.GetUserProfiles(ctx, ids); err == nil {
			profiles = got
		}
		var missing []uuid.UUID
		for _, id := range ids {
			if p, ok := profiles[id]; !ok || strings.TrimSpace(p.DisplayName) == "" {
				missing = append(missing, id)
			}
		}
		for id, p := range s.fetchMissingProfiles(ctx, missing) {
			profiles[id] = p
		}
		extras = s.profileExtras(ctx, ids)
		if s.hiddenUsers != nil {
			if m, err := s.hiddenUsers.HiddenUsers(ctx, ids); err == nil {
				hidden = m
			}
		}
		var avatarIDs []string
		for _, id := range ids {
			if p, ok := profiles[id]; ok && p.AvatarMediaID != nil && !hidden[id] {
				avatarIDs = append(avatarIDs, p.AvatarMediaID.String())
			}
		}
		if len(avatarIDs) > 0 && s.mediaServiceURL != "" && s.httpClient != nil {
			urls = s.fetchMediaURLs(ctx, viewerID, avatarIDs)
		}
	}
	for convID, senderID := range senders {
		rs := &RequestSender{}
		if senderID == uuid.Nil {
			rs.Deleted = true
			out[convID] = rs
			continue
		}
		id := senderID
		rs.UserID = &id
		p := profiles[senderID]
		name := strings.TrimSpace(p.DisplayName)
		username := strings.TrimSpace(extras[senderID].Username)
		if hidden[senderID] || (name == "" && username == "") {
			rs.Deleted = true
			out[convID] = rs
			continue
		}
		rs.DisplayName, rs.Username = name, username
		if p.AvatarMediaID != nil {
			rs.AvatarURL = urls[p.AvatarMediaID.String()]
		}
		out[convID] = rs
	}
	return out
}

// attachRequestSenders fills Sender on the request rows of a page.
func (s *Service) attachRequestSenders(ctx context.Context, viewerID uuid.UUID, rows []ConversationResponse) {
	ps, ok := s.parityFix2()
	if !ok {
		return
	}
	var reqIDs []uuid.UUID
	for _, r := range rows {
		if r.IsRequest {
			reqIDs = append(reqIDs, r.ID)
		}
	}
	if len(reqIDs) == 0 {
		return
	}
	senderIDs, err := ps.RequestSenderIDs(ctx, reqIDs)
	if err != nil {
		s.log.Warn("request sender lookup failed — sender block degraded", "err", err)
		senderIDs = map[uuid.UUID]uuid.UUID{}
	}
	want := make(map[uuid.UUID]uuid.UUID, len(reqIDs))
	for _, id := range reqIDs {
		want[id] = senderIDs[id] // uuid.Nil when unknown
	}
	blocks := s.requestSenders(ctx, viewerID, want)
	for i := range rows {
		if rows[i].IsRequest {
			rows[i].Sender = blocks[rows[i].ID]
		}
	}
}

// --- POST /v1/chat/conversations/:id/report ---

// conversationReportEvidence bounds the message ids a conversation report
// carries (the newest the reporter can still see).
const conversationReportEvidence = 20

// ReportConversation reports a whole conversation from its chat settings:
// a DM reports the other person (a "user" report), a group reports the
// group (a "conversation" report), with the newest ≤20 messages the caller
// can still see as evidence. The outbox event is the durable record; the
// trust-safety filing is best effort, like message reports.
func (s *Service) ReportConversation(ctx context.Context, userID, convID uuid.UUID, reason, note string) error {
	if !reportReasons[reason] {
		return fmt.Errorf("%w: reason must be spam, harassment, hate, sexual, violence, scam or other", ErrInvalidReport)
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 500 {
		return fmt.Errorf("%w: note is at most 500 characters", ErrInvalidReport)
	}
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotMember
	}
	conv, err := s.convStore.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	if conv == nil {
		return ErrNotMember
	}
	msgs, _, err := s.GetMessages(ctx, userID, convID, nil, conversationReportEvidence)
	if err != nil {
		return err
	}
	payload := sharedEvents.ConversationReportedPayload{
		ConversationID: convID.String(), ConversationType: conv.Type, ReporterID: userID.String(),
		Reason: reason, Note: note, MessageIDs: make([]string, 0, len(msgs)), ReportedAt: s.clock().UTC(),
	}
	for _, m := range msgs {
		payload.MessageIDs = append(payload.MessageIDs, m.MsgID.String())
	}
	if conv.Type == "direct" {
		members, err := s.convStore.GetMembers(ctx, convID)
		if err != nil {
			return err
		}
		for _, m := range members {
			if m.UserID != userID {
				payload.ReportedUserID = m.UserID.String()
				break
			}
		}
		if payload.ReportedUserID == "" {
			return fmt.Errorf("%w: there is no one else in this chat to report", ErrInvalidReport)
		}
		payload.EntityType, payload.EntityID = "user", payload.ReportedUserID
	} else {
		payload.EntityType, payload.EntityID = "conversation", convID.String()
	}
	newest := "none"
	if len(msgs) > 0 {
		newest = msgs[0].MsgID.String()
	}
	// One report per reporter per conversation state: a retry dedupes, a
	// report after new messages is a new one.
	dedupe := "conversation-report:" + userID.String() + ":" + convID.String() + ":" + newest
	if ds, ok := s.convStore.(deliveryStore); ok {
		if err := ds.InsertOutboxEventOnce(ctx, sharedEvents.ConversationReported+":"+dedupe, sharedEvents.ConversationReported, payload); err != nil {
			return err
		}
	}
	s.fileConversationReport(ctx, userID, payload, msgs, "chat-conv-report:"+userID.String()+":"+convID.String()+":"+newest)
	return nil
}

// ConversationReportPath is the trust-safety service-only route for a
// conversation report. Body:
//
//	{"reporter_id", "entity_type": "user"|"conversation", "entity_id",
//	 "conversation_id", "conversation_type", "reason", "note"?,
//	 "message_ids": [≤20, newest first], "idempotency_key"}
//
// 201 created / 200 duplicate, like the message route. Until trust-safety
// serves it (404/405), the report is filed on the message route instead,
// against the newest visible message someone else sent (see below).
const ConversationReportPath = "/v1/internal/reports/conversations"

func (s *Service) fileConversationReport(ctx context.Context, reporterID uuid.UUID, p sharedEvents.ConversationReportedPayload, msgs []MessageResponse, key string) {
	if s.trustSafetyURL == "" || s.httpClient == nil || s.internalServiceKey == "" {
		return
	}
	body := map[string]any{
		"reporter_id": reporterID.String(), "entity_type": p.EntityType, "entity_id": p.EntityID,
		"conversation_id": p.ConversationID, "conversation_type": p.ConversationType,
		"reason": p.Reason, "message_ids": p.MessageIDs, "idempotency_key": key,
	}
	if p.Note != "" {
		body["note"] = p.Note
	}
	status, err := s.postTrustSafety(ctx, ConversationReportPath, body)
	switch {
	case err != nil:
		s.log.Warn("trust-safety conversation report not filed; outbox event stands", "err", err)
		return
	case status == http.StatusOK || status == http.StatusCreated:
		return
	case status != http.StatusNotFound && status != http.StatusMethodNotAllowed:
		s.log.Warn("trust-safety conversation report refused; outbox event stands", "status", status)
		return
	}
	// Fallback while the conversation route is not deployed: file it as a
	// message report on the newest visible message from someone else, the
	// note naming it a conversation report, so it still reaches the queue.
	var target *MessageResponse
	for i := range msgs {
		if msgs[i].SenderID != reporterID {
			target = &msgs[i]
			break
		}
	}
	if target == nil {
		s.log.Warn("conversation report: trust-safety has no conversation route and there is no message to file it on; outbox event stands",
			"conversation_id", p.ConversationID)
		return
	}
	note := fmt.Sprintf("Reported the whole %s from chat settings (%d recent messages as evidence).", conversationNoun(p.ConversationType), len(p.MessageIDs))
	if p.Note != "" {
		note += " " + p.Note
	}
	snap := sharedEvents.MessageReportedPayload{
		EntityType: "message", EntityID: p.ConversationID + ":" + target.MsgID.String(), ConversationID: p.ConversationID,
		MessageID: target.MsgID.String(), ReporterID: reporterID.String(), SenderID: target.SenderID.String(),
		Reason: p.Reason, Note: note, Text: target.Text, MessageType: target.Type,
	}
	if target.MediaID != nil {
		snap.MediaID = target.MediaID.String()
	}
	fallback := map[string]any{
		"reporter_id": reporterID.String(), "entity_id": snap.EntityID, "reason": snap.Reason, "note": snap.Note,
		"evidence": map[string]string{"text": snap.Text, "media_id": snap.MediaID,
			"sender_id": snap.SenderID, "message_type": snap.MessageType},
		"idempotency_key": key,
	}
	status, err = s.postTrustSafety(ctx, "/v1/internal/reports/messages", fallback)
	if err != nil || (status != http.StatusOK && status != http.StatusCreated) {
		s.log.Warn("trust-safety conversation report (message fallback) not filed; outbox event stands", "err", err, "status", status)
	}
}

func conversationNoun(convType string) string {
	if convType == "direct" {
		return "chat"
	}
	return "group"
}

// postTrustSafety posts a service-only body (internal key, no end-user
// identity headers) and returns the status.
func (s *Service) postTrustSafety(ctx context.Context, path string, body any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.trustSafetyURL+path, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// --- media.sensitive backfill ---

// mediaInfoBackfillCap bounds the media-service lookups one history page
// may make; mediaInfoBackfillParallel bounds them in flight.
const (
	mediaInfoBackfillCap      = 20
	mediaInfoBackfillParallel = 5
	mediaInfoBackfillBudget   = 2 * time.Second
	// A settled verdict is cached for a day; an unknown one (no scan yet,
	// or the record could not be read) is asked again after a minute.
	mediaInfoKnownTTL   = 24 * time.Hour
	mediaInfoUnknownTTL = time.Minute
)

func mediaInfoCacheKey(id uuid.UUID) string { return "chat:mediainfo:" + id.String() }

// fillMediaSensitivity completes media metadata captured at send time:
// moderation usually finishes after the message is sent, so media.sensitive
// is still null on the stored copy. For image / video attachments whose
// verdict (or whole metadata) is unknown it asks media-service again,
// cached in Redis, bounded per page, and never fails the page.
func (s *Service) fillMediaSensitivity(ctx context.Context, msgs []MessageResponse) {
	if s.mediaServiceURL == "" || s.internalServiceKey == "" || s.httpClient == nil {
		return
	}
	var ids []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, m := range msgs {
		if m.Type != MessageTypeMedia || m.MediaID == nil || seen[*m.MediaID] {
			continue
		}
		if m.Media != nil && (m.Media.Sensitive != nil || m.Media.Kind == "audio") {
			continue
		}
		if len(ids) >= mediaInfoBackfillCap {
			break
		}
		seen[*m.MediaID] = true
		ids = append(ids, *m.MediaID)
	}
	if len(ids) == 0 {
		return
	}
	found := make(map[uuid.UUID]*MediaInfo, len(ids))
	var toFetch []uuid.UUID
	for _, id := range ids {
		if s.rdb == nil {
			toFetch = append(toFetch, id)
			continue
		}
		raw, err := s.rdb.Get(ctx, mediaInfoCacheKey(id)).Result()
		if err != nil {
			toFetch = append(toFetch, id)
			continue
		}
		if raw == "-" {
			continue // unknown, asked recently
		}
		var info MediaInfo
		if json.Unmarshal([]byte(raw), &info) == nil && info.Kind != "" {
			found[id] = &info
		}
	}
	if len(toFetch) > 0 {
		budget, cancel := context.WithTimeout(ctx, mediaInfoBackfillBudget)
		defer cancel()
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, mediaInfoBackfillParallel)
		for _, id := range toFetch {
			wg.Add(1)
			sem <- struct{}{}
			go func(id uuid.UUID) {
				defer wg.Done()
				defer func() { <-sem }()
				info := s.fetchMediaInfo(budget, id)
				if s.rdb != nil {
					val, ttl := "-", mediaInfoUnknownTTL
					if info != nil {
						if b, err := json.Marshal(info); err == nil {
							val = string(b)
						}
						if info.Sensitive != nil {
							ttl = mediaInfoKnownTTL
						}
					}
					s.rdb.Set(ctx, mediaInfoCacheKey(id), val, ttl)
				}
				if info != nil {
					mu.Lock()
					found[id] = info
					mu.Unlock()
				}
			}(id)
		}
		wg.Wait()
	}
	for i := range msgs {
		m := &msgs[i]
		if m.MediaID == nil {
			continue
		}
		info, ok := found[*m.MediaID]
		if !ok {
			continue
		}
		if m.Media == nil {
			cp := *info
			m.Media = &cp
			continue
		}
		if m.Media.Sensitive == nil && info.Sensitive != nil {
			cp := *m.Media
			v := *info.Sensitive
			cp.Sensitive = &v
			m.Media = &cp
		}
	}
}
