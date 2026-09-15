package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Lane D6 — dating → media-service internal dating photo routes.
//
// Same caller shape as the lane D5 liveness client: the internal service key
// and NO end-user identity headers (media-service refuses any request that
// carries X-User-Id / X-Verified-User-Id / X-Scopes / X-Admin-Role there).
// The user is named in the query or body; media-service checks the media is
// theirs.

// MediaDatingPhotosPath prefixes media-service's dating photo routes.
const MediaDatingPhotosPath = "/internal/v1/media/dating-photos/"

// HTTPMediaPhotoClient is the production MediaPhotoClient.
type HTTPMediaPhotoClient struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

// NewHTTPMediaPhotoClient builds the client. httpClient nil → a 30s timeout
// (prepare downloads and re-encodes the original).
func NewHTTPMediaPhotoClient(baseURL, internalKey string, httpClient *http.Client) *HTTPMediaPhotoClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPMediaPhotoClient{baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey, client: httpClient}
}

func (c *HTTPMediaPhotoClient) call(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("%w: encode: %v", ErrPhotoMediaUnavailable, err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: build request: %v", ErrPhotoMediaUnavailable, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: media-service unreachable: %v", ErrPhotoMediaUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	return resp.StatusCode, raw, nil
}

func decodeMediaPhotoData[T any](raw []byte) (*T, error) {
	var env struct {
		Data *T `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Data == nil {
		return nil, fmt.Errorf("%w: undecodable media-service response", ErrPhotoMediaUnavailable)
	}
	return env.Data, nil
}

func (c *HTTPMediaPhotoClient) status(mediaID uuid.UUID, code int, raw []byte) (*MediaPhotoStatus, error) {
	switch code {
	case http.StatusOK:
		st, err := decodeMediaPhotoData[MediaPhotoStatus](raw)
		if err != nil {
			return nil, err
		}
		if st.MediaID != mediaID {
			return nil, fmt.Errorf("%w: response names a different media", ErrPhotoMediaUnavailable)
		}
		return st, nil
	case http.StatusNotFound:
		return nil, ErrPhotoMediaNotFound
	case http.StatusConflict:
		return nil, ErrPhotoMediaNotReady
	case http.StatusUnprocessableEntity:
		return nil, ErrPhotoMediaUnsupported
	}
	return nil, fmt.Errorf("%w: media-service status %d", ErrPhotoMediaUnavailable, code)
}

// PhotoOwnerStatus implements MediaPhotoClient.
func (c *HTTPMediaPhotoClient) PhotoOwnerStatus(ctx context.Context, mediaID, requester uuid.UUID) (*MediaPhotoStatus, error) {
	if mediaID == uuid.Nil || requester == uuid.Nil {
		return nil, fmt.Errorf("invalid: media and requester ids required")
	}
	code, raw, err := c.call(ctx, http.MethodGet,
		MediaDatingPhotosPath+mediaID.String()+"/owner-status?requester_user_id="+url.QueryEscape(requester.String()), nil)
	if err != nil {
		return nil, err
	}
	return c.status(mediaID, code, raw)
}

// PreparePhoto implements MediaPhotoClient.
func (c *HTTPMediaPhotoClient) PreparePhoto(ctx context.Context, mediaID, requester uuid.UUID, detectFaces bool) (*MediaPhotoStatus, error) {
	if mediaID == uuid.Nil || requester == uuid.Nil {
		return nil, fmt.Errorf("invalid: media and requester ids required")
	}
	code, raw, err := c.call(ctx, http.MethodPost, MediaDatingPhotosPath+mediaID.String()+"/prepare",
		map[string]any{"requester_user_id": requester.String(), "detect_faces": detectFaces})
	if err != nil {
		return nil, err
	}
	return c.status(mediaID, code, raw)
}

// PhotoDeliveryURL implements MediaPhotoClient.
func (c *HTTPMediaPhotoClient) PhotoDeliveryURL(ctx context.Context, mediaID, owner uuid.UUID, variant string) (string, error) {
	if mediaID == uuid.Nil || owner == uuid.Nil {
		return "", fmt.Errorf("invalid: media and owner ids required")
	}
	code, raw, err := c.call(ctx, http.MethodPost, MediaDatingPhotosPath+mediaID.String()+"/delivery-url",
		map[string]string{"owner_user_id": owner.String(), "variant": variant})
	if err != nil {
		return "", err
	}
	switch code {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", ErrPhotoMediaNotFound
	case http.StatusConflict:
		return "", ErrPhotoMediaNotReady
	default:
		return "", fmt.Errorf("%w: media-service status %d", ErrPhotoMediaUnavailable, code)
	}
	out, err := decodeMediaPhotoData[struct {
		URL string `json:"url"`
	}](raw)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(out.URL, "https://") && !strings.HasPrefix(out.URL, "http://") {
		return "", fmt.Errorf("%w: delivery URL is not absolute", ErrPhotoMediaUnavailable)
	}
	return out.URL, nil
}

// DeletePhotoMedia implements MediaPhotoClient.
func (c *HTTPMediaPhotoClient) DeletePhotoMedia(ctx context.Context, mediaID, owner uuid.UUID) error {
	if mediaID == uuid.Nil || owner == uuid.Nil {
		return fmt.Errorf("invalid: media and owner ids required")
	}
	code, _, err := c.call(ctx, http.MethodDelete,
		MediaDatingPhotosPath+mediaID.String()+"?requester_user_id="+url.QueryEscape(owner.String()), nil)
	if err != nil {
		return err
	}
	switch code {
	case http.StatusOK, http.StatusNotFound:
		return nil
	case http.StatusConflict:
		slog.Warn("dating photo delete: media still referenced elsewhere; asset kept", "media_id", mediaID, "owner_id", owner)
		return nil
	}
	return fmt.Errorf("%w: media-service status %d", ErrPhotoMediaUnavailable, code)
}
