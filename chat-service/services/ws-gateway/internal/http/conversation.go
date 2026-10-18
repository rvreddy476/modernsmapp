package http

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// isConversationFrame reports the per-conversation presence/typing frames
// that EnableConversationFrames admits.
func isConversationFrame(msgType string) bool {
	switch msgType {
	case "conversation.enter", "conversation.heartbeat", "conversation.leave",
		"typing.start", "typing.started", "typing.stop":
		return true
	}
	return false
}

// handleConversationFrame applies one conversation frame. Every frame names
// a conversation the connection must hold an UNEXPIRED entitled convroom
// subscription to — message-service signed that entitlement after checking
// ACTIVE membership — otherwise it is refused: a client cannot mark itself
// "viewing" or "typing" in a conversation it does not belong to. Returns
// whether the frame was honoured (for tests).
func (s *Server) handleConversationFrame(ctx context.Context, msgType string, envelope map[string]any, userID uuid.UUID, subs *roomSubscriptions, typing *typingTracker, now time.Time) bool {
	raw, _ := envelope["conversation_id"].(string)
	if raw == "" {
		raw, _ = envelope["conv_id"].(string)
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return false
	}
	convID := parsed.String()
	if !subs.activeFor(convID, now) {
		s.log.Info("conversation frame refused: not subscribed",
			"user_id", userID, "type", msgType, "conversation_id", convID)
		return false
	}

	uid := userID.String()
	switch msgType {
	case "conversation.enter":
		if err := s.pres.EnterConversation(ctx, convID, uid); err != nil {
			s.log.Warn("presence enter failed", "err", err, "user_id", userID, "conv_id", convID)
		}
	case "conversation.heartbeat":
		if err := s.pres.HeartbeatConversation(ctx, convID, uid); err != nil {
			s.log.Warn("presence heartbeat failed", "err", err, "user_id", userID, "conv_id", convID)
		}
	case "conversation.leave":
		if err := s.pres.LeaveConversation(ctx, convID, uid); err != nil {
			s.log.Warn("presence leave failed", "err", err, "user_id", userID, "conv_id", convID)
		}
		typing.Stop(convID)
	case "typing.start", "typing.started":
		if err := s.pres.SetTyping(ctx, convID, uid); err != nil {
			s.log.Warn("typing set failed", "err", err, "user_id", userID, "conv_id", convID)
		}
		typing.Start(convID)
	case "typing.stop":
		typing.Stop(convID)
	default:
		return false
	}
	return true
}

// newConnTypingTracker builds one connection's tracker, emitting into queue.
func (s *Server) newConnTypingTracker(queue *typingQueue, userID uuid.UUID) *typingTracker {
	return newTypingTracker(s.opts.typingClock, s.opts.TypingTimeout, 0, func(conversationID string, isTyping bool) {
		id, err := uuid.Parse(conversationID)
		if err != nil {
			return
		}
		if !queue.push(typingEvent{conversationID: id, isTyping: isTyping}) {
			s.log.Warn("typing queue full or closed; dropping", "user_id", userID, "conversation_id", id, "is_typing", isTyping)
		}
	})
}
