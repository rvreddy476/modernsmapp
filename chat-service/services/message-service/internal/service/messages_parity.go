package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// Chat parity §2.3-§2.9 (lane B1).

var (
	// ErrMessageNotFound is a message the caller cannot see (404).
	ErrMessageNotFound = errors.New("message not found")
	// ErrInvalidReaction is an emoji outside one grapheme / 16 bytes.
	ErrInvalidReaction = errors.New("reaction must be a single emoji")
	// ErrInvalidForward is a malformed forward request.
	ErrInvalidForward = errors.New("invalid forward request")
	// ErrInvalidReport is an unknown report reason or an over-long note.
	ErrInvalidReport = errors.New("invalid report")
	// ErrNotPermittedDelete is an unsend the caller may not perform.
	ErrNotPermittedDelete = errors.New("not allowed to delete this message")
)

// applyHistoryCutoff drops the messages at or before the member's
// cleared_before and the ones they deleted for themselves. Any lookup error
// fails the read (never shows history the member cleared). Once the page
// reaches the cut-off there is nothing older to page to.
func (s *Service) applyHistoryCutoff(ctx context.Context, userID, convID uuid.UUID, msgs []scylla.Message, next *scylla.MessageCursor) ([]scylla.Message, *scylla.MessageCursor, error) {
	ps, ok := s.parity()
	if !ok {
		return msgs, next, nil
	}
	cut, err := ps.GetMemberCutoff(ctx, convID, userID)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]uuid.UUID, len(msgs))
	for i, m := range msgs {
		ids[i] = m.MsgID
	}
	hidden, err := ps.HiddenMessageIDs(ctx, userID, convID, ids)
	if err != nil {
		return nil, nil, err
	}
	out := msgs[:0:0]
	reachedCut := false
	for _, m := range msgs {
		if cut != nil && !m.Ts.After(*cut) {
			reachedCut = true
			continue
		}
		if hidden[m.MsgID] {
			continue
		}
		out = append(out, m)
	}
	if reachedCut {
		next = nil
	}
	return out, next, nil
}

// locateMessage resolves a message's partition key: from the client's ts
// when given, else from the durable delivery intent.
func (s *Service) locateMessage(ctx context.Context, convID, msgID uuid.UUID, tsHint *time.Time) (string, time.Time, error) {
	if tsHint != nil && !tsHint.IsZero() {
		return tsHint.UTC().Format("200601"), *tsHint, nil
	}
	ps, ok := s.parity()
	if !ok {
		return "", time.Time{}, ErrMessageNotFound
	}
	bucket, ts, _, found, err := ps.LookupMessageTS(ctx, convID, msgID)
	if err != nil {
		return "", time.Time{}, err
	}
	if !found {
		return "", time.Time{}, ErrMessageNotFound
	}
	return bucket, ts, nil
}

// visibleMessage loads a live message the member may still see (inside
// their history, not deleted for them).
func (s *Service) visibleMessage(ctx context.Context, userID, convID, msgID uuid.UUID, tsHint *time.Time) (*scylla.Message, error) {
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}
	bucket, ts, err := s.locateMessage(ctx, convID, msgID, tsHint)
	if err != nil {
		return nil, err
	}
	msg, err := s.msgStore.GetMessage(ctx, convID, bucket, ts, msgID)
	if err != nil {
		return nil, err
	}
	if msg == nil || msg.IsDeleted {
		return nil, ErrMessageNotFound
	}
	// A disappearing message past its expiry is gone even before the
	// worker deletes it (no forwarding, reacting or reporting it).
	if len(dropExpired([]scylla.Message{*msg}, s.clock())) == 0 {
		return nil, ErrMessageNotFound
	}
	kept, _, err := s.applyHistoryCutoff(ctx, userID, convID, []scylla.Message{*msg}, nil)
	if err != nil {
		return nil, err
	}
	if len(kept) == 0 {
		return nil, ErrMessageNotFound
	}
	return msg, nil
}

// --- Delete for me (§2.3) ---

