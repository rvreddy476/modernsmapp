package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
)

// Draft alt text at publish (2026-10-18).
//
// A composer draft carries alt_texts (media_id -> description, P0-7) and the
// publish used to drop them: the photos went out unlabelled whatever the
// author had written. publishDraftRow now writes each one onto the author's
// own media BEFORE CreatePost, so the publish response — the first render of
// the new post — already describes its images.
//
// The write goes through media-service, which owns media_assets, on its
// internal route (PATCH /v1/media/internal/:mediaId/alt-text, internal key,
// body {uploader_id, alt_text}). The public PATCH /v1/media/:id/alt-text
// needs the user's token, which the scheduled-publish worker never has.
// media-service updates only a row whose uploader_id is the author, so a
// draft naming somebody else's media changes nothing.
//
// Best-effort per image: a refusal or an outage is logged and the post
// publishes without that description, exactly as an image with none would.
// Only ids that are on the draft's media_ids are written; empty strings are
// skipped (the decorative mark is not expressible in alt_texts).

// draftAltTextWriter writes one description onto one asset of uploaderID.
type draftAltTextWriter interface {
	SetAltText(ctx context.Context, mediaID, uploaderID uuid.UUID, altText string) error
}

// MaxDraftAltTextRunes mirrors the composer's cap; longer values are cut.
const MaxDraftAltTextRunes = 1000

func (s *Service) altTextWriter() draftAltTextWriter {
	if s.draftAltTexts != nil {
		return s.draftAltTexts
	}
	return &httpDraftAltTextWriter{s: s}
}

// applyDraftAltTexts writes payload.AltTexts onto the draft's own media.
func (s *Service) applyDraftAltTexts(ctx context.Context, authorID uuid.UUID, payload *PostDraftPayload) {
	if payload == nil || len(payload.AltTexts) == 0 {
		return
	}
	writer := s.altTextWriter()
	// Walking media_ids (not the map) keeps the writes to the draft's own
	// media and in a deterministic order.
	for _, mediaID := range payload.MediaIDs {
		raw, ok := payload.AltTexts[mediaID.String()]
		if !ok {
			continue
		}
		alt := strings.TrimSpace(raw)
		if alt == "" {
			continue
		}
		if r := []rune(alt); len(r) > MaxDraftAltTextRunes {
			alt = string(r[:MaxDraftAltTextRunes])
		}
		if err := writer.SetAltText(ctx, mediaID, authorID, alt); err != nil {
			slog.Warn("draft publish: alt text not applied", "media_id", mediaID, "err", err)
		}
	}
}

// httpDraftAltTextWriter is media-service's internal alt-text route.
type httpDraftAltTextWriter struct{ s *Service }

func (w *httpDraftAltTextWriter) SetAltText(ctx context.Context, mediaID, uploaderID uuid.UUID, altText string) error {
	base := w.s.mediaServiceURL
	if base == "" {
		base = os.Getenv("MEDIA_SERVICE_URL")
	}
	if base == "" {
		base = "http://media-service:8087"
	}
	if w.s.internalServiceKey == "" {
		return fmt.Errorf("internal service key not configured")
	}
	body, err := json.Marshal(map[string]string{"uploader_id": uploaderID.String(), "alt_text": altText})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch,
		strings.TrimRight(base, "/")+"/v1/media/internal/"+mediaID.String()+"/alt-text", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", w.s.internalServiceKey)
	client := w.s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("media-service returned %d", resp.StatusCode)
	}
	return nil
}
