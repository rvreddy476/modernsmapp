package service

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Message types. Every decision that depends on a message's type goes
// through validateMessageContent and messagePreview below, and
// TestEveryMessageTypeIsHandled walks KnownMessageTypes through both (and
// checks the HTTP binding's oneof list matches) — so a type added here but
// not handled fails the tests instead of silently landing in a default branch.
const (
	MessageTypeText  = "text"
	MessageTypeMedia = "media"
	// MessageTypePost is "Send to friends" (TikTok parity T-25): a card for
	// a post-service post. Only the post id is stored. The server never
	// copies the post's caption, author or media into the message, so a
	// sender cannot leak a post the recipient is not allowed to see: clients
	// hydrate the card through post-service /v1/posts/batch AS THE VIEWER,
	// which enforces visibility, and render "not available" for an id the
	// batch does not return (deleted, hidden, private, blocked author).
	MessageTypePost = "post"
)

// KnownMessageTypes is every type SendMessage accepts.
var KnownMessageTypes = []string{MessageTypeText, MessageTypeMedia, MessageTypePost,
	MessageTypeVoice, MessageTypeSticker, MessageTypeGIF, MessageTypeProfile, MessageTypeSound, MessageTypeLive}

// MessageTextMaxRunes matches the HTTP binding (`max=2000`) and applies to a
// post's optional caption exactly as to a text message.
const MessageTextMaxRunes = 2000

// PostMessagePreview is the inbox / last-message preview for a post card sent
// without a caption.
const PostMessagePreview = "Sent a post"

// ErrInvalidMessageContent rejects a message whose fields do not fit its type.
var ErrInvalidMessageContent = errors.New("invalid message content")

// validateMessageContent is the service-side shape check for every send,
// including ones that do not come through the HTTP handler (the scheduled
// message worker, the dating first-move path). The handler checks the same
// rules first for a clean 400.
//
// Text and media keep exactly the rules they had before post cards existed
// (the handler owns "text required" / "media_id required" and the 2000-rune
// cap; scheduled messages have never had a cap). What is new is enforced here
// for every caller: post_id rides only on a post, a post carries a non-nil
// post_id and no attachment, its caption obeys the text cap, and an unknown
// type is refused instead of being stored verbatim.
func validateMessageContent(msgType, text string, mediaID, postID *uuid.UUID) error {
	switch msgType {
	case MessageTypeText, MessageTypeMedia:
		if postID != nil {
			return fmt.Errorf("%w: post_id is only allowed on post messages", ErrInvalidMessageContent)
		}
	case MessageTypePost:
		if postID == nil || *postID == uuid.Nil {
			return fmt.Errorf("%w: post_id is required for post messages", ErrInvalidMessageContent)
		}
		if mediaID != nil {
			return fmt.Errorf("%w: media_id is not allowed on post messages", ErrInvalidMessageContent)
		}
		if utf8.RuneCountInString(text) > MessageTextMaxRunes {
			return fmt.Errorf("%w: caption exceeds %d characters", ErrInvalidMessageContent, MessageTextMaxRunes)
		}
	default:
		return fmt.Errorf("%w: unknown message type %q", ErrInvalidMessageContent, msgType)
	}
	return nil
}

// messagePreview is the denormalised one-line preview of a message: the inbox
// projection, the conversation's last-message preview, the dating preview,
// the request preview and the post-delete preview repair all read it. Text
// and media keep their historical behaviour (the text; empty for an
// uncaptioned attachment). A post shows its caption, or "Sent a post" —
// never anything taken from the post itself.
func messagePreview(msgType, text string) string {
	if p, ok := richPreview(msgType, text); ok {
		return p
	}
	switch msgType {
	case MessageTypePost:
		if strings.TrimSpace(text) == "" {
			return PostMessagePreview
		}
		return text
	case MessageTypeText, MessageTypeMedia:
		return text
	default:
		return text
	}
}

// messagePreviewWithExtra is messagePreview that also reads the stored rich
// payload: a Giphy sticker (a gif with is_sticker, fix pass 2) previews as
// "Sticker", like a catalogue sticker, not "GIF".
func messagePreviewWithExtra(msgType, text, extraRaw string) string {
	if msgType == MessageTypeGIF {
		if e := decodeExtra(extraRaw); e != nil && e.GIF != nil && e.GIF.IsSticker {
			return StickerPreview
		}
	}
	return messagePreview(msgType, text)
}
