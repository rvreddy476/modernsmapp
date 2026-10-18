package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Chat parity programme (CONTRACT §1-3, §5-7, lane B1, 2026-10-18).
// Every method here is additive: the legacy paths keep their own queries.

// --- Policy helpers (§7) ---

// MaxFilteredKeywords bounds dm_filtered_keywords (CONTRACT §7).
const MaxFilteredKeywords = 100

// NormalizeKeywords lower-cases, trims, de-duplicates and bounds the
// recipient's filtered-words list (1..30 runes each, at most 100). Identity
// already normalises; this keeps the projection safe against any writer.
func NormalizeKeywords(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, k := range in {
		k = strings.ToLower(strings.TrimSpace(k))
		n := len([]rune(k))
		if n == 0 || n > 30 || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
		if len(out) == MaxFilteredKeywords {
			break
		}
	}
	return out
}

// BubbleStyles is the chat_bubble_style enum (CONTRACT §7).
var BubbleStyles = map[string]bool{"default": true, "ocean": true, "ember": true, "forest": true, "violet": true, "mono": true}

// BubbleOrDefault maps an unknown or empty bubble style to "default".
func BubbleOrDefault(v string) string {
	if BubbleStyles[v] {
		return v
	}
	return "default"
}

// --- Inbox (§1) ---

// InboxLane names the three inbox lists.
const (
	LanePrimary  = "primary"
	LaneRequests = "requests"
	LaneArchived = "archived"
)

// ValidLane reports whether lane is one of the three lists.
func ValidLane(lane string) bool {
	return lane == LanePrimary || lane == LaneRequests || lane == LaneArchived
}

// incomingRequest is true for a request conversation the viewer ($1) has to
// answer. A request row without an envelope is treated as incoming (it is
// excluded from primary rather than shown to the wrong side).
const incomingRequest = `(c.is_request AND COALESCE(mr.receiver_id = $1, true))`

func laneFilter(lane string) string {
	switch lane {
	case LaneRequests:
		return incomingRequest + ` AND c.request_declined_at IS NULL`
	case LaneArchived:
		return `COALESCE(s.label, '') = 'archived' AND NOT ` + incomingRequest
	default:
		return `COALESCE(s.label, '') <> 'archived' AND NOT ` + incomingRequest
	}
}

// visibleToMember hides a conversation the member deleted-for-me until a
// message newer than their cut-off arrives.
const visibleToMember = `(NOT m.hidden OR (c.last_message_at IS NOT NULL AND (m.cleared_before IS NULL OR c.last_message_at > m.cleared_before)))`

