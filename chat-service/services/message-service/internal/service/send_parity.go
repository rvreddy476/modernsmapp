package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// buildExtra resolves the rich payload for a send. It returns the reply to
// store when the send answers a Note (reply_kind "note").
func (s *Service) buildExtra(ctx context.Context, senderID uuid.UUID, in SendInput, memberIDs []uuid.UUID) (*MessageExtra, *ReplyRef, error) {
	extra := &MessageExtra{Forwarded: in.Forwarded}
	if fe := in.forwardedExtra; fe != nil {
		extra.Voice, extra.Sticker, extra.GIF = fe.Voice, fe.Sticker, fe.GIF
		extra.ProfileUserID, extra.SoundID, extra.StreamID, extra.Media = fe.ProfileUserID, fe.SoundID, fe.StreamID, fe.Media
		extra.Location = fe.Location
		return extra, nil, nil
	}
	switch in.Type {
	case MessageTypeVoice:
		wave := append([]int{}, in.Waveform...)
		extra.Voice = &VoiceInfo{DurationMs: in.DurationMs, Waveform: wave}
	case MessageTypeSticker:
		st, err := s.resolveSticker(ctx, *in.StickerID)
		if err != nil {
			return nil, nil, err
		}
		extra.Sticker = st
	case MessageTypeGIF:
		g := *in.GIF
		extra.GIF = &g
	case MessageTypeProfile:
		extra.ProfileUserID = in.ProfileUserID
	case MessageTypeSound:
		extra.SoundID = in.SoundID
	case MessageTypeLive:
		extra.StreamID = in.StreamID
	case MessageTypeLocation:
		// Validated already; stored rounded and trimmed, sealed with the extra.
		extra.Location = normalizeLocation(in.Location)
	}
	if in.MediaID != nil && (in.Type == MessageTypeMedia || in.Type == MessageTypeVoice) {
		info := s.fetchMediaInfo(ctx, *in.MediaID)
		if in.Type == MessageTypeVoice && info != nil && info.Kind != "audio" {
			return nil, nil, fmt.Errorf("%w: a voice message needs an audio upload", ErrInvalidMessageContent)
		}
		extra.Media = info
	}

	var reply *ReplyRef
	switch {
	case in.ReplyNoteID != nil:
		note, err := s.noteForReply(ctx, senderID, *in.ReplyNoteID, memberIDs)
		if err != nil {
			return nil, nil, err
		}
		preview := note.Text
		if r := []rune(preview); len(r) > 140 {
			preview = string(r[:140])
		}
		reply = &ReplyRef{ID: note.ID, Preview: preview, SenderID: note.UserID}
		extra.ReplyKind = "note"
	case in.ReplyTo != nil:
		extra.ReplyKind = "message"
	}
	return extra, reply, nil
}

// addExtraToFrame adds the rich fields to the realtime "message" frame so a
// live bubble renders the same as one loaded from history.
func addExtraToFrame(body map[string]interface{}, e *MessageExtra) {
	body["forwarded"] = e != nil && e.Forwarded
	if e == nil {
		return
	}
	if e.Voice != nil {
		body["voice"] = e.Voice
	}
	if e.Sticker != nil {
		body["sticker"] = StickerInfo{ID: e.Sticker.ID, URL: e.Sticker.URL, Width: e.Sticker.Width, Height: e.Sticker.Height}
	}
	if e.GIF != nil {
		body["gif"] = e.GIF
	}
	if e.ProfileUserID != nil {
		body["profile_user_id"] = e.ProfileUserID
	}
	if e.SoundID != nil {
		body["sound_id"] = e.SoundID
	}
	if e.StreamID != nil {
		body["stream_id"] = e.StreamID
	}
	if e.Location != nil {
		body["location"] = e.Location
	}
	if e.ReplyKind != "" {
		body["reply_kind"] = e.ReplyKind
	}
	if e.Media != nil {
		body["media"] = e.Media
	}
	if e.ExpiresAt != nil {
		body["expires_at"] = e.ExpiresAt
	}
}

// resolveStickerURLs fills sticker URLs (as the viewer) for catalogue
// stickers stored without a public URL. One batch per page.
func (s *Service) resolveStickerURLs(ctx context.Context, viewerID uuid.UUID, msgs []MessageResponse) {
	var ids []string
	for _, m := range msgs {
		if m.Sticker != nil && m.Sticker.URL == "" && m.Sticker.MediaID != uuid.Nil {
			ids = append(ids, m.Sticker.MediaID.String())
		}
	}
	if len(ids) == 0 {
		return
	}
	urls := s.stickerURLs(ctx, viewerID, ids)
	for i := range msgs {
		if st := msgs[i].Sticker; st != nil && st.URL == "" {
			copyOf := *st
			copyOf.URL = urls[st.MediaID.String()]
			msgs[i].Sticker = &copyOf
		}
	}
}
