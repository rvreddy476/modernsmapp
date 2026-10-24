package chatdb

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Hidden chats (message-service migration 018, 24 Oct 2026): a member can
// hide a conversation for themself (chat.conversation_members.locked). A
// call from a conversation the CALLEE hid rings as "Incoming call" with no
// name, and the call stays out of the callee's call history.

// callHiddenSQL: has $1 hidden the conversation the call came from ($2, may
// be NULL) or the pair's direct conversation with $3? Same shape as the
// mute check, so naming another source cannot bypass a hidden DM.
const callHiddenSQL = `
SELECT EXISTS (
  SELECT 1 FROM chat.conversation_members cm
  WHERE cm.user_id = $1
    AND cm.locked
    AND cm.left_at IS NULL
    AND (cm.conversation_id = $2::uuid
         OR cm.conversation_id IN (
              SELECT k.conversation_id FROM chat.direct_conversation_keys k
              WHERE (k.user_a = $1 AND k.user_b = $3) OR (k.user_a = $3 AND k.user_b = $1)))
)`

// HiddenFor reports whether userID hid the conversation of a call with
// otherID (sourceConversationID optional). FAILS CLOSED: any error other than
// the schema not being migrated yet answers true — a nameless ring is the
// safe mistake; a named one on a hidden chat is not.
func (m *MuteReader) HiddenFor(ctx context.Context, userID, otherID uuid.UUID, sourceConversationID *uuid.UUID) bool {
	if m == nil || m.pool == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var source any
	if sourceConversationID != nil {
		source = *sourceConversationID
	}
	var hidden bool
	if err := m.pool.QueryRow(ctx, callHiddenSQL, userID, source, otherID).Scan(&hidden); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "42703" || pgErr.Code == "42P01") {
			// Before migration 018 nothing can be hidden.
			return false
		}
		m.log.Warn("hidden-chat lookup failed; treating the call as hidden", "err", err)
		return true
	}
	return hidden
}