// ListInboxConversations returns one slice of an inbox lane. pinned=true
// returns the viewer's pinned conversations (newest pin first, no cursor);
// pinned=false pages the rest by (last activity, id) descending. SortAt on
// each row is the cursor value.
func (s *ConversationStore) ListInboxConversations(ctx context.Context, userID uuid.UUID, lane string, pinned bool, limit int, cursorTs *time.Time, cursorID *uuid.UUID) ([]Conversation, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	args := []any{userID, pinned}
	q := `
		SELECT c.id, c.type, c.title, c.created_by, c.is_request, c.created_at, c.updated_at,
		       c.avatar_media_id, c.last_message_at, c.last_message_preview, c.last_message_sender, c.description,
		       COALESCE(c.source_app, ''), c.match_id, COALESCE(c.last_message_at, c.created_at)
		FROM chat.conversations c
		JOIN chat.conversation_members m ON m.conversation_id = c.id AND m.user_id = $1 AND m.left_at IS NULL
		LEFT JOIN chat.conversation_settings s ON s.conversation_id = c.id AND s.user_id = $1
		LEFT JOIN chat.message_requests mr ON mr.conversation_id = c.id
		WHERE ` + visibleToMember + ` AND ` + laneFilter(lane) + `
		  AND COALESCE(s.is_pinned, false) = $2`
	if !pinned && cursorTs != nil && cursorID != nil {
		args = append(args, *cursorTs, *cursorID)
		q += ` AND (COALESCE(c.last_message_at, c.created_at), c.id) < ($3, $4)`
	}
	if pinned {
		q += ` ORDER BY s.pinned_at DESC NULLS LAST, c.id DESC`
	} else {
		q += ` ORDER BY COALESCE(c.last_message_at, c.created_at) DESC, c.id DESC`
	}
	args = append(args, limit)
	q += ` LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Type, &c.Title, &c.CreatedBy, &c.IsRequest, &c.CreatedAt, &c.UpdatedAt,
			&c.AvatarMediaID, &c.LastMessageAt, &c.LastMessagePreview, &c.LastMessageSender, &c.Description,
			&c.SourceApp, &c.MatchID, &c.SortAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// InboxMeta is the viewer-scoped state of one conversation row.
type InboxMeta struct {
	ConversationID  uuid.UUID
	UnreadCount     int
	ClearedBefore   *time.Time
	Hidden          bool
	LastMessageType string
	IsPinned        bool
	PinnedAt        *time.Time
	IsMuted         bool
	MuteUntil       *time.Time
	MuteCalls       bool
	AlertTone       string
	Label           *string
	// MarkedUnread is the member's own "mark as unread" (fix pass 2).
	MarkedUnread bool
}

// GetInboxMeta loads the viewer's member + settings state for a page in one
// query.
func (s *ConversationStore) GetInboxMeta(ctx context.Context, userID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]InboxMeta, error) {
	out := make(map[uuid.UUID]InboxMeta, len(convIDs))
	if len(convIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT m.conversation_id, m.unread_count, m.cleared_before, m.hidden, c.last_message_type,
		       COALESCE(s.is_pinned, false), s.pinned_at, COALESCE(s.is_muted, false), s.mute_until,
		       COALESCE(s.mute_calls, false), COALESCE(s.alert_tone, 'default'), s.label, m.marked_unread
		FROM chat.conversation_members m
		JOIN chat.conversations c ON c.id = m.conversation_id
		LEFT JOIN chat.conversation_settings s ON s.conversation_id = m.conversation_id AND s.user_id = m.user_id
		WHERE m.user_id = $1 AND m.conversation_id = ANY($2::uuid[])
	`, userID, convIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var im InboxMeta
		if err := rows.Scan(&im.ConversationID, &im.UnreadCount, &im.ClearedBefore, &im.Hidden, &im.LastMessageType,
			&im.IsPinned, &im.PinnedAt, &im.IsMuted, &im.MuteUntil, &im.MuteCalls, &im.AlertTone, &im.Label, &im.MarkedUnread); err != nil {
			return nil, err
		}
		out[im.ConversationID] = im
	}
	return out, rows.Err()
}

// GetOtherMembersReadCursors returns every OTHER member's read cursor for a
// page of conversations: conversation -> user -> cursor. One query.
func (s *ConversationStore) GetOtherMembersReadCursors(ctx context.Context, viewerID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]map[uuid.UUID]ReadCursor, error) {
	out := map[uuid.UUID]map[uuid.UUID]ReadCursor{}
	if len(convIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT conversation_id, user_id, COALESCE(last_read_message_id, '00000000-0000-0000-0000-000000000000'::uuid), last_read_at
		FROM chat.read_cursors
		WHERE conversation_id = ANY($1::uuid[]) AND user_id <> $2
	`, convIDs, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rc ReadCursor
		if err := rows.Scan(&rc.ConversationID, &rc.UserID, &rc.LastReadMessageID, &rc.LastReadAt); err != nil {
			return nil, err
		}
		if out[rc.ConversationID] == nil {
			out[rc.ConversationID] = map[uuid.UUID]ReadCursor{}
		}
		out[rc.ConversationID][rc.UserID] = rc
	}
	return out, rows.Err()
}

// ErrNotActiveMember is returned by the per-member writes below when the
// user holds no active membership.
var ErrNotActiveMember = errors.New("not a conversation member")

// ClearForMe sets the member's history cut-off to `at` (never backwards) and
// zeroes their unread count; hide=true also hides the conversation from their
// lists until a newer message arrives (§1.2 / §1.3).
func (s *ConversationStore) ClearForMe(ctx context.Context, conversationID, userID uuid.UUID, at time.Time, hide bool) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE chat.conversation_members
		SET cleared_before = GREATEST(COALESCE(cleared_before, $3), $3),
		    unread_count = 0,
		    marked_unread = FALSE,
		    hidden = CASE WHEN $4 THEN TRUE ELSE hidden END
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`, conversationID, userID, at, hide)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotActiveMember
	}
	return nil
}

