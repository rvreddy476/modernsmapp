package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/atpost/chat-message-service/internal/ratelimit"
	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// WhatsApp-style message ticks (founder, 22 Oct 2026).
//
// For the viewer's OWN messages, in direct chats and groups:
//
//	sent      one grey tick    the server has it
//	delivered two grey ticks   every other active member's device received it
//	read      two coloured     every other active member opened it
//
// Both states are cursor comparisons, never per-message rows: a member's
// delivered cursor (chat.delivered_cursors, advanced by the client's ack)
// and read cursor (chat.read_cursors, advanced by mark-read). A message of
// mine is delivered when its ts is at or before every other member's
// effective delivered cursor, and read likewise for read cursors.
//
// A member who joined after a message was sent is not one of its
// recipients: their effective cursor is never below their join time, so a
// newcomer who has acked nothing does not turn old messages back to one
// tick.
//
// Privacy (WhatsApp):
//   - DELIVERED is always shown — even with read receipts off, and in a
//     pending request.
//   - READ is withheld while the conversation is a request, in a dating
//     conversation whose receipts dating has not allowed, and whenever
//     EITHER side has read receipts off (reciprocity: turn yours off and you
//     stop seeing other people's). See readReceiptVisible.

// Tick states on the wire (MessageResponse.Status).
const (
	TickSent      = "sent"
	TickDelivered = "delivered"
	TickRead      = "read"
)

// deliveredAckRateLimit bounds POST /conversations/:id/delivered. Clients
// batch to at most one ack per second per conversation; this allows a busy
// inbox (many chats receiving at once) and refuses a flood.
var deliveredAckRateLimit = ratelimit.Limit{Action: "delivered_ack", Max: 300, Window: time.Minute}

// policyResolveCap bounds the one-by-one policy fetches a single aggregate
// may make for members whose projection row is missing or stale (the rest
// come from one batch query). Beyond it a member's receipt counts as
// withheld for this response — fail closed, never a slow page.
const policyResolveCap = 32

// ReceiptWatermarks is the viewer-scoped aggregate on the conversation
// response: every own message with ts <= read_up_to is read, else ts <=
// delivered_up_to is delivered, else sent. Nil means no such message yet
// (read_up_to is nil whenever read is withheld).
type ReceiptWatermarks struct {
	DeliveredUpTo *time.Time `json:"delivered_up_to"`
	ReadUpTo      *time.Time `json:"read_up_to"`
}

// DeliveredAck is the POST /conversations/:id/delivered response.
type DeliveredAck struct {
	DeliveredUpTo *time.Time `json:"delivered_up_to"`
	Advanced      bool       `json:"advanced"`
}

// receiptStore is the cursor surface ticks need (ConversationStore
// implements it; a test fake that does not simply gets no ticks).
type receiptStore interface {
	UpsertDeliveredCursor(ctx context.Context, conversationID, userID uuid.UUID, messageID *uuid.UUID, deliveredAt time.Time) (bool, error)
	GetConversationDeliveredCursors(ctx context.Context, conversationID uuid.UUID) (map[uuid.UUID]postgres.DeliveredCursor, error)
	GetOtherMembersDeliveredCursors(ctx context.Context, viewerID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]map[uuid.UUID]postgres.DeliveredCursor, error)
	GetUserPolicies(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]postgres.UserPolicy, error)
	GetReadCursor(ctx context.Context, conversationID, userID uuid.UUID) (*postgres.ReadCursor, error)
}

func (s *Service) receiptStore() (receiptStore, bool) {
	rs, ok := s.convStore.(receiptStore)
	return rs, ok
}

// --- the pure rules (unit-tested, mutation-checked) ---

// receiptsOff is one side's "my read receipts are not shared" state. An
// unknown policy (identity unreachable) counts as off: disclosure fails
// closed.
func receiptsOff(p ChatPolicy) bool {
	return !p.Known || p.ChatPaused || p.ReadReceiptsVisibility == "no_one"
}

