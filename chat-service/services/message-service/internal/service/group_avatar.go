package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Chat-app pass (2026-09-05): group avatars end-to-end.
//
// A group avatar is a media-service asset the owner/admin uploaded. Two things
// have to be true for every member to render it:
//
//  1. media-service must know chat vouches for it — the chat delivery
//     authority (/internal/v1/chat/media-access) answers from
//     chat.message_media_references, so setting an avatar writes a reference
//     row keyed by a deterministic per-(group, media) id. Ownership/readiness
//     is proven first through the same reserve call attachments use.
//  2. The list/get responses carry a URL the VIEWER may use: one batch call to
//     media-service delivery per page, as the viewer, never per row.

// groupAvatarReferenceID is the deterministic message_media_references key
// (and media reservation id) for a group avatar: stable per (group, media).
func groupAvatarReferenceID(conversationID, mediaID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(conversationID, []byte("group-avatar:"+mediaID.String()))
}

type mediaReferenceStore interface {
	InsertMessageMediaReference(ctx context.Context, messageID, mediaID, conversationID uuid.UUID) error
}

// registerGroupAvatar proves the actor owns a ready, approved asset and makes
// it deliverable to the roster.
func (s *Service) registerGroupAvatar(ctx context.Context, conversationID, actorID, mediaID uuid.UUID) error {
	refID := groupAvatarReferenceID(conversationID, mediaID)
	if err := s.reserveChatAttachment(ctx, refID, actorID, mediaID); err != nil {
		return err
	}
	refs, ok := s.convStore.(mediaReferenceStore)
	if !ok {
		return errors.New("media reference store unavailable")
	}
	return refs.InsertMessageMediaReference(ctx, refID, mediaID, conversationID)
}

// mediaDeliveryEntry is the slice of media-service's MediaURLResponse this
// service reads.
type mediaDeliveryEntry struct {
	Variants    map[string]string `json:"variants"`
	PlaybackURL string            `json:"playback_url"`
}

// avatarVariantPreference: the inbox renders a small square; fall back to the
// original for assets without renditions yet.
var avatarVariantPreference = []string{"thumb_300", "thumb_150", "medium", "small", "original"}

func pickAvatarURL(e mediaDeliveryEntry) string {
	for _, name := range avatarVariantPreference {
		if u := e.Variants[name]; u != "" {
			return u
		}
	}
	return e.PlaybackURL
}

// resolveGroupAvatarURLs fills AvatarURL for every group row with an avatar,
// as the viewer, in one media-service batch call. Best-effort: a delivery
// failure leaves avatar_url empty (avatar_media_id still rides along).
func (s *Service) resolveGroupAvatarURLs(ctx context.Context, viewerID uuid.UUID, convs []ConversationResponse) {
	if s.mediaServiceURL == "" || s.httpClient == nil {
		return
	}
	ids := make([]string, 0, len(convs))
	seen := map[uuid.UUID]bool{}
	for _, c := range convs {
		if c.Type == "group" && c.AvatarMediaID != nil && !seen[*c.AvatarMediaID] {
			seen[*c.AvatarMediaID] = true
			ids = append(ids, c.AvatarMediaID.String())
		}
	}
	if len(ids) == 0 {
		return
	}
	urls := s.fetchMediaURLs(ctx, viewerID, ids)
	for i := range convs {
		if convs[i].AvatarMediaID != nil {
			convs[i].AvatarURL = urls[convs[i].AvatarMediaID.String()]
		}
	}
}

// fetchMediaURLs calls media-service delivery (POST /v1/media/batch) as the
// viewer. Batches of at most 50 (the delivery limit).
func (s *Service) fetchMediaURLs(ctx context.Context, viewerID uuid.UUID, ids []string) map[string]string {
	out := map[string]string{}
	for start := 0; start < len(ids); start += 50 {
		end := start + 50
		if end > len(ids) {
			end = len(ids)
		}
		body, err := json.Marshal(map[string]any{"ids": ids[start:end]})
		if err != nil {
			return out
		}
		reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.mediaServiceURL+"/v1/media/batch", bytes.NewReader(body))
		if err != nil {
			cancel()
			return out
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", viewerID.String())
		if s.internalServiceKey != "" {
			req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			cancel()
			s.log.Warn("group avatar delivery lookup failed", "err", err)
			return out
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		cancel()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			s.log.Warn("group avatar delivery lookup non-200", "status", resp.StatusCode)
			return out
		}
		var envelope struct {
			Data map[string]mediaDeliveryEntry `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			s.log.Warn("group avatar delivery decode failed", "err", fmt.Errorf("%w", err))
			return out
		}
		for id, entry := range envelope.Data {
			if u := pickAvatarURL(entry); u != "" {
				out[id] = u
			}
		}
	}
	return out
}