// DeleteMessageForMe hides one message for the caller only.
func (s *Service) DeleteMessageForMe(ctx context.Context, userID, convID, msgID uuid.UUID, tsHint *time.Time) error {
	msg, err := s.visibleMessage(ctx, userID, convID, msgID, tsHint)
	if err != nil {
		return err
	}
	ps, ok := s.parity()
	if !ok {
		return errors.New("not supported")
	}
	// Hiding an unread message also takes it out of the caller's unread count.
	return ps.HideMessageForMe(ctx, userID, convID, msgID, msg.SenderID, msg.Ts)
}

// LocateMessage exposes the partition lookup to the handler (delete with no
// ts in the body).
func (s *Service) LocateMessage(ctx context.Context, convID, msgID uuid.UUID) (string, time.Time, error) {
	return s.locateMessage(ctx, convID, msgID, nil)
}

// publishToConversation fans a realtime frame out to every member's
// personal channel and to the conversation room, like the message frame.
func (s *Service) publishToConversation(convID uuid.UUID, members []postgres.Member, frameType string, body map[string]interface{}) {
	if s.rdb == nil {
		return
	}
	go func() {
		pubCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		payload, _ := json.Marshal(map[string]interface{}{"type": frameType, "payload": body})
		pipe := s.rdb.Pipeline()
		for _, m := range members {
			pipe.Publish(pubCtx, fmt.Sprintf("chat:%s", m.UserID), payload)
		}
		pipe.Publish(pubCtx, fmt.Sprintf("convroom:%s", convID), payload)
		if _, err := pipe.Exec(pubCtx); err != nil {
			s.log.Warn("failed to publish frame", "type", frameType, "err", err)
		}
	}()
}

// --- Forward (§2.4) ---

const forwardMaxTargets = 5

// ForwardResult is one target's outcome.
type ForwardResult struct {
	ConversationID uuid.UUID  `json:"conversation_id"`
	Outcome        string     `json:"outcome"`
	MessageID      *uuid.UUID `json:"message_id,omitempty"`
}

// ForwardResponse is POST .../forward.
type ForwardResponse struct {
	Results []ForwardResult `json:"results"`
}

func forwardOutcome(err error) string {
	switch {
	case err == nil:
		return "sent"
	case errors.Is(err, ErrMessagingNotAllowed), errors.Is(err, ErrRequestFirstMessageInvalid),
		errors.Is(err, ErrAwaitingRequestAcceptance), errors.Is(err, ErrFirstMovePending),
		errors.Is(err, ErrMediaNotAllowed):
		return "blocked"
	default:
		return "unavailable"
	}
}

// ForwardMessage copies one visible message into up to five conversations
// the caller belongs to. Every target runs the full send pipeline (who may
// message whom, chat pause, request rules, rate limit); the copy is marked
// forwarded and names only the forwarder.
func (s *Service) ForwardMessage(ctx context.Context, userID, convID, msgID uuid.UUID, tsHint *time.Time, targets []uuid.UUID, idempotencyKey string) (*ForwardResponse, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return nil, ErrIdempotencyKeyRequired
	}
	if len(targets) == 0 || len(targets) > forwardMaxTargets {
		return nil, fmt.Errorf("%w: 1..%d conversation_ids", ErrInvalidForward, forwardMaxTargets)
	}
	seen := map[uuid.UUID]bool{}
	for _, t := range targets {
		if t == uuid.Nil || seen[t] {
			return nil, fmt.Errorf("%w: conversation_ids must be distinct", ErrInvalidForward)
		}
		seen[t] = true
	}
	src, err := s.visibleMessage(ctx, userID, convID, msgID, tsHint)
	if err != nil {
		return nil, err
	}
	extra := decodeExtra(src.Extra)
	if extra == nil {
		extra = &MessageExtra{}
	}
	base := SendInput{
		Type: src.Type, Text: src.Text, MediaID: src.MediaID, PostID: src.PostID,
		Forwarded: true, forwardedExtra: extra, forwardedMediaOK: src.MediaID != nil,
		ProfileUserID: extra.ProfileUserID, SoundID: extra.SoundID, StreamID: extra.StreamID,
	}
	if base.Type == "" {
		base.Type = MessageTypeText
	}
	if extra.Voice != nil {
		base.DurationMs, base.Waveform = extra.Voice.DurationMs, extra.Voice.Waveform
	}
	if extra.Sticker != nil {
		id := extra.Sticker.ID
		base.StickerID = &id
	}
	if extra.GIF != nil {
		g := *extra.GIF
		base.GIF = &g
	}
	out := &ForwardResponse{Results: make([]ForwardResult, 0, len(targets))}
	for _, target := range targets {
		in := base
		in.IdempotencyKey = "fwd:" + idempotencyKey + ":" + msgID.String() + ":" + target.String()
		resp, err := s.SendRich(ctx, userID, target, in)
		res := ForwardResult{ConversationID: target, Outcome: forwardOutcome(err)}
		if err == nil {
			id := resp.MsgID
			res.MessageID = &id
		} else if res.Outcome == "unavailable" && !errors.Is(err, ErrMatchClosed) && err.Error() != "not a conversation member" {
			s.log.Warn("forward to target failed", "err", err, "conversation_id", target)
		}
		out.Results = append(out.Results, res)
	}
	return out, nil
}

