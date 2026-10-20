package service

import (
	"encoding/json"

	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Chat parity §2.1 / §2.2 (lane B1, 2026-10-18): the rich message types.
//
// Every rich field rides ONE JSON payload (MessageExtra) — on the durable
// delivery intent (chat.message_delivery_intents.extra) and on the Scylla row
// (chatservice.messages.extra) — so repair replays exactly what the live path
// wrote, and a new field never needs another column on both stores.
const (
	MessageTypeVoice   = "voice"
	MessageTypeSticker = "sticker"
	MessageTypeGIF     = "gif"
	MessageTypeProfile = "profile"
	MessageTypeSound   = "sound"
	MessageTypeLive    = "live"
)

// Voice limits (CONTRACT §2.1).
const (
	VoiceMaxDurationMs = 300000
	VoiceMaxWaveform   = 64
)

// Previews for the rich types (our words, CONTRACT §9).
const (
	VoicePreview   = "Voice message"
	StickerPreview = "Sticker"
	GIFPreview     = "GIF"
	ProfilePreview = "Shared a profile"
	SoundPreview   = "Shared a sound"
	LivePreview    = "Shared a LIVE"
)

// ErrStickerNotFound is a sticker id outside the active catalogue.
var ErrStickerNotFound = fmt.Errorf("%w: sticker not found", ErrInvalidMessageContent)

// VoiceInfo is the voice-note shape on the wire.
type VoiceInfo struct {
	DurationMs int   `json:"duration_ms"`
	Waveform   []int `json:"waveform"`
}

// StickerInfo is the sticker shape on the wire. URL is resolved per viewer.
type StickerInfo struct {
	ID      uuid.UUID `json:"id"`
	MediaID uuid.UUID `json:"-"`
	URL     string    `json:"url"`
	Width   int       `json:"width"`
	Height  int       `json:"height"`
}

// GIFInfo is the gif card, as the client chose it through the Giphy SDK
// (see gifs.go): stored and returned unmodified.
type GIFInfo struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	URL      string `json:"url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	StillURL string `json:"still_url,omitempty"`
	// IsSticker marks a Giphy sticker (transparent art, drawn without a
	// bubble) sent through the gif type (fix pass 2). Always on the wire.
	IsSticker bool `json:"is_sticker"`
}

// MediaInfo is the attachment metadata from the media-service record.
// Sensitive is media-service's moderation verdict (null while unknown: no
// real scan on record yet, or the record could not be read).
type MediaInfo struct {
	Kind       string `json:"kind"`
	Width      *int   `json:"width"`
	Height     *int   `json:"height"`
	DurationMs *int   `json:"duration_ms"`
	Sensitive  *bool  `json:"sensitive"`
}

// MessageExtra is the stored rich payload.
type MessageExtra struct {
	Forwarded     bool         `json:"forwarded,omitempty"`
	Voice         *VoiceInfo   `json:"voice,omitempty"`
	Sticker       *storedStick `json:"sticker,omitempty"`
	GIF           *GIFInfo     `json:"gif,omitempty"`
	ProfileUserID *uuid.UUID   `json:"profile_user_id,omitempty"`
	SoundID       *uuid.UUID   `json:"sound_id,omitempty"`
	StreamID      *uuid.UUID   `json:"stream_id,omitempty"`
	ReplyKind     string       `json:"reply_kind,omitempty"`
	Media         *MediaInfo   `json:"media,omitempty"`
	// ExpiresAt is when a disappearing message goes (created_at + the
	// conversation timer captured AT SEND TIME). Nil: never.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// storedStick keeps the sticker's media id in storage (the wire never shows
// it; the URL is resolved for the viewer at read time).
type storedStick struct {
	ID      uuid.UUID `json:"id"`
	MediaID uuid.UUID `json:"media_id"`
	URL     string    `json:"url,omitempty"`
	Width   int       `json:"width"`
	Height  int       `json:"height"`
}

func (e *MessageExtra) empty() bool {
	return e == nil || (!e.Forwarded && e.Voice == nil && e.Sticker == nil && e.GIF == nil &&
		e.ProfileUserID == nil && e.SoundID == nil && e.StreamID == nil && e.ReplyKind == "" && e.Media == nil &&
		e.ExpiresAt == nil)
}

func encodeExtra(e *MessageExtra) ([]byte, string) {
	if e.empty() {
		return nil, ""
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, ""
	}
	return raw, string(raw)
}

func decodeExtra(raw string) *MessageExtra {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var e MessageExtra
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return nil
	}
	return &e
}

// applyExtra copies the stored payload onto a wire message.
func applyExtra(resp *MessageResponse, e *MessageExtra) {
	if resp == nil {
		return
	}
	if e == nil {
		// A quote stored before reply kinds existed is a message reply.
		if resp.ReplyToID != nil && resp.ReplyKind == nil {
			k := "message"
			resp.ReplyKind = &k
		}
		return
	}
	defer applyExtra(resp, nil)
	resp.Forwarded = e.Forwarded
	resp.Voice = e.Voice
	if e.Sticker != nil {
		resp.Sticker = &StickerInfo{ID: e.Sticker.ID, MediaID: e.Sticker.MediaID, URL: e.Sticker.URL, Width: e.Sticker.Width, Height: e.Sticker.Height}
	}
	resp.GIF = e.GIF
	resp.ProfileUserID = e.ProfileUserID
	resp.SoundID = e.SoundID
	resp.StreamID = e.StreamID
	if e.ReplyKind != "" {
		k := e.ReplyKind
		resp.ReplyKind = &k
	}
	resp.Media = e.Media
	resp.ExpiresAt = e.ExpiresAt
}

// GIFRef is the client's gif card on a send (same shape as GIFInfo).
type GIFRef = GIFInfo

// SendInput is every field a send can carry. The legacy positional
// SendMessage builds one with the first eight fields.
type SendInput struct {
	Type           string
	Text           string
	MediaID        *uuid.UUID
	PostID         *uuid.UUID
	ReplyTo        *ReplyRef
	IdempotencyKey string

	ReplyNoteID   *uuid.UUID
	DurationMs    int
	Waveform      []int
	StickerID     *uuid.UUID
	GIF           *GIFRef
	ProfileUserID *uuid.UUID
	SoundID       *uuid.UUID
	StreamID      *uuid.UUID

	// Forwarded marks a forwarded copy (§2.4). forwardedMediaOK skips the
	// uploader-only media reservation for an attachment the forwarder can
	// already see in the source conversation.
	Forwarded        bool
	forwardedMediaOK bool
	forwardedExtra   *MessageExtra
}

func nonNil(id *uuid.UUID) bool { return id != nil && *id != uuid.Nil }

// validateSendInput is the service-side shape check for every send. The
// three original types keep exactly validateMessageContent's rules; the rich
// types are checked here.
func validateSendInput(in SendInput) error {
	richSet := nonNil(in.StickerID) || in.GIF != nil || nonNil(in.ProfileUserID) || nonNil(in.SoundID) ||
		nonNil(in.StreamID) || in.DurationMs != 0 || len(in.Waveform) > 0
	switch in.Type {
	case MessageTypeText, MessageTypeMedia, MessageTypePost:
		if richSet {
			return fmt.Errorf("%w: rich fields are not allowed on %s messages", ErrInvalidMessageContent, in.Type)
		}
		return validateMessageContent(in.Type, in.Text, in.MediaID, in.PostID)
	}
	if in.PostID != nil {
		return fmt.Errorf("%w: post_id is only allowed on post messages", ErrInvalidMessageContent)
	}
	if utf8.RuneCountInString(in.Text) > MessageTextMaxRunes {
		return fmt.Errorf("%w: text exceeds %d characters", ErrInvalidMessageContent, MessageTextMaxRunes)
	}
	only := func(field string, set bool) error {
		if !set {
			return fmt.Errorf("%w: %s is required for %s messages", ErrInvalidMessageContent, field, in.Type)
		}
		others := 0
		for _, b := range []bool{nonNil(in.StickerID), in.GIF != nil, nonNil(in.ProfileUserID), nonNil(in.SoundID), nonNil(in.StreamID)} {
			if b {
				others++
			}
		}
		if in.Type != MessageTypeVoice && others != 1 {
			return fmt.Errorf("%w: exactly one card field is allowed on %s messages", ErrInvalidMessageContent, in.Type)
		}
		if in.Type != MessageTypeVoice && (in.MediaID != nil || in.DurationMs != 0 || len(in.Waveform) > 0) {
			return fmt.Errorf("%w: media fields are not allowed on %s messages", ErrInvalidMessageContent, in.Type)
		}
		return nil
	}
	switch in.Type {
	case MessageTypeVoice:
		if !nonNil(in.MediaID) {
			return fmt.Errorf("%w: media_id is required for voice messages", ErrInvalidMessageContent)
		}
		if nonNil(in.StickerID) || in.GIF != nil || nonNil(in.ProfileUserID) || nonNil(in.SoundID) || nonNil(in.StreamID) {
			return fmt.Errorf("%w: card fields are not allowed on voice messages", ErrInvalidMessageContent)
		}
		if in.DurationMs < 1 || in.DurationMs > VoiceMaxDurationMs {
			return fmt.Errorf("%w: duration_ms must be 1..%d", ErrInvalidMessageContent, VoiceMaxDurationMs)
		}
		if len(in.Waveform) > VoiceMaxWaveform {
			return fmt.Errorf("%w: waveform has at most %d values", ErrInvalidMessageContent, VoiceMaxWaveform)
		}
		for _, v := range in.Waveform {
			if v < 0 || v > 255 {
				return fmt.Errorf("%w: waveform values are 0..255", ErrInvalidMessageContent)
			}
		}
		if strings.TrimSpace(in.Text) != "" {
			return fmt.Errorf("%w: voice messages carry no text", ErrInvalidMessageContent)
		}
		return nil
	case MessageTypeSticker:
		if err := only("sticker_id", nonNil(in.StickerID)); err != nil {
			return err
		}
		if strings.TrimSpace(in.Text) != "" {
			return fmt.Errorf("%w: sticker messages carry no text", ErrInvalidMessageContent)
		}
		return nil
	case MessageTypeGIF:
		if err := only("gif", in.GIF != nil); err != nil {
			return err
		}
		if strings.TrimSpace(in.Text) != "" {
			return fmt.Errorf("%w: gif messages carry no text", ErrInvalidMessageContent)
		}
		return validateGIFRef(in.GIF)
	case MessageTypeProfile:
		return only("user_id", nonNil(in.ProfileUserID))
	case MessageTypeSound:
		return only("sound_id", nonNil(in.SoundID))
	case MessageTypeLive:
		return only("stream_id", nonNil(in.StreamID))
	}
	return fmt.Errorf("%w: unknown message type %q", ErrInvalidMessageContent, in.Type)
}

// richPreview is the one-line preview for the rich types; an optional
// caption (profile / sound / live) wins over the default words.
func richPreview(msgType, text string) (string, bool) {
	var def string
	switch msgType {
	case MessageTypeVoice:
		return VoicePreview, true
	case MessageTypeSticker:
		return StickerPreview, true
	case MessageTypeGIF:
		return GIFPreview, true
	case MessageTypeProfile:
		def = ProfilePreview
	case MessageTypeSound:
		def = SoundPreview
	case MessageTypeLive:
		def = LivePreview
	default:
		return "", false
	}
	if strings.TrimSpace(text) != "" {
		return text, true
	}
	return def, true
}
