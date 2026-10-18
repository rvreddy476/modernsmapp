package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// --- Media metadata (§2.1) ---

// fetchMediaInfo reads the media-service record as a keyed service caller
// (GET /v1/media/:id) for kind, size, duration and — fix pass 2 — the
// moderation verdict: media-service answers keyed callers with
// data.sensitive (true|false, or null when no real scan is on record). It is
// read defensively: an absent or null field stays null, never false.
// Best-effort: nil when the record cannot be read.
func (s *Service) fetchMediaInfo(ctx context.Context, mediaID uuid.UUID) *MediaInfo {
	if s.mediaServiceURL == "" || s.internalServiceKey == "" || s.httpClient == nil {
		return nil
	}
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, s.mediaServiceURL+"/v1/media/"+mediaID.String(), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}
	var env struct {
		Data struct {
			FileType        string `json:"file_type"`
			Width           *int   `json:"width"`
			Height          *int   `json:"height"`
			DurationMs      *int   `json:"duration_ms"`
			DurationSeconds *int   `json:"duration_seconds"`
			Sensitive       *bool  `json:"sensitive"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	d := env.Data
	kind := strings.ToLower(d.FileType)
	if kind != "image" && kind != "video" && kind != "audio" {
		return nil
	}
	info := &MediaInfo{Kind: kind, Width: d.Width, Height: d.Height, DurationMs: d.DurationMs, Sensitive: d.Sensitive}
	if info.DurationMs == nil && d.DurationSeconds != nil {
		ms := *d.DurationSeconds * 1000
		info.DurationMs = &ms
	}
	return info
}

// --- Stickers (§2.7) ---

// StickerCatalogue is GET /v1/chat/stickers.
type StickerCatalogue struct {
	Packs []StickerPackOut `json:"packs"`
}

type StickerPackOut struct {
	ID       uuid.UUID    `json:"id"`
	Name     string       `json:"name"`
	Stickers []StickerOut `json:"stickers"`
}

type StickerOut struct {
	ID     uuid.UUID `json:"id"`
	URL    string    `json:"url"`
	Width  int       `json:"width"`
	Height int       `json:"height"`
}

// ListStickers returns the active catalogue with URLs the viewer may use.
func (s *Service) ListStickers(ctx context.Context, viewerID uuid.UUID) (*StickerCatalogue, error) {
	out := &StickerCatalogue{Packs: []StickerPackOut{}}
	ps, ok := s.parity()
	if !ok {
		return out, nil
	}
	packs, err := ps.ListStickerCatalogue(ctx)
	if err != nil {
		return nil, err
	}
	var mediaIDs []string
	for _, p := range packs {
		for _, st := range p.Stickers {
			if st.URL == "" {
				mediaIDs = append(mediaIDs, st.MediaID.String())
			}
		}
	}
	urls := s.stickerURLs(ctx, viewerID, mediaIDs)
	for _, p := range packs {
		po := StickerPackOut{ID: p.ID, Name: p.Name, Stickers: []StickerOut{}}
		for _, st := range p.Stickers {
			u := st.URL
			if u == "" {
				u = urls[st.MediaID.String()]
			}
			po.Stickers = append(po.Stickers, StickerOut{ID: st.ID, URL: u, Width: st.Width, Height: st.Height})
		}
		out.Packs = append(out.Packs, po)
	}
	return out, nil
}

func (s *Service) stickerURLs(ctx context.Context, viewerID uuid.UUID, mediaIDs []string) map[string]string {
	if len(mediaIDs) == 0 || s.mediaServiceURL == "" || s.httpClient == nil {
		return map[string]string{}
	}
	return s.fetchMediaURLs(ctx, viewerID, mediaIDs)
}

// resolveSticker validates a catalogue sticker for a send.
func (s *Service) resolveSticker(ctx context.Context, id uuid.UUID) (*storedStick, error) {
	ps, ok := s.parity()
	if !ok {
		return nil, ErrStickerNotFound
	}
	st, err := ps.GetActiveSticker(ctx, id)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, ErrStickerNotFound
	}
	return &storedStick{ID: st.ID, MediaID: st.MediaID, URL: st.URL, Width: st.Width, Height: st.Height}, nil
}

// ErrInvalidSticker is a malformed admin catalogue write.
var ErrInvalidSticker = errors.New("invalid sticker")

// UpsertStickerPackAdmin creates or updates a pack (internal admin route).
func (s *Service) UpsertStickerPackAdmin(ctx context.Context, id uuid.UUID, name string, sortOrder int, active bool) (uuid.UUID, error) {
	ps, ok := s.parity()
	if !ok {
		return uuid.Nil, errors.New("stickers not supported")
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 40 {
		return uuid.Nil, fmt.Errorf("%w: name must be 1..40 characters", ErrInvalidSticker)
	}
	if id == uuid.Nil {
		id = uuid.New()
	}
	return id, ps.UpsertStickerPack(ctx, id, name, sortOrder, active)
}

// UpsertStickerAdmin adds or updates a sticker (internal admin route). The
// media id must be a media-service asset; the url is optional (a public
// CDN rendition) — when empty, viewers get a delivery URL per request.
func (s *Service) UpsertStickerAdmin(ctx context.Context, st postgres.Sticker, active bool) (uuid.UUID, error) {
	ps, ok := s.parity()
	if !ok {
		return uuid.Nil, errors.New("stickers not supported")
	}
	if st.PackID == uuid.Nil || st.MediaID == uuid.Nil || st.Width <= 0 || st.Height <= 0 || st.Width > 4096 || st.Height > 4096 {
		return uuid.Nil, fmt.Errorf("%w: pack_id, media_id and a positive width/height are required", ErrInvalidSticker)
	}
	if st.URL != "" && !strings.HasPrefix(st.URL, "https://") {
		return uuid.Nil, fmt.Errorf("%w: url must be https", ErrInvalidSticker)
	}
	if st.ID == uuid.Nil {
		st.ID = uuid.New()
	}
	return st.ID, ps.UpsertSticker(ctx, st, active)
}