// readReceiptVisible is the ONE disclosure rule for a read receipt between
// two members of an accepted (non-request) conversation: may VIEWER see that
// READER has read?
//
//   - the reader's receipts off -> no
//   - the viewer's receipts off -> no (reciprocity, as on WhatsApp)
//   - everyone                  -> yes
//   - connections_only          -> yes: an accepted conversation IS the
//     connection. The graph's connections_only means a legacy accepted
//     connection, which the DM consent model (20 Oct) no longer creates, so
//     asking graph withheld every receipt from almost every pair — the
//     "single tick after they replied" bug. The call rule made the same move
//     (accepted_chats_only grants on an accepted 1:1 chat).
//   - anything else             -> no (an unknown value fails closed)
//
// Callers apply the conversation gates first (request pending, dating).
func readReceiptVisible(viewer, reader ChatPolicy) bool {
	if receiptsOff(reader) || receiptsOff(viewer) {
		return false
	}
	switch reader.ReadReceiptsVisibility {
	case "everyone", "connections_only":
		return true
	default:
		return false
	}
}

// receiptPeer is one OTHER member's inputs to the viewer's ticks.
type receiptPeer struct {
	UserID        uuid.UUID
	JoinedAt      time.Time
	Delivered     time.Time // delivered cursor; zero = none
	Read          time.Time // read cursor; zero = none
	ReadDisclosed bool      // readReceiptVisible for this viewer
}

func maxTime(ts ...time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if t.After(out) {
			out = t
		}
	}
	return out
}

// deliveredEff: reading implies receiving, and a member is not a recipient
// of what was sent before they joined.
func deliveredEff(p receiptPeer) time.Time {
	return maxTime(p.Delivered, p.Read, p.JoinedAt)
}

// readEff: a withheld reader contributes only their join time — they never
// make a message read, but they do not block messages sent before they
// joined either.
func readEff(p receiptPeer) time.Time {
	if !p.ReadDisclosed {
		return p.JoinedAt
	}
	return maxTime(p.Read, p.JoinedAt)
}

// computeWatermarks folds the other members into the viewer's watermarks.
// readAllowed carries the conversation-level gates (not a request, dating
// allows). In a DIRECT chat a withheld reader means no read at all (the
// join-time floor exists for group newcomers only).
func computeWatermarks(peers []receiptPeer, direct, readAllowed bool) ReceiptWatermarks {
	var w ReceiptWatermarks
	if len(peers) == 0 {
		return w
	}
	var dMin, rMin time.Time
	readOK := readAllowed
	for i, p := range peers {
		d, r := deliveredEff(p), readEff(p)
		if i == 0 || d.Before(dMin) {
			dMin = d
		}
		if i == 0 || r.Before(rMin) {
			rMin = r
		}
		if direct && !p.ReadDisclosed {
			readOK = false
		}
	}
	if !dMin.IsZero() {
		d := dMin
		w.DeliveredUpTo = &d
	}
	if readOK && !rMin.IsZero() {
		r := rMin
		w.ReadUpTo = &r
	}
	return w
}

// tickFor is the sender's tick for one of their messages.
func tickFor(ts time.Time, w ReceiptWatermarks) string {
	if w.ReadUpTo != nil && !ts.After(*w.ReadUpTo) {
		return TickRead
	}
	if w.DeliveredUpTo != nil && !ts.After(*w.DeliveredUpTo) {
		return TickDelivered
	}
	return TickSent
}

// minIndex answers "the minimum over everyone except V" for every V in one
// pass (the two smallest values), so a group frame fan-out is O(members),
// not O(members^2).
type minIndex struct {
	n          int
	min1, min2 time.Time
	arg1       uuid.UUID
}

func newMinIndex(vals map[uuid.UUID]time.Time) minIndex {
	var mi minIndex
	for id, t := range vals {
		switch {
		case mi.n == 0 || t.Before(mi.min1):
			mi.min2 = mi.min1
			mi.min1, mi.arg1 = t, id
		case mi.n == 1 || t.Before(mi.min2):
			mi.min2 = t
		}
		mi.n++
	}
	return mi
}

// excluding returns the minimum over the set without v; ok=false when v
// was the only member (no constraint: +infinity).
func (mi minIndex) excluding(v uuid.UUID, vIn bool) (time.Time, bool) {
	if !vIn {
		return mi.min1, mi.n > 0
	}
	if mi.n <= 1 {
		return time.Time{}, false
	}
	if v == mi.arg1 {
		return mi.min2, true
	}
	return mi.min1, true
}