// GetMemberCutoff returns the member's cleared_before (nil: full history).
func (s *ConversationStore) GetMemberCutoff(ctx context.Context, conversationID, userID uuid.UUID) (*time.Time, error) {
	var cut *time.Time
	err := s.db.QueryRow(ctx, `
		SELECT cleared_before FROM chat.conversation_members
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`, conversationID, userID).Scan(&cut)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return cut, err
}

// ResetUnread zeroes the member's unread counter and clears their own
// "mark as unread" (MarkRead): reading a conversation is what ends a mark.
func (s *ConversationStore) ResetUnread(ctx context.Context, conversationID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversation_members SET unread_count = 0, marked_unread = FALSE
		WHERE conversation_id = $1 AND user_id = $2 AND (unread_count <> 0 OR marked_unread)
	`, conversationID, userID)
	return err
}

// SetMarkedUnread sets or clears the member's own "mark as unread" (fix
// pass 2, PUT .../unread). Only an active member may: ErrNotActiveMember
// otherwise. Idempotent.
func (s *ConversationStore) SetMarkedUnread(ctx context.Context, conversationID, userID uuid.UUID, unread bool) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE chat.conversation_members SET marked_unread = $3
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`, conversationID, userID, unread)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotActiveMember
	}
	return nil
}

// --- Delete for me (§2.3) ---

func (s *ConversationStore) HideMessageForMe(ctx context.Context, userID, conversationID, messageID, senderID uuid.UUID, msgTs time.Time) error {
	// One statement: the unread release only happens when THIS call hid the
	// message, so a retried delete-for-me never decrements twice.
	_, err := s.db.Exec(ctx, `
		WITH ins AS (
			INSERT INTO chat.hidden_messages (user_id, conversation_id, message_id)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING
			RETURNING user_id
		)
		UPDATE chat.conversation_members m
		SET unread_count = m.unread_count - 1
		WHERE EXISTS (SELECT 1 FROM ins)
		  AND m.conversation_id = $2 AND m.user_id = $1 AND m.user_id <> $4
		  AND `+unreadStillCounts("$5")+`
	`, userID, conversationID, messageID, senderID, msgTs)
	return err
}

// unreadStillCounts is true for a member row m whose unread counter still
// includes a message sent at the timestamp parameter ts: the counter is
// positive, the member is active, has not cleared past it, and has not read
// up to it.
func unreadStillCounts(ts string) string {
	return strings.ReplaceAll(unreadStillCountsSQL, "$TS", ts)
}

const unreadStillCountsSQL = `m.unread_count > 0
		  AND m.left_at IS NULL
		  AND (m.cleared_before IS NULL OR m.cleared_before < $TS)
		  AND NOT EXISTS (
			SELECT 1 FROM chat.read_cursors rc
			WHERE rc.conversation_id = m.conversation_id AND rc.user_id = m.user_id
			  AND rc.last_read_at >= $TS
		  )`

