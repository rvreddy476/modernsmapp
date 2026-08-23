package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProfileMediaAuthority verifies a proposed avatar/cover reference at the
// canonical media owner.  It is deliberately an interface so handler tests
// can prove fail-closed behaviour without replacing the production client.
type ProfileMediaAuthority interface {
	RequireAttachable(ctx context.Context, ownerID, mediaID uuid.UUID, subtype string) error
}

type mediaAuthorityClient struct {
	baseURL     string
	internalKey string
	httpClient  *http.Client
}

func NewMediaAuthorityClient(baseURL, internalKey string) ProfileMediaAuthority {
	return &mediaAuthorityClient{
		baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey,
		httpClient: &http.Client{Timeout: 4 * time.Second},
	}
}

func (c *mediaAuthorityClient) RequireAttachable(ctx context.Context, ownerID, mediaID uuid.UUID, subtype string) error {
	endpoint := fmt.Sprintf("%s/v1/media/internal/%s/profile-authority?owner_id=%s&subtype=%s",
		c.baseURL, mediaID, ownerID, url.QueryEscape(subtype))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("media authority unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("media authority refused reference: status %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			MediaID    uuid.UUID `json:"media_id"`
			OwnerID    uuid.UUID `json:"owner_id"`
			Subtype    string    `json:"media_subtype"`
			Attachable bool      `json:"attachable"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode media authority: %w", err)
	}
	if !envelope.Data.Attachable || envelope.Data.MediaID != mediaID ||
		envelope.Data.OwnerID != ownerID || envelope.Data.Subtype != subtype {
		return fmt.Errorf("media authority returned a mismatched or negative verdict")
	}
	return nil
}