// aggregateMoves reports, for every member V other than the actor, the new
// "every other member" watermark when the actor's effective cursor moves
// from oldEff to newEff — only for the V whose watermark actually advanced.
// eff holds every member's effective value (the actor's NEW one included).
func aggregateMoves(eff map[uuid.UUID]time.Time, actor uuid.UUID, oldEff, newEff time.Time) map[uuid.UUID]time.Time {
	out := map[uuid.UUID]time.Time{}
	if !newEff.After(oldEff) {
		return out
	}
	rest := make(map[uuid.UUID]time.Time, len(eff))
	for id, t := range eff {
		if id != actor {
			rest[id] = t
		}
	}
	mi := newMinIndex(rest)
	for v := range rest {
		before, after := oldEff, newEff
		if m, ok := mi.excluding(v, true); ok {
			if m.Before(before) {
				before = m
			}
			if m.Before(after) {
				after = m
			}
		}
		if after.After(before) {
			out[v] = after
		}
	}
	return out
}

// --- loading ---

// receiptState is one conversation's receipt inputs, loaded once.
type receiptState struct {
	conv      *postgres.Conversation
	members   []postgres.Member
	delivered map[uuid.UUID]time.Time
	read      map[uuid.UUID]time.Time
	policies  map[uuid.UUID]ChatPolicy
	gated     bool // dating mechanic M9
}

func (s *Service) loadReceiptState(ctx context.Context, convID uuid.UUID, withPolicies bool) (*receiptState, error) {
	rs, ok := s.receiptStore()
	if !ok {
		return nil, errors.New("receipt store unavailable")
	}
	conv, err := s.convStore.GetConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return nil, ErrNotMember
	}
	members, err := s.convStore.GetMembers(ctx, convID)
	if err != nil {
		return nil, err
	}
	st := &receiptState{conv: conv, members: members,
		delivered: map[uuid.UUID]time.Time{}, read: map[uuid.UUID]time.Time{}}
	dcs, err := rs.GetConversationDeliveredCursors(ctx, convID)
	if err != nil {
		return nil, err
	}
	for id, c := range dcs {
		st.delivered[id] = c.LastDeliveredAt
	}
	rcs, err := s.groupStore().GetConversationReadCursors(ctx, convID)
	if err != nil {
		return nil, err
	}
	for id, c := range rcs {
		st.read[id] = c.LastReadAt
	}
	if meta, err := s.convStore.GetConversationMeta(ctx, convID); err == nil && meta != nil {
		st.gated = meta.ReceiptsGated
	}
	if withPolicies {
		ids := make([]uuid.UUID, len(members))
		for i, m := range members {
			ids[i] = m.UserID
		}
		st.policies = s.chatPoliciesFor(ctx, ids)
	}
	return st, nil
}

// chatPoliciesFor resolves many members' policies: one batch query for the
// fresh projection rows, then at most policyResolveCap individual resolves;
// anyone left over is unknown (receipts withheld for this response).
func (s *Service) chatPoliciesFor(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]ChatPolicy {
	out := make(map[uuid.UUID]ChatPolicy, len(ids))
	rows := map[uuid.UUID]postgres.UserPolicy{}
	if rs, ok := s.receiptStore(); ok {
		if got, err := rs.GetUserPolicies(ctx, ids); err == nil {
			rows = got
		} else {
			s.log.Warn("policy batch read failed", "err", err)
		}
	}
	resolved := 0
	for _, id := range ids {
		if row, ok := rows[id]; ok && time.Since(row.RefreshedAt) < policyMaxAge {
			out[id] = policyFromRow(row)
			continue
		}
		if resolved < policyResolveCap {
			resolved++
			out[id] = s.GetChatPolicy(ctx, id)
			continue
		}
		out[id] = ChatPolicy{Known: false, ReadReceiptsVisibility: "no_one"}
	}
	return out
}

// readGateOpen is the conversation-level read gate for viewer.
func (s *Service) readGateOpen(ctx context.Context, st *receiptState, viewerID uuid.UUID) bool {
	if st.conv.IsRequest {
		return false
	}
	if st.gated && !s.datingReceiptsAllowed(ctx, st.conv.ID, viewerID) {
		return false
	}
	return true
}

// peersFor builds the viewer's view of every other active member.
func (st *receiptState) peersFor(viewerID uuid.UUID) []receiptPeer {
	viewerPolicy := st.policies[viewerID]
	peers := make([]receiptPeer, 0, len(st.members))
	for _, m := range st.members {
		if m.UserID == viewerID {
			continue
		}
		peers = append(peers, receiptPeer{
			UserID:        m.UserID,
			JoinedAt:      m.JoinedAt,
			Delivered:     st.delivered[m.UserID],
			Read:          st.read[m.UserID],
			ReadDisclosed: readReceiptVisible(viewerPolicy, st.policies[m.UserID]),
		})
	}
	return peers
}