// ReleaseUnreadForUnsent takes an unsent message back out of every other
// member's unread counter, except members who had already hidden it for
// themselves (their counter released it then).
func (s *ConversationStore) ReleaseUnreadForUnsent(ctx context.Context, conversationID, messageID, senderID uuid.UUID, msgTs time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversation_members m
		SET unread_count = m.unread_count - 1
		WHERE m.conversation_id = $1 AND m.user_id <> $3
		  AND NOT EXISTS (
			SELECT 1 FROM chat.hidden_messages h
			WHERE h.user_id = m.user_id AND h.conversation_id = $1 AND h.message_id = $2
		  )
		  AND `+unreadStillCounts("$4")+`
	`, conversationID, messageID, senderID, msgTs)
	return err
}

// HiddenMessageIDs returns which of ids the user hid in the conversation.
func (s *ConversationStore) HiddenMessageIDs(ctx context.Context, userID, conversationID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT message_id FROM chat.hidden_messages
		WHERE user_id = $1 AND conversation_id = $2 AND message_id = ANY($3::uuid[])
	`, userID, conversationID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// --- Message location ---

// LookupMessageTS finds a message's partition key from its durable delivery
// intent (message_id is unique there). found=false for a message that never
// went through the intent path (pre-2026-09 rows) or belongs elsewhere.
func (s *ConversationStore) LookupMessageTS(ctx context.Context, conversationID, messageID uuid.UUID) (string, time.Time, uuid.UUID, bool, error) {
	var bucket string
	var ts time.Time
	var sender uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT bucket, message_ts, sender_id FROM chat.message_delivery_intents
		WHERE message_id = $1 AND conversation_id = $2
	`, messageID, conversationID).Scan(&bucket, &ts, &sender)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, uuid.Nil, false, nil
	}
	if err != nil {
		return "", time.Time{}, uuid.Nil, false, err
	}
	return bucket, ts, sender, true, nil
}

// --- Delivery-side parity writes (§1, §1.4, §3) ---

// DeliveryParity is what the durable delivery completion records beyond the
// message itself.
type DeliveryParity struct {
	IdempotencyKey string
	ConversationID uuid.UUID
	MessageID      uuid.UUID
	SenderID       uuid.UUID
	MemberIDs      []uuid.UUID
	MessageType    string
	MessageTS      time.Time
	PostID         *uuid.UUID
	MediaID        *uuid.UUID
	MediaKind      string
}

// ApplyDeliveryParity runs in ONE transaction and is replay-safe:
//
//   - last_message_type follows the same newest-wins guard as the preview;
//   - unread_count is incremented for every other member exactly once per
//     message (guarded by message_delivery_intents.unread_counted_at);
//   - the shared-posts / media index is an idempotent insert;
//   - a DM streak advances by calendar day (Asia/Kolkata) — replaying the
//     same message is a no-op because each side's last day only moves
//     forward.
func (s *ConversationStore) ApplyDeliveryParity(ctx context.Context, d DeliveryParity) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var convType string
	if err := tx.QueryRow(ctx, `SELECT type FROM chat.conversations WHERE id = $1`, d.ConversationID).Scan(&convType); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE chat.conversations SET last_message_type = $2
		WHERE id = $1 AND last_message_at = $3
	`, d.ConversationID, d.MessageType, d.MessageTS); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, `
		UPDATE chat.message_delivery_intents SET unread_counted_at = NOW()
		WHERE idempotency_key = $1 AND unread_counted_at IS NULL
	`, d.IdempotencyKey)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if _, err := tx.Exec(ctx, `
			UPDATE chat.conversation_members SET unread_count = unread_count + 1
			WHERE conversation_id = $1 AND user_id <> $2 AND left_at IS NULL AND user_id = ANY($3::uuid[])
		`, d.ConversationID, d.SenderID, d.MemberIDs); err != nil {
			return err
		}
	}

	switch {
	case d.MessageType == "post" && d.PostID != nil:
		if _, err := tx.Exec(ctx, `
			INSERT INTO chat.conversation_shared_items (conversation_id, message_id, kind, post_id, sender_id, sent_at)
			VALUES ($1, $2, 'post', $3, $4, $5) ON CONFLICT DO NOTHING
		`, d.ConversationID, d.MessageID, *d.PostID, d.SenderID, d.MessageTS); err != nil {
			return err
		}
	case d.MessageType == "media" && d.MediaID != nil:
		var kind *string
		if d.MediaKind != "" {
			kind = &d.MediaKind
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO chat.conversation_shared_items (conversation_id, message_id, kind, media_id, media_kind, sender_id, sent_at)
			VALUES ($1, $2, 'media', $3, $4, $5, $6) ON CONFLICT DO NOTHING
		`, d.ConversationID, d.MessageID, *d.MediaID, kind, d.SenderID, d.MessageTS); err != nil {
			return err
		}
	}

	if convType == "direct" && len(d.MemberIDs) == 2 {
		if err := advanceStreakTx(ctx, tx, d.ConversationID, d.MemberIDs[0], d.MemberIDs[1], d.SenderID, d.MessageTS); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// StreakZone is the calendar the streak counts days in (CONTRACT §3).
var StreakZone = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return time.FixedZone("IST", 5*3600+1800)
	}
	return loc
}()

// StreakDay is the Asia/Kolkata calendar day of t, as UTC midnight (the way
// a DATE column scans).
func StreakDay(t time.Time) time.Time {
	y, m, d := t.In(StreakZone).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// StreakState is one chat.dm_streaks row.
type StreakState struct {
	ConversationID uuid.UUID
	UserA, UserB   uuid.UUID
	Days           int
	LastDay        *time.Time
	ALastDay       *time.Time
	BLastDay       *time.Time
}

func laterDay(cur *time.Time, day time.Time) *time.Time {
	if cur == nil || day.After(*cur) {
		d := day
		return &d
	}
	return cur
}

// AdvanceStreak applies one message from sender on `day` (a StreakDay).
// A day counts when both sides sent at least one message that day; the run
// continues when the previous counted day was the day before.
func AdvanceStreak(st StreakState, sender uuid.UUID, day time.Time) StreakState {
	switch sender {
	case st.UserA:
		st.ALastDay = laterDay(st.ALastDay, day)
	case st.UserB:
		st.BLastDay = laterDay(st.BLastDay, day)
	default:
		return st
	}
	if st.ALastDay == nil || st.BLastDay == nil || !st.ALastDay.Equal(day) || !st.BLastDay.Equal(day) {
		return st
	}
	if st.LastDay != nil && !day.After(*st.LastDay) {
		return st // already counted (or an out-of-order replay)
	}
	if st.LastDay != nil && st.LastDay.AddDate(0, 0, 1).Equal(day) {
		st.Days++
	} else {
		st.Days = 1
	}
	d := day
	st.LastDay = &d
	return st
}

func advanceStreakTx(ctx context.Context, tx pgx.Tx, conversationID, m1, m2, sender uuid.UUID, ts time.Time) error {
	a, b := normalizePair(m1, m2)
	if _, err := tx.Exec(ctx, `
		INSERT INTO chat.dm_streaks (conversation_id, user_a, user_b) VALUES ($1, $2, $3)
		ON CONFLICT (conversation_id) DO NOTHING
	`, conversationID, a, b); err != nil {
		return err
	}
	st := StreakState{ConversationID: conversationID}
	if err := tx.QueryRow(ctx, `
		SELECT user_a, user_b, days, last_day, a_last_day, b_last_day
		FROM chat.dm_streaks WHERE conversation_id = $1 FOR UPDATE
	`, conversationID).Scan(&st.UserA, &st.UserB, &st.Days, &st.LastDay, &st.ALastDay, &st.BLastDay); err != nil {
		return err
	}
	next := AdvanceStreak(st, sender, StreakDay(ts))
	_, err := tx.Exec(ctx, `
		UPDATE chat.dm_streaks SET days = $2, last_day = $3, a_last_day = $4, b_last_day = $5, updated_at = NOW()
		WHERE conversation_id = $1
	`, conversationID, next.Days, next.LastDay, next.ALastDay, next.BLastDay)
	return err
}

// GetStreaks loads the streak rows for a page of conversations.
func (s *ConversationStore) GetStreaks(ctx context.Context, convIDs []uuid.UUID) (map[uuid.UUID]StreakState, error) {
	out := map[uuid.UUID]StreakState{}
	if len(convIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT conversation_id, user_a, user_b, days, last_day, a_last_day, b_last_day
		FROM chat.dm_streaks WHERE conversation_id = ANY($1::uuid[])
	`, convIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st StreakState
		if err := rows.Scan(&st.ConversationID, &st.UserA, &st.UserB, &st.Days, &st.LastDay, &st.ALastDay, &st.BLastDay); err != nil {
			return nil, err
		}
		out[st.ConversationID] = st
	}
	return out, rows.Err()
}