// --- Reactions list (§2.5) ---

// ReactionUser is one reactor.
type ReactionUser struct {
	UserID      uuid.UUID `json:"user_id"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url"`
}

// ReactionGroup is one emoji with who chose it.
type ReactionGroup struct {
	Emoji string         `json:"emoji"`
	Count int            `json:"count"`
	Users []ReactionUser `json:"users"`
}

// ListReactions returns a message's reactions grouped by emoji, most used
// first (ties by emoji), reactors oldest first.
func (s *Service) ListReactions(ctx context.Context, userID, convID, msgID uuid.UUID, tsHint *time.Time) ([]ReactionGroup, error) {
	msg, err := s.visibleMessage(ctx, userID, convID, msgID, tsHint)
	if err != nil {
		return nil, err
	}
	rx, err := s.msgStore.GetReactionsForMessages(ctx, convID, msg.Bucket, []scylla.MsgKey{{Ts: msg.Ts, MsgID: msg.MsgID}})
	if err != nil {
		return nil, err
	}
	rows := rx[msg.MsgID]
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.Before(rows[j].CreatedAt) })
	var userIDs []uuid.UUID
	seenUser := map[uuid.UUID]bool{}
	for _, r := range rows {
		if !seenUser[r.UserID] {
			seenUser[r.UserID] = true
			userIDs = append(userIDs, r.UserID)
		}
	}
	cards := s.authorCards(ctx, userID, userIDs)
	groups := map[string]*ReactionGroup{}
	var order []string
	for _, r := range rows {
		g, ok := groups[r.Emoji]
		if !ok {
			g = &ReactionGroup{Emoji: r.Emoji, Users: []ReactionUser{}}
			groups[r.Emoji] = g
			order = append(order, r.Emoji)
		}
		c := cards[r.UserID]
		g.Users = append(g.Users, ReactionUser{UserID: r.UserID, DisplayName: c.DisplayName, AvatarURL: c.AvatarURL})
		g.Count++
	}
	out := make([]ReactionGroup, 0, len(order))
	for _, e := range order {
		out = append(out, *groups[e])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Emoji < out[j].Emoji
	})
	return out, nil
}

// --- Report (§2.6) ---

// reportReasons is the CONTRACT §2.6 reason enum (trust-safety maps it).
var reportReasons = map[string]bool{
	"spam": true, "harassment": true, "hate": true, "sexual": true, "violence": true, "scam": true, "other": true,
}