// receiptWatermarksFor computes the viewer's watermarks for one
// conversation; nil when the store cannot (ticks then stay "sent").
func (s *Service) receiptWatermarksFor(ctx context.Context, viewerID, convID uuid.UUID) *ReceiptWatermarks {
	st, err := s.loadReceiptState(ctx, convID, true)
	if err != nil {
		if !errors.Is(err, ErrNotMember) {
			s.log.Warn("receipt state load failed", "err", err, "conversation_id", convID)
		}
		return nil
	}
	w := computeWatermarks(st.peersFor(viewerID), st.conv.Type == "direct", s.readGateOpen(ctx, st, viewerID))
	return &w
}

// attachConversationReceipts sets the viewer's watermarks on a conversation
// response so a reload shows the right ticks.
func (s *Service) attachConversationReceipts(ctx context.Context, viewerID uuid.UUID, resp *ConversationResponse) {
	if resp == nil {
		return
	}
	resp.Receipts = s.receiptWatermarksFor(ctx, viewerID, resp.ID)
}

// attachTickStatus sets Status on every message the viewer sent in a
// history page. One state load per page, never per message.
func (s *Service) attachTickStatus(ctx context.Context, viewerID, convID uuid.UUID, msgs []MessageResponse) {
	own := false
	for i := range msgs {
		if msgs[i].SenderID == viewerID {
			own = true
			break
		}
	}
	if !own {
		return
	}
	w := s.receiptWatermarksFor(ctx, viewerID, convID)
	if w == nil {
		return
	}
	for i := range msgs {
		if msgs[i].SenderID != viewerID {
			continue
		}
		st := tickFor(msgs[i].Ts, *w)
		msgs[i].Status = &st
	}
}

// --- delivered ack ---

// MarkDelivered advances the caller's delivered cursor to the newest
// message their device has (ts, capped at now; now when absent) and tells
// every other member whose "delivered to everyone" watermark moved.
// Delivered is never privacy-gated (WhatsApp shows it with read receipts
// off and in a request).
func (s *Service) MarkDelivered(ctx context.Context, userID, convID uuid.UUID, messageID *uuid.UUID, ts *time.Time) (*DeliveredAck, error) {
	if err := s.checkRateLimit(ctx, deliveredAckRateLimit, userID); err != nil {
		return nil, err
	}
	ok, err := s.convStore.CheckMembership(ctx, convID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}
	rs, ok := s.receiptStore()
	if !ok {
		return nil, errors.New("receipt store unavailable")
	}
	now := s.clock().UTC()
	at := now
	if ts != nil && !ts.IsZero() && ts.Before(now) {
		at = ts.UTC()
	}
	// The state BEFORE the upsert: the frame fan-out needs the actor's old
	// effective cursor.
	st, err := s.loadReceiptState(ctx, convID, false)
	if err != nil {
		return nil, err
	}
	var joined time.Time
	for _, m := range st.members {
		if m.UserID == userID {
			joined = m.JoinedAt
		}
	}
	oldEff := deliveredEff(receiptPeer{JoinedAt: joined, Delivered: st.delivered[userID], Read: st.read[userID]})
	advanced, err := rs.UpsertDeliveredCursor(ctx, convID, userID, messageID, at)
	if err != nil {
		return nil, err
	}
	cursor := st.delivered[userID]
	if advanced {
		cursor = at
	}
	out := &DeliveredAck{Advanced: advanced}
	if !cursor.IsZero() {
		c := cursor
		out.DeliveredUpTo = &c
	}
	if !advanced {
		return out, nil
	}
	st.delivered[userID] = at
	eff := make(map[uuid.UUID]time.Time, len(st.members))
	for _, m := range st.members {
		eff[m.UserID] = deliveredEff(receiptPeer{JoinedAt: m.JoinedAt, Delivered: st.delivered[m.UserID], Read: st.read[m.UserID]})
	}
	msgIDStr := ""
	if messageID != nil {
		msgIDStr = messageID.String()
	}
	direct := st.conv.Type == "direct"
	for v, upTo := range aggregateMoves(eff, userID, oldEff, eff[userID]) {
		payload := map[string]interface{}{
			"conversation_id": convID.String(),
			"delivered_at":    at.Format(time.RFC3339Nano),
			"delivered_up_to": upTo.UTC().Format(time.RFC3339Nano),
		}
		// A group frame names no member (directive §3.5: aggregates only);
		// a DM's acker is the one other person anyway.
		if direct {
			payload["user_id"] = userID.String()
			payload["message_id"] = msgIDStr
		} else {
			payload["aggregate"] = true
		}
		s.publishFrame(ctx, st, v, "delivery_receipt", payload)
	}
	return out, nil
}