// RemoveSharedItem drops an unsent message from the shared tabs.
func (s *ConversationStore) RemoveSharedItem(ctx context.Context, conversationID, messageID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM chat.conversation_shared_items WHERE conversation_id = $1 AND message_id = $2`,
		conversationID, messageID)
	return err
}

// SharedItem is one row of the shared posts / media tabs (§1.4).
type SharedItem struct {
	MessageID uuid.UUID
	Kind      string
	PostID    *uuid.UUID
	MediaID   *uuid.UUID
	MediaKind *string
	SenderID  uuid.UUID
	SentAt    time.Time
}

// ListSharedItems pages a conversation's shared posts or media, newest
// first, after the viewer's cut-off and without messages they hid.
func (s *ConversationStore) ListSharedItems(ctx context.Context, conversationID, viewerID uuid.UUID, kind string, cutoff *time.Time, beforeTs *time.Time, beforeID *uuid.UUID, limit int) ([]SharedItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	args := []any{conversationID, kind, viewerID, cutoff, limit}
	q := `
		SELECT i.message_id, i.kind, i.post_id, i.media_id, i.media_kind, i.sender_id, i.sent_at
		FROM chat.conversation_shared_items i
		WHERE i.conversation_id = $1 AND i.kind = $2
		  AND ($4::timestamptz IS NULL OR i.sent_at > $4)
		  AND NOT EXISTS (SELECT 1 FROM chat.hidden_messages h
		                  WHERE h.user_id = $3 AND h.conversation_id = i.conversation_id AND h.message_id = i.message_id)`
	if beforeTs != nil && beforeID != nil {
		args = append(args, *beforeTs, *beforeID)
		q += ` AND (i.sent_at, i.message_id) < ($6, $7)`
	}
	q += ` ORDER BY i.sent_at DESC, i.message_id DESC LIMIT $5`
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SharedItem
	for rows.Next() {
		var it SharedItem
		if err := rows.Scan(&it.MessageID, &it.Kind, &it.PostID, &it.MediaID, &it.MediaKind, &it.SenderID, &it.SentAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// --- Notes (§5) ---

// Note is one 24-hour status line.
type Note struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Text      string    `json:"text"`
	Audience  string    `json:"audience"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ReplaceNote deletes the author's notes and inserts n in one transaction.
func (s *ConversationStore) ReplaceNote(ctx context.Context, n Note) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM chat.notes WHERE user_id = $1`, n.UserID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO chat.notes (id, user_id, text, audience, created_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6)
	`, n.ID, n.UserID, n.Text, n.Audience, n.CreatedAt, n.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ConversationStore) DeleteNotes(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM chat.notes WHERE user_id = $1`, userID)
	return err
}

func scanNotes(rows pgx.Rows) ([]Note, error) {
	defer rows.Close()
	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.UserID, &n.Text, &n.Audience, &n.CreatedAt, &n.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetNote returns a live (unexpired) note by id.
func (s *ConversationStore) GetNote(ctx context.Context, id uuid.UUID, now time.Time) (*Note, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, text, audience, created_at, expires_at FROM chat.notes WHERE id = $1 AND expires_at > $2
	`, id, now)
	if err != nil {
		return nil, err
	}
	notes, err := scanNotes(rows)
	if err != nil || len(notes) == 0 {
		return nil, err
	}
	return &notes[0], nil
}

