package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Mechanic M15 — dating → media-service internal dating clip routes
// (MEDIA_DATING_CLIPS_ENABLED on media-service). Same caller shape as the
// photo client: the internal service key and NO end-user identity headers.

// MediaDatingClipsPath prefixes media-service's dating clip routes.
const MediaDatingClipsPath = "/internal/v1/media/dating-clips/"

var (
	// ErrClipMediaNotFound: missing, or not the requester's.
	ErrClipMediaNotFound = errors.New("not_found: clip media not found")
	// ErrClipNotReady: still processing; the client retries.
	ErrClipNotReady = errors.New("conflict: clip is still processing")
	// ErrClipUnsupported: not a playable voice or video clip.
	ErrClipUnsupported = errors.New("invalid: only a voice or video clip can answer a prompt")
	// ErrClipMediaUnavailable: media-service could not be asked.
	ErrClipMediaUnavailable = errors.New("unavailable: media-service unavailable")
)

// ClipTooLongError is media-service's CLIP_TOO_LONG.
type ClipTooLongError struct{ MaxMs int }

func (e *ClipTooLongError) Error() string {
	return fmt.Sprintf("a clip is at most %d seconds", e.MaxMs/1000)
}

// ClipStatus is media-service's owner-status answer.
type ClipStatus struct {
	OwnerUserID uuid.UUID `json:"owner_user_id"`
	Kind        string    `json:"kind"`
	Processing  string    `json:"processing"`
	Moderation  string    `json:"moderation"`
	DurationMs  int       `json:"duration_ms"`
	Usable      bool      `json:"usable"`
}

// ClipDelivery is a short-lived playback URL.
type ClipDelivery struct {
	Kind      string    `json:"kind"`
	URL       string    `json:"url"`
	PosterURL string    `json:"poster_url,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// MediaClipClient is the media-service side of prompt clips.
type MediaClipClient interface {
	ClipOwnerStatus(ctx context.Context, mediaID, requester uuid.UUID) (*ClipStatus, error)
	PrepareClip(ctx context.Context, mediaID, requester uuid.UUID) (kind string, durationMs int, err error)
	ClipDeliveryURL(ctx context.Context, mediaID, owner uuid.UUID) (*ClipDelivery, error)
	DeleteClip(ctx context.Context, mediaID, requester uuid.UUID) error
}

// HTTPMediaClipClient is the production MediaClipClient.
type HTTPMediaClipClient struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

// NewHTTPMediaClipClient builds the client (15 s timeout by default).
func NewHTTPMediaClipClient(baseURL, internalKey string, httpClient *http.Client) *HTTPMediaClipClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &HTTPMediaClipClient{baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey, client: httpClient}
}

func (c *HTTPMediaClipClient) call(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("%w: encode: %v", ErrClipMediaUnavailable, err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: build request: %v", ErrClipMediaUnavailable, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: media-service unreachable: %v", ErrClipMediaUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	return resp.StatusCode, raw, nil
}

func decodeClipData[T any](raw []byte) (*T, error) {
	var env struct {
		Data *T `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Data == nil {
		return nil, fmt.Errorf("%w: undecodable media-service response", ErrClipMediaUnavailable)
	}
	return env.Data, nil
}

// clipRefusal maps a media-service error answer.
func clipRefusal(status int, raw []byte) error {
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	switch env.Error.Code {
	case "CLIP_NOT_FOUND":
		return ErrClipMediaNotFound
	case "CLIP_NOT_READY":
		return ErrClipNotReady
	case "CLIP_UNSUPPORTED":
		return ErrClipUnsupported
	case "CLIP_TOO_LONG":
		max := 30000
		if v, ok := env.Error.Details["max_ms"].(float64); ok && v > 0 {
			max = int(v)
		}
		return &ClipTooLongError{MaxMs: max}
	}
	if status == http.StatusNotFound {
		return ErrClipMediaNotFound
	}
	return fmt.Errorf("%w: status %d", ErrClipMediaUnavailable, status)
}

func (c *HTTPMediaClipClient) ClipOwnerStatus(ctx context.Context, mediaID, requester uuid.UUID) (*ClipStatus, error) {
	path := MediaDatingClipsPath + url.PathEscape(mediaID.String()) + "/owner-status?requester_user_id=" + url.QueryEscape(requester.String())
	status, raw, err := c.call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, clipRefusal(status, raw)
	}
	return decodeClipData[ClipStatus](raw)
}

func (c *HTTPMediaClipClient) PrepareClip(ctx context.Context, mediaID, requester uuid.UUID) (string, int, error) {
	status, raw, err := c.call(ctx, http.MethodPost, MediaDatingClipsPath+url.PathEscape(mediaID.String())+"/prepare",
		map[string]string{"requester_user_id": requester.String()})
	if err != nil {
		return "", 0, err
	}
	if status != http.StatusOK {
		return "", 0, clipRefusal(status, raw)
	}
	out, err := decodeClipData[struct {
		Kind       string `json:"kind"`
		DurationMs int    `json:"duration_ms"`
	}](raw)
	if err != nil {
		return "", 0, err
	}
	return out.Kind, out.DurationMs, nil
}

func (c *HTTPMediaClipClient) ClipDeliveryURL(ctx context.Context, mediaID, owner uuid.UUID) (*ClipDelivery, error) {
	status, raw, err := c.call(ctx, http.MethodPost, MediaDatingClipsPath+url.PathEscape(mediaID.String())+"/delivery-url",
		map[string]string{"owner_user_id": owner.String()})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, clipRefusal(status, raw)
	}
	return decodeClipData[ClipDelivery](raw)
}

func (c *HTTPMediaClipClient) DeleteClip(ctx context.Context, mediaID, requester uuid.UUID) error {
	path := MediaDatingClipsPath + url.PathEscape(mediaID.String()) + "?requester_user_id=" + url.QueryEscape(requester.String())
	status, raw, err := c.call(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	if status == http.StatusOK || status == http.StatusNotFound {
		return nil
	}
	return clipRefusal(status, raw)
}
