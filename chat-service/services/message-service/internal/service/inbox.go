package service

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-shared/presence"
	"github.com/google/uuid"
)

// Chat parity §1 (lane B1): inbox lanes, unread counts, pins first, mute
// expiry, streaks, the DM peer, last_message, hide / clear for me.

// StreakInfo is a DM streak (CONTRACT §3). Present only when days >= 3.
type StreakInfo struct {
	Days      int       `json:"days"`
	AtRisk    bool      `json:"at_risk"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PeerInfo is the other person in a DM. Online state is null when their
// privacy settings do not disclose it to the viewer.
type PeerInfo struct {
	UserID       uuid.UUID  `json:"user_id"`
	DisplayName  string     `json:"display_name"`
	Username     string     `json:"username"`
	AvatarURL    string     `json:"avatar_url"`
	IsOnline     *bool      `json:"is_online"`
	LastActiveAt *time.Time `json:"last_active_at"`
}

// LastMessageInfo is the inbox row's newest visible message.
type LastMessageInfo struct {
	Type      string     `json:"type"`
	Preview   string     `json:"preview"`
	SenderID  *uuid.UUID `json:"sender_id"`
	At        time.Time  `json:"at"`
	OwnStatus *string    `json:"own_status"`
}

// ErrInvalidLane is an unknown ?lane= value.
var ErrInvalidLane = errors.New("lane must be primary, requests or archived")

// pinnedInboxCap bounds the pinned block on the first page.
const pinnedInboxCap = 100

// streakMinDays is the display threshold (CONTRACT §3).
const streakMinDays = 3

// streakAtRiskHour: after 18:00 IST a streak that has not been kept today
// is at risk.
const streakAtRiskHour = 18

// StreakFor turns a stored streak into what the inbox shows at `now`.
func StreakFor(st postgres.StreakState, now time.Time) *StreakInfo {
	if st.LastDay == nil || st.Days < streakMinDays {
		return nil
	}
	today := postgres.StreakDay(now)
	yesterday := today.AddDate(0, 0, -1)
	last := *st.LastDay
	if !last.Equal(today) && !last.Equal(yesterday) {
		return nil
	}
	expires := time.Date(last.Year(), last.Month(), last.Day()+2, 0, 0, 0, 0, postgres.StreakZone).UTC()
	return &StreakInfo{
		Days:      st.Days,
		AtRisk:    last.Equal(yesterday) && now.In(postgres.StreakZone).Hour() >= streakAtRiskHour,
		ExpiresAt: expires,
	}
}

// ListInbox is GET /v1/chat/conversations?lane=. The first page (no cursor)
// starts with the viewer's pinned conversations; the cursor pages the rest
// by last activity.
func (s *Service) ListInbox(ctx context.Context, userID uuid.UUID, lane string, limit int, cursor *ConversationCursor) ([]ConversationResponse, *ConversationCursor, error) {
	if lane == "" {
		lane = postgres.LanePrimary
	}
	if !postgres.ValidLane(lane) {
		return nil, nil, ErrInvalidLane
	}
	ps, ok := s.parity()
	if !ok {
		return s.listConversationsLegacy(ctx, userID, limit, cursor)
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var convs []postgres.Conversation
	var cursorTs *time.Time
	var cursorID *uuid.UUID
	if cursor == nil {
		pinned, err := ps.ListInboxConversations(ctx, userID, lane, true, pinnedInboxCap, nil, nil)
		if err != nil {
			return nil, nil, err
		}
		convs = append(convs, pinned...)
	} else {
		cursorTs, cursorID = &cursor.UpdatedAt, &cursor.ID
	}
	rest, err := ps.ListInboxConversations(ctx, userID, lane, false, limit, cursorTs, cursorID)
	if err != nil {
		return nil, nil, err
	}
	convs = append(convs, rest...)

	out := make([]ConversationResponse, 0, len(convs))
	for _, c := range convs {
		members, err := s.convStore.GetMembers(ctx, c.ID)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, ConversationResponse{
			ID:                 c.ID,
			Type:               c.Type,
			Title:              c.Title,
			CreatedBy:          c.CreatedBy,
			IsRequest:          c.IsRequest,
			Members:            s.enrichMembers(ctx, members),
			CreatedAt:          c.CreatedAt,
			UpdatedAt:          c.UpdatedAt,
			AvatarMediaID:      c.AvatarMediaID,
			Description:        c.Description,
			LastMessageAt:      c.LastMessageAt,
			LastMessagePreview: c.LastMessagePreview,
			LastMessageSender:  c.LastMessageSender,
			SourceApp:          datingSourceApp(c.SourceApp),
			MatchID:            datingMatchID(c.SourceApp, c.MatchID),
		})
	}
	s.resolveGroupAvatarURLs(ctx, userID, out)
	s.decorateRows(ctx, userID, out)

	var next *ConversationCursor
	if len(rest) == limit {
		last := rest[len(rest)-1]
		next = &ConversationCursor{UpdatedAt: last.SortAt, ID: last.ID}
	}
	return out, next, nil
}

// ListConversations is the legacy entry point: the primary lane.
func (s *Service) ListConversations(ctx context.Context, userID uuid.UUID, limit int, cursor *ConversationCursor) ([]ConversationResponse, *ConversationCursor, error) {
	return s.ListInbox(ctx, userID, postgres.LanePrimary, limit, cursor)
}

// decorateRows fills the viewer-scoped parity fields on a page of rows with
// a fixed number of queries (never per row, apart from the cached policy
// and disclosure decisions the existing receipt path already makes).
func (s *Service) decorateRows(ctx context.Context, viewerID uuid.UUID, rows []ConversationResponse) {
	ps, ok := s.parity()
	if !ok || len(rows) == 0 {
		return
	}
	now := s.clock()
	ids := make([]uuid.UUID, len(rows))
	var directIDs []uuid.UUID
	for i, r := range rows {
		ids[i] = r.ID
		if r.Type == "direct" {
			directIDs = append(directIDs, r.ID)
		}
	}
	meta, err := ps.GetInboxMeta(ctx, viewerID, ids)
	if err != nil {
		s.log.Warn("inbox meta batch failed — parity fields degraded", "err", err)
		meta = map[uuid.UUID]postgres.InboxMeta{}
	}
	own, err := s.groupStore().GetReadCursors(ctx, viewerID, ids)
	if err != nil {
		own = nil
	}
	others, err := ps.GetOtherMembersReadCursors(ctx, viewerID, directIDs)
	if err != nil {
		others = nil
	}
	streaks, err := ps.GetStreaks(ctx, directIDs)
	if err != nil {
		streaks = nil
	}
	peers := s.buildPeers(ctx, viewerID, rows)

	for i := range rows {
		r := &rows[i]
		m, hasMeta := meta[r.ID]
		if !hasMeta {
			m = postgres.InboxMeta{AlertTone: "default"}
		}
		r.IsPinned = m.IsPinned
		r.IsMuted = m.IsMuted && (m.MuteUntil == nil || m.MuteUntil.After(now))
		r.MuteCalls = m.MuteCalls
		r.AlertTone = m.AlertTone
		if r.AlertTone == "" {
			r.AlertTone = "default"
		}

		// The cut-off hides history: a last message at or before it is not
		// shown, and is not unread.
		visibleLast := r.LastMessageAt != nil && (m.ClearedBefore == nil || r.LastMessageAt.After(*m.ClearedBefore))
		if r.LastMessageAt != nil && !visibleLast {
			r.LastMessagePreview = ""
			r.LastMessageSender = nil
		}
		hasUnread := false
		if visibleLast && (r.LastMessageSender == nil || *r.LastMessageSender != viewerID) {
			rc, ok := own[r.ID]
			hasUnread = !ok || rc.LastReadAt.Before(*r.LastMessageAt)
		}
		r.HasUnread = hasUnread
		// Reconciled counter: a read cursor newer than the last message
		// always means 0, and an unread row always shows at least 1.
		switch {
		case !hasUnread:
			r.UnreadCount = 0
		case m.UnreadCount < 1:
			r.UnreadCount = 1
		default:
			r.UnreadCount = m.UnreadCount
		}

		if visibleLast {
			typ := m.LastMessageType
			if typ == "" {
				typ = MessageTypeText
			}
			lm := &LastMessageInfo{Type: typ, Preview: r.LastMessagePreview, SenderID: r.LastMessageSender, At: *r.LastMessageAt}
			if r.LastMessageSender != nil && *r.LastMessageSender == viewerID {
				status := "sent"
				if p, ok := peers[r.ID]; ok && r.Type == "direct" {
					if rc, ok := others[r.ID][p.UserID]; ok && !rc.LastReadAt.Before(*r.LastMessageAt) &&
						s.receiptDisclosed(ctx, r.ID, viewerID, p.UserID) {
						status = "read"
					}
				}
				lm.OwnStatus = &status
			}
			r.LastMessage = lm
		}

		if r.Type == "direct" {
			if p, ok := peers[r.ID]; ok {
				peer := p
				r.Peer = &peer
			}
			if st, ok := streaks[r.ID]; ok {
				r.Streak = StreakFor(st, now)
			}
		}
	}
}

// receiptDisclosed applies the reader's own read-receipt privacy (the same
// rule attachReadCursors and the live frame use) for viewer.
func (s *Service) receiptDisclosed(ctx context.Context, convID, viewerID, readerID uuid.UUID) bool {
	if meta, err := s.convStore.GetConversationMeta(ctx, convID); err == nil && meta != nil && meta.ReceiptsGated &&
		!s.datingReceiptsAllowed(ctx, convID, viewerID) {
		return false
	}
	policy := s.GetChatPolicy(ctx, readerID)
	if !policy.Known || policy.ChatPaused || policy.ReadReceiptsVisibility == "no_one" {
		return false
	}
	if policy.ReadReceiptsVisibility == "connections_only" {
		return s.discloseReceiptTo(ctx, viewerID, readerID)
	}
	return true
}

// onlineStates resolves is_online / last_active_at for ids as the viewer
// sees them: nil when the target's privacy does not disclose it.
func (s *Service) onlineStates(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*bool, map[uuid.UUID]*time.Time) {
	online := map[uuid.UUID]*bool{}
	lastActive := map[uuid.UUID]*time.Time{}
	if len(ids) == 0 || s.rdb == nil {
		return online, lastActive
	}
	keys := make([]string, len(ids))
	strIDs := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = "presence:" + id.String()
		strIDs[i] = id.String()
	}
	results, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return online, lastActive
	}
	hidden := map[uuid.UUID]bool{}
	if s.hiddenUsers != nil {
		if m, err := s.hiddenUsers.HiddenUsers(ctx, ids); err == nil {
			hidden = m
		} else {
			return online, lastActive
		}
	}
	// Last-active timestamps (shared/presence, written by ws-gateway). The
	// helper applies no privacy: each target is gated below.
	seen, _ := presence.LastActiveAt(ctx, s.rdb, strIDs)
	for i, id := range ids {
		if hidden[id] || !s.discloseOnlineTo(ctx, viewerID, id) {
			continue
		}
		on := results[i] != nil
		online[id] = &on
		if t, ok := seen[id.String()]; ok && s.discloseLastSeenTo(ctx, viewerID, id) {
			tt := t.UTC()
			lastActive[id] = &tt
		}
	}
	return online, lastActive
}

// buildPeers resolves the DM peer for every direct row in one pass.
func (s *Service) buildPeers(ctx context.Context, viewerID uuid.UUID, rows []ConversationResponse) map[uuid.UUID]PeerInfo {
	out := map[uuid.UUID]PeerInfo{}
	peerOf := map[uuid.UUID]MemberWithProfile{}
	var peerIDs []uuid.UUID
	var avatarIDs []string
	for _, r := range rows {
		if r.Type != "direct" {
			continue
		}
		for _, m := range r.Members {
			if m.UserID != viewerID {
				peerOf[r.ID] = m
				peerIDs = append(peerIDs, m.UserID)
				if m.AvatarMediaID != nil {
					avatarIDs = append(avatarIDs, m.AvatarMediaID.String())
				}
				break
			}
		}
	}
	if len(peerIDs) == 0 {
		return out
	}
	extras := s.profileExtras(ctx, peerIDs)
	var urls map[string]string
	if len(avatarIDs) > 0 && s.mediaServiceURL != "" && s.httpClient != nil {
		urls = s.fetchMediaURLs(ctx, viewerID, avatarIDs)
	}
	online, lastActive := s.onlineStates(ctx, viewerID, peerIDs)
	for convID, m := range peerOf {
		p := PeerInfo{UserID: m.UserID, DisplayName: m.DisplayName, Username: extras[m.UserID].Username,
			IsOnline: online[m.UserID], LastActiveAt: lastActive[m.UserID]}
		if m.AvatarMediaID != nil {
			p.AvatarURL = urls[m.AvatarMediaID.String()]
		}
		out[convID] = p
	}
	return out
}

// profileExtras returns cached usernames / account ages, refreshing the
// rows never filled from user-service (account_created_at still NULL).
func (s *Service) profileExtras(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]postgres.ProfileExtras {
	ps, ok := s.parity()
	if !ok {
		return map[uuid.UUID]postgres.ProfileExtras{}
	}
	got, err := ps.GetProfileExtras(ctx, ids)
	if err != nil {
		return map[uuid.UUID]postgres.ProfileExtras{}
	}
	if s.userServiceURL == "" {
		return got
	}
	const refreshCap = 20 // bound the user-service fan-out per page
	refreshed := 0
	for _, id := range ids {
		if pe, ok := got[id]; ok && pe.AccountCreatedAt != nil {
			continue
		}
		if refreshed >= refreshCap {
			break
		}
		refreshed++
		profile, extra, ok := s.fetchProfileWithExtras(ctx, id)
		if !ok {
			continue
		}
		if err := s.convStore.UpsertUserProfile(ctx, id, profile.DisplayName, profile.AvatarMediaID); err != nil {
			continue
		}
		if err := ps.SetProfileExtras(ctx, id, extra.Username, extra.AccountCreatedAt); err == nil {
			got[id] = extra
		}
	}
	return got
}

// --- hide / clear / unread (§1.2, §1.3) ---

// DeleteConversationForMe hides the conversation from the caller's lists and
// clears their history up to now. Groups: hide only — membership stays.
func (s *Service) DeleteConversationForMe(ctx context.Context, userID, convID uuid.UUID) error {
	return s.clearForMe(ctx, userID, convID, true)
}

// ClearConversationForMe clears the caller's history up to now.
func (s *Service) ClearConversationForMe(ctx context.Context, userID, convID uuid.UUID) error {
	return s.clearForMe(ctx, userID, convID, false)
}

func (s *Service) clearForMe(ctx context.Context, userID, convID uuid.UUID, hide bool) error {
	ps, ok := s.parity()
	if !ok {
		return errors.New("not supported")
	}
	if err := ps.ClearForMe(ctx, convID, userID, s.clock().UTC(), hide); err != nil {
		if errors.Is(err, postgres.ErrNotActiveMember) {
			return ErrNotMember
		}
		return err
	}
	return nil
}

// ErrNotMember is the parity routes' membership refusal (404 on the wire,
// so a route never confirms a conversation exists).
var ErrNotMember = errors.New("not a conversation member")

// memberCutoff returns the caller's history cut-off (nil: none).
// A lookup error propagates: callers fail closed rather than show history
// the member cleared.
func (s *Service) memberCutoff(ctx context.Context, convID, userID uuid.UUID) (*time.Time, error) {
	ps, ok := s.parity()
	if !ok {
		return nil, nil
	}
	return ps.GetMemberCutoff(ctx, convID, userID)
}

// discloseLastSeenTo mirrors discloseOnlineTo for the last-active time
// (graph action see_last_seen), cached 30 s per pair, failing closed.
func (s *Service) discloseLastSeenTo(ctx context.Context, viewerID, targetID uuid.UUID) bool {
	if viewerID == targetID {
		return true
	}
	if s.rdb == nil {
		return s.checkVisibilityPermission(ctx, viewerID, targetID, "see_last_seen")
	}
	cacheKey := "lastseenvis:" + viewerID.String() + ":" + targetID.String()
	if cached, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil {
		return cached == "1"
	}
	allowed := s.checkVisibilityPermission(ctx, viewerID, targetID, "see_last_seen")
	value := "0"
	if allowed {
		value = "1"
	}
	s.rdb.Set(ctx, cacheKey, value, 30*time.Second)
	return allowed
}