// ListLiveNotes returns the live notes of the given authors, newest first.
func (s *ConversationStore) ListLiveNotes(ctx context.Context, userIDs []uuid.UUID, now time.Time, limit int) ([]Note, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, text, audience, created_at, expires_at FROM chat.notes
		WHERE user_id = ANY($1::uuid[]) AND expires_at > $2
		ORDER BY created_at DESC LIMIT $3
	`, userIDs, now, limit)
	if err != nil {
		return nil, err
	}
	return scanNotes(rows)
}

// DirectPeerIDs lists the people the user shares an active direct
// conversation with (both memberships active).
func (s *ConversationStore) DirectPeerIDs(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT o.user_id
		FROM chat.conversation_members m
		JOIN chat.conversations c ON c.id = m.conversation_id AND c.type = 'direct'
		JOIN chat.conversation_members o ON o.conversation_id = m.conversation_id AND o.user_id <> m.user_id AND o.left_at IS NULL
		WHERE m.user_id = $1 AND m.left_at IS NULL
		ORDER BY COALESCE(c.last_message_at, c.created_at) DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// --- Stickers (§2.7) ---

type Sticker struct {
	ID       uuid.UUID `json:"id"`
	PackID   uuid.UUID `json:"pack_id"`
	MediaID  uuid.UUID `json:"media_id"`
	URL      string    `json:"url"`
	Width    int       `json:"width"`
	Height   int       `json:"height"`
	SortOrd  int       `json:"sort_order"`
	PackName string    `json:"-"`
}

type StickerPack struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sort_order"`
	Stickers  []Sticker `json:"stickers"`
}

// ListStickerCatalogue returns active packs with their active stickers;
// packs without stickers are left out.
func (s *ConversationStore) ListStickerCatalogue(ctx context.Context) ([]StickerPack, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.name, p.sort_order, st.id, st.media_id, st.url, st.width, st.height, st.sort_order
		FROM chat.sticker_packs p
		JOIN chat.stickers st ON st.pack_id = p.id AND st.active
		WHERE p.active
		ORDER BY p.sort_order, p.created_at, p.id, st.sort_order, st.created_at, st.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var packs []StickerPack
	for rows.Next() {
		var p StickerPack
		var st Sticker
		if err := rows.Scan(&p.ID, &p.Name, &p.SortOrder, &st.ID, &st.MediaID, &st.URL, &st.Width, &st.Height, &st.SortOrd); err != nil {
			return nil, err
		}
		st.PackID = p.ID
		if len(packs) == 0 || packs[len(packs)-1].ID != p.ID {
			p.Stickers = []Sticker{}
			packs = append(packs, p)
		}
		packs[len(packs)-1].Stickers = append(packs[len(packs)-1].Stickers, st)
	}
	return packs, rows.Err()
}

