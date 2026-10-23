package eligibility

// Public profiles for the membership reads (23 Oct 2026): identity-profile's
// batch read, the route feed-service, post-service and live-service-v2
// already hydrate people from.
//
//   POST {PROFILE_SERVICE_URL}/v1/profiles/batch {"user_ids":[...]}
//   -> {"<uuid>":{"username":string|null,"display_name":string,"avatar_url":string|null,...}}
//
// A raw map, not an envelope. X-Internal-Service-Key and NO X-User-Id: the
// answer does not depend on who is asking. At most 100 ids per call. An id
// the map leaves out is an account identity does not show. Every failure is
// an error with fixed text and the status; a body is never quoted.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/google/uuid"
)

// ProfileBatchMax is the most ids one batch call carries.
const ProfileBatchMax = 100

var _ service.ProfileSource = (*Profiles)(nil)

// Profiles is service.ProfileSource over identity-profile.
type Profiles struct {
	baseURL, internalKey string
	http                 *http.Client
}

// NewProfiles returns nil when baseURL is empty (people are then ids only).
func NewProfiles(baseURL, internalKey string) *Profiles {
	if trimBase(baseURL) == "" {
		return nil
	}
	return &Profiles{baseURL: trimBase(baseURL), internalKey: internalKey, http: httpClient()}
}

// Profiles reads the public profiles of userIDs, ProfileBatchMax per call.
func (c *Profiles) Profiles(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]service.PublicProfile, error) {
	out := make(map[uuid.UUID]service.PublicProfile, len(userIDs))
	for start := 0; start < len(userIDs); start += ProfileBatchMax {
		end := start + ProfileBatchMax
		if end > len(userIDs) {
			end = len(userIDs)
		}
		ids := make([]string, 0, end-start)
		for _, id := range userIDs[start:end] {
			ids = append(ids, id.String())
		}
		page, err := c.batch(ctx, ids)
		if err != nil {
			return nil, err
		}
		for id, p := range page {
			if p == nil {
				continue
			}
			prof := service.PublicProfile{DisplayName: p.DisplayName}
			if p.Username != nil {
				prof.Username = *p.Username
			}
			if p.AvatarURL != nil {
				prof.AvatarURL = *p.AvatarURL
			}
			out[id] = prof
		}
	}
	return out, nil
}

type batchProfile struct {
	Username    *string `json:"username"`
	DisplayName string  `json:"display_name"`
	AvatarURL   *string `json:"avatar_url"`
}

func (c *Profiles) batch(ctx context.Context, ids []string) (map[uuid.UUID]*batchProfile, error) {
	body, err := json.Marshal(map[string]any{"user_ids": ids})
	if err != nil {
		return nil, errors.New("profiles batch: encode request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/profiles/batch", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("profiles batch: build request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Caller-Service", callerService)
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("profiles batch: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("profiles batch: status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, errors.New("profiles batch: read body")
	}
	var page map[uuid.UUID]*batchProfile
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, errors.New("profiles batch: malformed body")
	}
	return page, nil
}
