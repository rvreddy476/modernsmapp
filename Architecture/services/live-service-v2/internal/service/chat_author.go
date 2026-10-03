package service

// Chat authors and chat text (2 Oct 2026).
//
// Every chat row — GET /chat, the POST answer, the chat.message frame and
// the pinned message — carries `author`: who wrote it, from the same
// directory the creator card uses (identity-profile, cached 60s) plus this
// service's own badges and the author's role in the stream. Only user_id and
// role are guaranteed: a profile lookup that fails leaves the name off and
// never fails the request.
//
// Chat text is any Unicode. Its length is counted in characters (code
// points), as the column's CHECK counts it, never in bytes.

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// MaxChatChars is the longest chat message, in characters (code points). A
// family emoji joined with zero-width joiners is seven of them, a flag two.
const MaxChatChars = 500

// cleanChatText trims the message and drops NUL characters, the one thing
// PostgreSQL cannot store in text (a message carrying one used to fail with
// a 500). Everything else — emoji, joiners, variation selectors, combining
// marks, any script — is kept exactly as sent.
func cleanChatText(text string) string {
	if strings.ContainsRune(text, 0) {
		text = strings.ReplaceAll(text, "\x00", "")
	}
	return strings.TrimSpace(text)
}

// withAuthors sets `author` on each message, in place (the rows are the
// caller's own copies). The role is the author's role in the stream now:
// host, moderator or viewer. Best effort throughout: a failed moderator
// read makes non-hosts viewers, a failed profile read leaves the names off.
func (s *Service) withAuthors(ctx context.Context, st *postgres.LiveStream, msgs []*postgres.ChatMessage) {
	if len(msgs) == 0 || st == nil {
		return
	}
	ids := make([]uuid.UUID, 0, len(msgs))
	needMods := false
	for _, m := range msgs {
		ids = append(ids, m.UserID)
		if m.UserID != st.CreatorUserID {
			needMods = true
		}
	}
	mods := map[uuid.UUID]bool{}
	if needMods {
		list, err := s.store.ListModerators(ctx, st.ID)
		if err != nil {
			slog.WarnContext(ctx, "live-v2: moderators not read; chat authors show as viewers", "stream_id", st.ID, "err", err)
		}
		for _, id := range list {
			mods[id] = true
		}
	}
	cards := s.userCardsWith(ctx, ids, true)
	for _, m := range msgs {
		a := &postgres.ChatAuthor{UserID: m.UserID, Badges: []string{}, Role: postgres.ChatRoleViewer}
		switch {
		case m.UserID == st.CreatorUserID:
			a.Role = postgres.ChatRoleHost
		case mods[m.UserID]:
			a.Role = postgres.ChatRoleModerator
		}
		if card := cards[m.UserID]; card != nil {
			a.Name, a.Handle, a.AvatarURL = card.Name, card.Handle, card.AvatarURL
			if len(card.Badges) > 0 {
				a.Badges = append([]string{}, card.Badges...)
			}
		}
		m.Author = a
	}
}