// GetActiveSticker returns an active sticker in an active pack, or nil.
func (s *ConversationStore) GetActiveSticker(ctx context.Context, id uuid.UUID) (*Sticker, error) {
	var st Sticker
	err := s.db.QueryRow(ctx, `
		SELECT st.id, st.pack_id, st.media_id, st.url, st.width, st.height, st.sort_order
		FROM chat.stickers st JOIN chat.sticker_packs p ON p.id = st.pack_id
		WHERE st.id = $1 AND st.active AND p.active
	`, id).Scan(&st.ID, &st.PackID, &st.MediaID, &st.URL, &st.Width, &st.Height, &st.SortOrd)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// UpsertStickerPack creates or renames a pack.
func (s *ConversationStore) UpsertStickerPack(ctx context.Context, id uuid.UUID, name string, sortOrder int, active bool) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.sticker_packs (id, name, sort_order, active) VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, sort_order = EXCLUDED.sort_order, active = EXCLUDED.active
	`, id, name, sortOrder, active)
	return err
}

// UpsertSticker adds or updates a sticker in a pack.
func (s *ConversationStore) UpsertSticker(ctx context.Context, st Sticker, active bool) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.stickers (id, pack_id, media_id, url, width, height, sort_order, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET pack_id = EXCLUDED.pack_id, media_id = EXCLUDED.media_id, url = EXCLUDED.url,
		    width = EXCLUDED.width, height = EXCLUDED.height, sort_order = EXCLUDED.sort_order, active = EXCLUDED.active
	`, st.ID, st.PackID, st.MediaID, st.URL, st.Width, st.Height, st.SortOrd, active)
	return err
}

// --- Search (§6) ---