// ReportMessage files a member's report of one message. The durable outbox
// event (MessageReported) is the record of truth; the trust-safety report is
// also filed directly, best-effort (it dedupes repeat reports itself).
func (s *Service) ReportMessage(ctx context.Context, userID, convID, msgID uuid.UUID, tsHint *time.Time, reason, note string) error {
	if !reportReasons[reason] {
		return fmt.Errorf("%w: reason must be spam, harassment, hate, sexual, violence, scam or other", ErrInvalidReport)
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 500 {
		return fmt.Errorf("%w: note is at most 500 characters", ErrInvalidReport)
	}
	msg, err := s.visibleMessage(ctx, userID, convID, msgID, tsHint)
	if err != nil {
		return err
	}
	if msg.SenderID == userID {
		return fmt.Errorf("%w: you cannot report your own message", ErrInvalidReport)
	}
	entityID := convID.String() + ":" + msgID.String()
	payload := sharedEvents.MessageReportedPayload{
		EntityType: "message", EntityID: entityID, ConversationID: convID.String(), MessageID: msgID.String(),
		ReporterID: userID.String(), SenderID: msg.SenderID.String(), Reason: reason, Note: note,
		Text: msg.Text, MessageType: msg.Type, ReportedAt: s.clock().UTC(),
	}
	if msg.MediaID != nil {
		payload.MediaID = msg.MediaID.String()
	}
	if ds, ok := s.convStore.(deliveryStore); ok {
		if err := ds.InsertOutboxEventOnce(ctx, "message-report:"+userID.String()+":"+msgID.String(), sharedEvents.MessageReported, payload); err != nil {
			return err
		}
	}
	s.fileTrustSafetyReport(ctx, userID, entityID, payload)
	return nil
}

// SetTrustSafetyService wires the trust-safety base URL for message reports.
func (s *Service) SetTrustSafetyService(base string) { s.trustSafetyURL = strings.TrimRight(base, "/") }

// fileTrustSafetyReport posts to trust-safety's service-only message-report
// route (lane B4): internal key, no end-user identity headers, the reporter
// in the body, idempotent on (reporter, message). 201 created, 200 duplicate.
func (s *Service) fileTrustSafetyReport(ctx context.Context, reporterID uuid.UUID, entityID string, snap sharedEvents.MessageReportedPayload) {
	if s.trustSafetyURL == "" || s.httpClient == nil || s.internalServiceKey == "" {
		return
	}
	payload := map[string]any{
		"reporter_id": reporterID.String(),
		"entity_id":   entityID,
		"reason":      snap.Reason,
		"evidence": map[string]string{"text": snap.Text, "media_id": snap.MediaID,
			"sender_id": snap.SenderID, "message_type": snap.MessageType},
		"idempotency_key": "chat-msg-report:" + reporterID.String() + ":" + snap.MessageID,
	}
	if snap.Note != "" {
		payload["note"] = snap.Note
	}
	body, _ := json.Marshal(payload)
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.trustSafetyURL+"/v1/internal/reports/messages", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.log.Warn("trust-safety message report not filed; outbox event stands", "err", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		s.log.Warn("trust-safety message report refused; outbox event stands", "status", resp.StatusCode)
	}
}

// --- Read summary (§2.9) ---

// SeenBy is one reader of a message.
type SeenBy struct {
	UserID      uuid.UUID `json:"user_id"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url"`
	At          time.Time `json:"at"`
}

// ReadSummary is GET .../read-summary.
type ReadSummary struct {
	SeenBy []SeenBy `json:"seen_by"`
	Count  int      `json:"count"`
}

// ReadSummaryFor lists who has read a message, each reader disclosed only
// under their own read-receipt setting (the rule every receipt path uses).
func (s *Service) ReadSummaryFor(ctx context.Context, viewerID, convID, msgID uuid.UUID, tsHint *time.Time) (*ReadSummary, error) {
	msg, err := s.visibleMessage(ctx, viewerID, convID, msgID, tsHint)
	if err != nil {
		return nil, err
	}
	// DM consent: a request conversation discloses no other reader.
	conv, err := s.convStore.GetConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	requestGated := conv == nil || conv.IsRequest
	members, err := s.convStore.GetMembers(ctx, convID)
	if err != nil {
		return nil, err
	}
	cursors, err := s.groupStore().GetConversationReadCursors(ctx, convID)
	if err != nil {
		return nil, err
	}
	type reader struct {
		id uuid.UUID
		at time.Time
	}
	var readers []reader
	for _, m := range members {
		if m.UserID == msg.SenderID {
			continue
		}
		rc, ok := cursors[m.UserID]
		if !ok || rc.LastReadAt.Before(msg.Ts) {
			continue
		}
		if m.UserID != viewerID && (requestGated || !s.receiptDisclosed(ctx, convID, viewerID, m.UserID)) {
			continue
		}
		readers = append(readers, reader{m.UserID, rc.LastReadAt.UTC()})
	}
	sort.Slice(readers, func(i, j int) bool { return readers[i].at.Before(readers[j].at) })
	ids := make([]uuid.UUID, len(readers))
	for i, r := range readers {
		ids[i] = r.id
	}
	cards := s.authorCards(ctx, viewerID, ids)
	out := &ReadSummary{SeenBy: make([]SeenBy, 0, len(readers))}
	for _, r := range readers {
		c := cards[r.id]
		out.SeenBy = append(out.SeenBy, SeenBy{UserID: r.id, DisplayName: c.DisplayName, AvatarURL: c.AvatarURL, At: r.at})
	}
	out.Count = len(out.SeenBy)
	return out, nil
}

// --- Shared posts / media (§1.4) ---

// SharedPost is one shared post card.
type SharedPost struct {
	MessageID uuid.UUID `json:"message_id"`
	PostID    uuid.UUID `json:"post_id"`
	SenderID  uuid.UUID `json:"sender_id"`
	At        time.Time `json:"at"`
}

// SharedMedia is one shared attachment.
type SharedMedia struct {
	MessageID uuid.UUID `json:"message_id"`
	MediaID   uuid.UUID `json:"media_id"`
	Kind      *string   `json:"kind"`
	SenderID  uuid.UUID `json:"sender_id"`
	At        time.Time `json:"at"`
}

// SharedCursor pages the shared tabs.
type SharedCursor struct {
	At time.Time `json:"t"`
	ID uuid.UUID `json:"m"`
}

// ListShared returns the conversation's shared posts or media for the
// caller (cut-off and delete-for-me respected), newest first.
func (s *Service) ListShared(ctx context.Context, userID, convID uuid.UUID, kind string, cursor *SharedCursor, limit int) (interface{}, *SharedCursor, error) {
	if kind != "posts" && kind != "media" {
		return nil, nil, fmt.Errorf("%w: kind must be posts or media", ErrInvalidMessageContent)
	}
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, ErrNotMember
	}
	ps, okp := s.parity()
	if !okp {
		return []SharedPost{}, nil, nil
	}
	cut, err := ps.GetMemberCutoff(ctx, convID, userID)
	if err != nil {
		return nil, nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	var beforeTs *time.Time
	var beforeID *uuid.UUID
	if cursor != nil {
		beforeTs, beforeID = &cursor.At, &cursor.ID
	}
	storeKind := "post"
	if kind == "media" {
		storeKind = "media"
	}
	items, err := ps.ListSharedItems(ctx, convID, userID, storeKind, cut, beforeTs, beforeID, limit)
	if err != nil {
		return nil, nil, err
	}
	var next *SharedCursor
	if len(items) == limit {
		last := items[len(items)-1]
		next = &SharedCursor{At: last.SentAt, ID: last.MessageID}
	}
	if kind == "posts" {
		out := make([]SharedPost, 0, len(items))
		for _, it := range items {
			if it.PostID != nil {
				out = append(out, SharedPost{MessageID: it.MessageID, PostID: *it.PostID, SenderID: it.SenderID, At: it.SentAt.UTC()})
			}
		}
		return out, next, nil
	}
	out := make([]SharedMedia, 0, len(items))
	for _, it := range items {
		if it.MediaID != nil {
			out = append(out, SharedMedia{MessageID: it.MessageID, MediaID: *it.MediaID, Kind: it.MediaKind, SenderID: it.SenderID, At: it.SentAt.UTC()})
		}
	}
	return out, next, nil
}

// SetTypingState is POST /typing with an explicit is_typing (CONTRACT §4:
// the REST route gains {"is_typing": false}). true is exactly SetTyping;
// false clears the indicator and tells the other members it stopped.
func (s *Service) SetTypingState(ctx context.Context, userID, convID uuid.UUID, isTyping bool) error {
	if isTyping {
		return s.SetTyping(ctx, userID, convID)
	}
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("not a member of this conversation")
	}
	if p := s.GetChatPolicy(ctx, userID); !p.Known || p.ChatPaused || !p.SendTypingIndicators || s.isHidden(ctx, userID) {
		return nil
	}
	if s.rdb == nil {
		return nil
	}
	s.rdb.Del(ctx, fmt.Sprintf("typing:%s:%s", convID, userID))
	members, _ := s.convStore.GetMembers(ctx, convID)
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "typing",
		"payload": map[string]interface{}{
			"conversation_id": convID.String(),
			"user_id":         userID.String(),
			"is_typing":       false,
		},
	})
	for _, m := range members {
		if m.UserID != userID {
			s.rdb.Publish(ctx, fmt.Sprintf("chat:%s", m.UserID), payload)
		}
	}
	return nil
}