// --- read (called from MarkRead) ---

// priorReadAt is the reader's read cursor before MarkRead advances it.
func (s *Service) priorReadAt(ctx context.Context, convID, userID uuid.UUID) time.Time {
	rs, ok := s.receiptStore()
	if !ok {
		return time.Time{}
	}
	rc, err := rs.GetReadCursor(ctx, convID, userID)
	if err != nil || rc == nil {
		return time.Time{}
	}
	return rc.LastReadAt
}

// publishGroupReadAggregate sends the GROUP read frame after reader moved
// their read cursor from prevRead. Groups never get a per-reader frame
// (directive §3.5): each member whose "everyone has read" watermark moved
// receives only that watermark — {conversation_id, aggregate: true,
// read_up_to} — and only when read is visible to them (the request gate
// is the caller's; reciprocity and each reader's own setting apply here).
func (s *Service) publishGroupReadAggregate(ctx context.Context, convID, readerID uuid.UUID, prevRead time.Time) {
	if s.rdb == nil {
		return
	}
	st, err := s.loadReceiptState(ctx, convID, true)
	if err != nil || st.conv.IsRequest {
		return
	}
	var readerJoined time.Time
	for _, m := range st.members {
		if m.UserID == readerID {
			readerJoined = m.JoinedAt
		}
	}
	// The reader's read is the same disclosure for every viewer whose own
	// receipts are on (readReceiptVisible depends on the viewer only through
	// reciprocity, checked per recipient below), so the effective values are
	// computed against an "on" viewer.
	on := ChatPolicy{Known: true, ReadReceiptsVisibility: "everyone"}
	eff := make(map[uuid.UUID]time.Time, len(st.members))
	for _, m := range st.members {
		eff[m.UserID] = readEff(receiptPeer{JoinedAt: m.JoinedAt, Read: st.read[m.UserID],
			ReadDisclosed: readReceiptVisible(on, st.policies[m.UserID])})
	}
	readerOn := readReceiptVisible(on, st.policies[readerID])
	oldEff := readEff(receiptPeer{JoinedAt: readerJoined, Read: prevRead, ReadDisclosed: readerOn})
	for v, upTo := range aggregateMoves(eff, readerID, oldEff, eff[readerID]) {
		if receiptsOff(st.policies[v]) || !s.readGateOpen(ctx, st, v) {
			continue
		}
		s.publishFrame(ctx, st, v, "read_receipt", map[string]interface{}{
			"conversation_id": convID.String(),
			"aggregate":       true,
			"read_up_to":      upTo.UTC().Format(time.RFC3339Nano),
		})
	}
}

// publishFrame sends one receipt frame to a member's personal channel —
// through personalChannel, so a member who hid the conversation gets none. A
// recipient missing from the member list gets none either.
func (s *Service) publishFrame(ctx context.Context, st *receiptState, userID uuid.UUID, typ string, payload map[string]interface{}) {
	if s.rdb == nil || st == nil {
		return
	}
	raw, err := json.Marshal(map[string]interface{}{"type": typ, "payload": payload})
	if err != nil {
		return
	}
	for _, m := range st.members {
		if m.UserID == userID {
			publishPersonal(ctx, s.rdb, m, raw)
			return
		}
	}
}

// readDisclosedTo is readReceiptVisible with both policies resolved — the
// per-pair entry point for the existing receipt paths (inbox own status,
// read summary, conversation cursors, the direct live frame).
func (s *Service) readDisclosedTo(ctx context.Context, viewerID, readerID uuid.UUID) bool {
	return readReceiptVisible(s.GetChatPolicy(ctx, viewerID), s.GetChatPolicy(ctx, readerID))
}