// SearchMyConversations returns the caller's conversations whose group title
// or DM peer name / username matches q (case-insensitive), excluding
// incoming requests and conversations hidden for the caller.
func (s *ConversationStore) SearchMyConversations(ctx context.Context, userID uuid.UUID, q string, limit int) ([]uuid.UUID, error) {
	pattern := "%" + escapeLike(strings.ToLower(q)) + "%"
	rows, err := s.db.Query(ctx, `
		SELECT c.id
		FROM chat.conversations c
		JOIN chat.conversation_members m ON m.conversation_id = c.id AND m.user_id = $1 AND m.left_at IS NULL
		LEFT JOIN chat.message_requests mr ON mr.conversation_id = c.id
		WHERE `+visibleToMember+` AND NOT `+incomingRequest+`
		  AND (
		    (c.type = 'group' AND lower(COALESCE(c.title, '')) LIKE $2 ESCAPE '\')
		    OR (c.type = 'direct' AND EXISTS (
		        SELECT 1 FROM chat.conversation_members o
		        JOIN chat.user_profiles p ON p.user_id = o.user_id
		        WHERE o.conversation_id = c.id AND o.user_id <> $1
		          AND (lower(p.display_name) LIKE $2 ESCAPE '\' OR lower(p.username) LIKE $2 ESCAPE '\')))
		  )
		ORDER BY COALESCE(c.last_message_at, c.created_at) DESC, c.id DESC
		LIMIT $3
	`, userID, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// MemberSearchState is what search needs to re-check a hit.
type MemberSearchState struct {
	ClearedBefore *time.Time
	IsRequest     bool
}

// GetMemberSearchStates returns, for the conversations the user is an
// ACTIVE member of, their cut-off and whether the conversation is a request.
// Conversations missing from the map are not the user's.
func (s *ConversationStore) GetMemberSearchStates(ctx context.Context, userID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]MemberSearchState, error) {
	out := map[uuid.UUID]MemberSearchState{}
	if len(convIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT m.conversation_id, m.cleared_before, c.is_request
		FROM chat.conversation_members m JOIN chat.conversations c ON c.id = m.conversation_id
		WHERE m.user_id = $1 AND m.left_at IS NULL AND m.conversation_id = ANY($2::uuid[])
	`, userID, convIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var st MemberSearchState
		if err := rows.Scan(&id, &st.ClearedBefore, &st.IsRequest); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// HiddenAmong returns which (conversation, message) pairs the user hid.
func (s *ConversationStore) HiddenAmong(ctx context.Context, userID uuid.UUID, messageIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(messageIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT message_id FROM chat.hidden_messages WHERE user_id = $1 AND message_id = ANY($2::uuid[])
	`, userID, messageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// --- Requests (§7) ---

// RequestEnvelope is a pending request row with its conversation.
type RequestEnvelope struct {
	Conversation Conversation
	SenderID     uuid.UUID
	ReceiverID   uuid.UUID
	Preview      string
	Status       string
}

// ListRequestEnvelopes returns the requests the legacy /requests list shows
// (every request conversation the user is a member of, not declined), with
// the envelope, newest first, bounded.
func (s *ConversationStore) ListRequestEnvelopes(ctx context.Context, userID uuid.UUID, limit int) ([]RequestEnvelope, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.type, c.title, c.created_by, c.is_request, c.created_at, c.updated_at,
		       COALESCE(mr.sender_id, '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE(mr.receiver_id, '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE(mr.preview, ''), COALESCE(mr.status, '')
		FROM chat.conversations c
		JOIN chat.conversation_members m ON m.conversation_id = c.id
		LEFT JOIN chat.message_requests mr ON mr.conversation_id = c.id
		WHERE m.user_id = $1 AND c.is_request = TRUE AND c.request_declined_at IS NULL
		ORDER BY c.created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestEnvelope
	for rows.Next() {
		var e RequestEnvelope
		c := &e.Conversation
		if err := rows.Scan(&c.ID, &c.Type, &c.Title, &c.CreatedBy, &c.IsRequest, &c.CreatedAt, &c.UpdatedAt,
			&e.SenderID, &e.ReceiverID, &e.Preview, &e.Status); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- Profile extras (§1 peer.username, §7 account age) ---

type ProfileExtras struct {
	Username         string
	AccountCreatedAt *time.Time
}

func (s *ConversationStore) GetProfileExtras(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]ProfileExtras, error) {
	out := map[uuid.UUID]ProfileExtras{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id, username, account_created_at FROM chat.user_profiles WHERE user_id = ANY($1::uuid[])
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var pe ProfileExtras
		if err := rows.Scan(&id, &pe.Username, &pe.AccountCreatedAt); err != nil {
			return nil, err
		}
		out[id] = pe
	}
	return out, rows.Err()
}

// SetProfileExtras caches username + account creation time (the row must
// exist: it is written right after UpsertUserProfile).
func (s *ConversationStore) SetProfileExtras(ctx context.Context, userID uuid.UUID, username string, createdAt *time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.user_profiles
		SET username = $2, account_created_at = COALESCE($3, account_created_at)
		WHERE user_id = $1
	`, userID, username, createdAt)
	return err
}

// --- Read summary (§2.9) ---

// SortUUIDs orders ids by their string form (stable output).
func SortUUIDs(ids []uuid.UUID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
}

// MarshalExtra is a tiny helper the delivery intent uses for its JSON
// column: nil and empty payloads store NULL.
func MarshalExtra(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if string(raw) == "{}" || string(raw) == "null" {
		return nil, nil
	}
	return raw, nil
}

// AlertTones is the per-conversation alert_tone enum (CONTRACT §1.1).
var AlertTones = map[string]bool{"default": true, "chime": true, "pop": true, "bell": true, "silent": true}

func alertToneOrDefault(v string) string {
	if AlertTones[v] {
		return v
	}
	return "default"
}

// ListAlertTones returns the members of a conversation with a non-default
// alert tone (carried on MessageCreated for the push channel, §8).
func (s *ConversationStore) ListAlertTones(ctx context.Context, conversationID uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	rows, err := s.db.Query(ctx, `
		SELECT user_id, alert_tone FROM chat.conversation_settings
		WHERE conversation_id = $1 AND alert_tone <> 'default'
	`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var tone string
		if err := rows.Scan(&id, &tone); err != nil {
			return nil, err
		}
		out[id] = tone
	}
	return out, rows.Err()
}
