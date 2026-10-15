package service

// Join notices (TikTok parity T-29, 14 Oct 2026). When a viewer's presence
// moves to present, the room hears it once as a realtime frame on
// live:stream:{id}, which the web renders as a system chat row
// ("<name> joined"):
//
//	{"type":"viewer.joined","stream_id":"<uuid>","at":"<RFC3339>",
//	 "user_id":"<uuid>",
//	 "user":{"user_id","name"?,"handle"?,"avatar_url"?,"badges":[],"role":"viewer|moderator"},
//	 "joined_at":"<RFC3339>",
//	 "payload":{...the same fields...}}
//
// `user` is exactly the `author` card a chat row carries (chat_author.go),
// from the same cached directory: user_id and role always, the rest only
// when the lookup answered. There is no HTTP read of joins; a missed frame
// is simply not shown.
//
// Who is announced, and when:
//   - only a move to present (store.ApplyPresence's joined): a repeated
//     participant_joined for a viewer already present, or a redelivered
//     webhook, announces nothing;
//   - never the host (the creator's own connection, or the stream's
//     encoder): applyWebhook never sends a host here, and the creator id is
//     refused again below;
//   - never a signed-out or unknown viewer: only a user-id identity reaches
//     presence at all (applyPresence drops anything else), so the room
//     never shows an anonymous viewer and neither does this;
//   - only while chat is open (live or reconnecting): a join notice is a
//     chat row, and an ended, failed, scheduled or still-starting stream
//     has no chat to show it in;
//   - only a viewer who passes the room gate now (MayWatch: visibility,
//     blocks either way with the host, stream ban, platform live ban). A
//     stale LiveKit token must not put a banned or blocked user's name in
//     the room; an undecidable gate announces nothing;
//   - at most once per user per stream per minute (Allow, a Redis SET NX
//     with a 60 s TTL shared by every replica), so leave/rejoin flapping
//     cannot flood the chat. The throttle is taken before the gate and the
//     profile lookup, so neither runs more than once a minute per viewer.
//
// Blocks between viewers: the chat path does not hide one viewer's messages
// from another who blocked them — the gateway relays every room frame to
// every admitted socket, and the only block rule is the one between viewer
// and host, enforced by the room gate. Join notices follow the same rule
// (the MayWatch check above) and nothing more; if chat ever filters per
// viewer, it has to happen in the ws-gateway for both frames alike.

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// viewerJoinedWindow: a viewer is announced at most once per window per
// stream.
const viewerJoinedWindow = time.Minute

func viewerJoinedKey(streamID, userID uuid.UUID) string {
	return "live:viewer_joined:" + streamID.String() + ":" + userID.String()
}

// viewerJoinedPayload is the viewer.joined frame's payload.
type viewerJoinedPayload struct {
	StreamID string               `json:"stream_id"`
	UserID   uuid.UUID            `json:"user_id"`
	User     *postgres.ChatAuthor `json:"user"`
	JoinedAt time.Time            `json:"joined_at"`
}

// announceJoin publishes viewer.joined for userID, whose presence in st just
// moved to present. Best effort: it never fails the webhook.
func (s *Service) announceJoin(ctx context.Context, st *postgres.LiveStream, userID uuid.UUID) {
	if s.rt == nil || st == nil || userID == uuid.Nil || userID == st.CreatorUserID {
		return
	}
	if !chatOpen(st.Status) {
		return
	}
	if !s.rt.Allow(ctx, viewerJoinedKey(st.ID, userID), viewerJoinedWindow) {
		return
	}
	ok, err := s.MayWatch(ctx, st.ID, userID)
	if err != nil {
		slog.WarnContext(ctx, "live-v2: room gate undecided; join not announced", "stream_id", st.ID, "err", err)
		return
	}
	if !ok {
		return
	}
	user := s.chatAuthors(ctx, st, []uuid.UUID{userID})[userID]
	s.publish(ctx, st.ID, EventViewerJoined, viewerJoinedPayload{
		StreamID: st.ID.String(),
		UserID:   userID,
		User:     user,
		JoinedAt: s.clock().UTC(),
	})
}
