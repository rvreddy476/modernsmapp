// Package mediaclient resolves media ids into delivery URLs through
// media-service's POST /v1/media/batch, as the viewer — the same contract
// post-service and feed-service use for avatars and post media.
//
// Search needs it at QUERY time, not index time: a post document carries
// the first attached asset's id (and the author's avatar media id lives on
// the user document), and the signed rendition URLs media-service hands
// out are short-lived, so they cannot be stored in the index. One batch
// call per result page (chunked at media-service's 50-id cap) turns those
// ids into thumbnail / avatar URLs for the result rows.
//
// BEST-EFFORT by design: a media-service blip must not take search down.
// Anything unresolved is simply a null thumbnail_url / avatar_url on the
// row, exactly as an image-less post would render.
package mediaclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// batchLimit is media-service's per-call cap (BatchMediaURLsRequest max=50).
const batchLimit = 50

// Asset is what media-service reports for one id the viewer may see.
type Asset struct {
	MediaID    string
	Kind       string // image | video | audio
	DurationMs int
	Variants   map[string]string
	// PlaybackURL is the one URL a player should open for a video
	// (gateway-relative HLS master, or the signed progressive original
	// while the ladder is still being built).
	PlaybackURL string
}

// imageVariantPreference: the rendition a result card / avatar should use
// for an IMAGE asset, best first (mirrors post-service's avatar choice).
var imageVariantPreference = []string{"small_480", "thumb_480", "thumb_150", "medium_1080", "original"}

// videoPosterPreference: the poster-frame rendition for a VIDEO asset.
// Never a video rendition ("360p", "original"…) — those are not images.
var videoPosterPreference = []string{"thumb_480", "small_480", "poster", "thumbnail", "thumb_150"}

// ThumbnailURL picks the image rendition a result card should show, or ""
// when the asset has no usable image rendition.
func (a Asset) ThumbnailURL() string {
	prefs := imageVariantPreference
	if a.Kind == "video" {
		prefs = videoPosterPreference
	}
	for _, name := range prefs {
		if u := a.Variants[name]; u != "" {
			return u
		}
	}
	return ""
}

// Client calls media-service. A nil *Client, or one with an empty baseURL,
// resolves nothing (dev mode without media-service).
type Client struct {
	baseURL     string
	internalKey string
	httpClient  *http.Client
}

// New returns a client for media-service at baseURL; internalKey is
// forwarded as X-Internal-Service-Key.
func New(baseURL, internalKey string) *Client {
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		internalKey: internalKey,
		httpClient:  &http.Client{Timeout: 3 * time.Second},
	}
}

// Resolve returns the assets among ids that the viewer (empty = anonymous)
// may see. Unknown, denied and unresolvable ids are absent from the map;
// errors are logged, never returned — see the package comment.
func (c *Client) Resolve(ctx context.Context, viewerID string, ids []string) map[string]Asset {
	out := map[string]Asset{}
	if c == nil || c.baseURL == "" || len(ids) == 0 {
		return out
	}
	// Dedupe, keep order.
	seen := make(map[string]bool, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	for start := 0; start < len(unique); start += batchLimit {
		end := start + batchLimit
		if end > len(unique) {
			end = len(unique)
		}
		c.resolveChunk(ctx, viewerID, unique[start:end], out)
	}
	return out
}

func (c *Client) resolveChunk(ctx context.Context, viewerID string, ids []string, out map[string]Asset) {
	body, err := json.Marshal(map[string]any{"ids": ids})
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/media/batch", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if viewerID != "" {
		req.Header.Set("X-User-Id", viewerID)
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "search: media batch skipped", "err", err)
		return
	}
	defer resp.Body.Close()
	var envelope struct {
		Data map[string]struct {
			MediaID     string            `json:"media_id"`
			Kind        string            `json:"kind"`
			DurationMs  int               `json:"duration_ms"`
			Variants    map[string]string `json:"variants"`
			PlaybackURL string            `json:"playback_url"`
		} `json:"data"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope)
	if resp.StatusCode != http.StatusOK || decodeErr != nil {
		slog.WarnContext(ctx, "search: media batch skipped", "status", resp.StatusCode, "err", decodeErr)
		return
	}
	for id, d := range envelope.Data {
		out[id] = Asset{
			MediaID:     id,
			Kind:        d.Kind,
			DurationMs:  d.DurationMs,
			Variants:    d.Variants,
			PlaybackURL: d.PlaybackURL,
		}
	}
}
