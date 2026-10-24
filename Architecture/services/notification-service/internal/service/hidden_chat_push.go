package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/notification-service/internal/push"
	"github.com/google/uuid"
)

// Hidden chats (chat-service contract pinned 24 Oct 2026).
//
// A recipient who hid a conversation (chat ships them in
// MessageCreated.locked_recipient_ids, chat.dating.message.new.hidden_chat
// and CallInvited.hidden_chat) gets, for each message, ONE content-free
// device push:
//
//	title "New message", body "" — no sender, no text, no avatar, no
//	conversation id, no deep link into the chat;
//	collapse key "hidden_chat:<recipient>" so every hidden message folds
//	into a single "New message" notification;
//	NO inbox row and NO realtime notify frame, so nothing reaches the
//	notification list or the app badge (the badge counts inbox rows).
//
// A call from a hidden conversation rings "Incoming call" with no name: the
// push carries the call id (the app must be able to answer) but no caller,
// and no inbox row or realtime frame is written.

// Hidden-chat push copy and keys.
const (
	HiddenChatPushTitle      = "New message"
	HiddenCallPushTitle      = "Incoming call"
	HiddenChatEntityType     = "hidden_chat"
	HiddenChatDataKey        = "hidden_chat"
	hiddenChatDeepLink       = "/messenger"
	hiddenChatCollapsePrefix = "hidden_chat:"
)

// HiddenChatPushData is the complete data map of a hidden-chat message push.
// Exported so tests can prove what it does NOT carry.
func HiddenChatPushData(recipientID uuid.UUID) map[string]string {
	return map[string]string{
		"type":                     "dm",
		"entity_type":              HiddenChatEntityType,
		"entity_id":                uuid.Nil.String(),
		"deep_link":                hiddenChatDeepLink,
		"title":                    HiddenChatPushTitle,
		"body":                     "",
		HiddenChatDataKey:          "true",
		"collapse_key":             hiddenChatCollapsePrefix + recipientID.String(),
		push.AndroidChannelDataKey: ChatChannelMessages,
	}
}

// CreateHiddenChatMessagePush delivers the content-free push for one message
// in a conversation the recipient hid. Preferences still apply (master
// toggle, the "dm" category, quiet hours) and a muted conversation stays
// silent. Nothing is written to the inbox or published to realtime.
func (s *Service) CreateHiddenChatMessagePush(ctx context.Context, userID uuid.UUID, muted bool) error {
	if s.recipientSuppressed(ctx, userID, "dm") {
		return nil
	}
	decision := s.resolveGeneralDelivery(ctx, userID, "dm")
	if muted || !decision.SendPush || s.pusher == nil || s.pgStore == nil {
		return nil
	}
	tokens, err := s.eventDevices(ctx, userID, "dm")
	if err != nil {
		return fmt.Errorf("hidden chat push devices: %w", err)
	}
	data := HiddenChatPushData(userID)
	for _, t := range tokens {
		if err := s.pusher.Send(ctx, t.PushToken, t.Platform, HiddenChatPushTitle, "", data); err != nil {
			slog.Warn("push: hidden chat send failed", "error", err, "platform", t.Platform)
		}
	}
	return nil
}

// HiddenCallPushData is the data map of a nameless ring: the call id and
// type (the app must reach the call) and nothing about the caller.
func HiddenCallPushData(notifType string, callID uuid.UUID) map[string]string {
	return map[string]string{
		"type":            notifType,
		"entity_id":       callID.String(),
		"deep_link":       fmt.Sprintf("/call/%s", callID),
		HiddenChatDataKey: "true",
	}
}

// CreateHiddenCallPush rings a callee who hid the conversation: push only,
// title "Incoming call", no body, no caller. At-least-once like every ring
// (errors propagate so the consumer retries); no inbox row, no realtime.
func (s *Service) CreateHiddenCallPush(ctx context.Context, userID uuid.UUID, notifType string, callID uuid.UUID) error {
	targets, allowed, err := s.callPushTargets(ctx, userID)
	if err != nil {
		return fmt.Errorf("hidden call push targets: %w", err)
	}
	if !allowed {
		return nil
	}
	data := HiddenCallPushData(notifType, callID)
	for _, t := range targets {
		err := s.pusher.Send(ctx, t.Token, t.Platform, HiddenCallPushTitle, "", data)
		switch {
		case err == nil:
		case errors.Is(err, push.ErrDeviceRejected):
			if retireErr := s.retireCallDevice(ctx, userID, t); retireErr != nil {
				return fmt.Errorf("retire rejected device (%s): %w", t.Platform, retireErr)
			}
		case errors.Is(err, push.ErrProviderNotConfigured) && !s.callPushRequired:
			slog.Warn("hidden call push skipped: provider not configured", "platform", t.Platform)
		default:
			return fmt.Errorf("hidden call push (%s): %w", t.Platform, err)
		}
	}
	return nil
}
