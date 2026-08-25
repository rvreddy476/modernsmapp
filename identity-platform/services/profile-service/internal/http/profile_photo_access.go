package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProfilePhotoAccessChecker resolves the enforceable profile-photo audience.
// Anonymous viewers are strangers; authenticated viewers are resolved by the
// canonical graph permission matrix.
type ProfilePhotoAccessChecker interface {
	CanViewProfilePhoto(ctx context.Context, viewerID, ownerID uuid.UUID) (bool, error)
}

type profilePhotoBatchAccessChecker interface {
	CanViewProfilePhotos(ctx context.Context, viewerID uuid.UUID, ownerIDs []uuid.UUID) (map[uuid.UUID]bool, error)
}

type profilePhotoAccessClient struct {
	graphURL    string
	userURL     string
	internalKey string
	client      *http.Client
}

func NewProfilePhotoAccessChecker(graphURL, userURL, internalKey string) ProfilePhotoAccessChecker {
	return &profilePhotoAccessClient{
		graphURL: strings.TrimRight(graphURL, "/"),
		userURL:  strings.TrimRight(userURL, "/"), internalKey: internalKey,
		client: &http.Client{Timeout: 3 * time.Second},
	}
}

func (p *profilePhotoAccessClient) CanViewProfilePhoto(
	ctx context.Context,
	viewerID, ownerID uuid.UUID,
) (bool, error) {
	if ownerID == uuid.Nil {
		return false, fmt.Errorf("profile photo owner is missing")
	}
	if viewerID == ownerID {
		return true, nil
	}
	if viewerID == uuid.Nil {
		return p.anonymousCanView(ctx, ownerID)
	}
	return p.authenticatedCanView(ctx, viewerID, ownerID)
}

func (p *profilePhotoAccessClient) anonymousCanView(ctx context.Context, ownerID uuid.UUID) (bool, error) {
	if p.userURL == "" {
		return false, fmt.Errorf("identity user-service is not configured")
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/v1/users/%s/settings", p.userURL, ownerID),
		nil,
	)
	if err != nil {
		return false, err
	}
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("read profile-photo privacy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("profile-photo privacy returned %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Audience string `json:"who_can_see_profile_photo"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("decode profile-photo privacy: %w", err)
	}
	return body.Data.Audience == "everyone", nil
}

func (p *profilePhotoAccessClient) authenticatedCanView(
	ctx context.Context,
	viewerID, ownerID uuid.UUID,
) (bool, error) {
	if p.graphURL == "" {
		return false, fmt.Errorf("graph-service is not configured")
	}
	endpoint, err := url.Parse(p.graphURL + "/v1/permissions/check")
	if err != nil {
		return false, err
	}
	query := endpoint.Query()
	query.Set("target_user_id", ownerID.String())
	query.Set("actions", "view_profile")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return false, err
	}
	p.authorize(req)
	req.Header.Set("X-User-Id", viewerID.String())
	resp, err := p.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("resolve profile-photo permission: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("profile-photo permission returned %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Decisions map[string]struct {
				Allowed bool `json:"allowed"`
			} `json:"decisions"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("decode profile-photo permission: %w", err)
	}
	decision, ok := body.Data.Decisions["view_profile"]
	if !ok {
		return false, fmt.Errorf("profile-photo permission omitted view_profile")
	}
	return decision.Allowed, nil
}

func (p *profilePhotoAccessClient) CanViewProfilePhotos(
	ctx context.Context,
	viewerID uuid.UUID,
	ownerIDs []uuid.UUID,
) (map[uuid.UUID]bool, error) {
	allowed := make(map[uuid.UUID]bool, len(ownerIDs))
	if viewerID == uuid.Nil {
		// Anonymous discovery is deliberately redacted in bulk. A direct profile
		// read still resolves the owner's `everyone` policy exactly; silently
		// making N privacy-service calls for one list page does not scale.
		return allowed, nil
	}
	targets := make([]string, 0, len(ownerIDs))
	for _, ownerID := range ownerIDs {
		if ownerID == viewerID {
			allowed[ownerID] = true
			continue
		}
		targets = append(targets, ownerID.String())
	}
	if len(targets) == 0 {
		return allowed, nil
	}
	if len(targets) > 50 {
		return nil, fmt.Errorf("profile-photo batch exceeds 50 targets")
	}
	body, err := json.Marshal(map[string]any{
		"target_user_ids": targets,
		"actions":         []string{"view_profile"},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		p.graphURL+"/v1/permissions/check-batch",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	p.authorize(req)
	req.Header.Set("X-User-Id", viewerID.String())
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("resolve profile-photo batch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("profile-photo batch returned %d", resp.StatusCode)
	}
	var response struct {
		Data struct {
			Results map[string]map[string]struct {
				Allowed bool `json:"allowed"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode profile-photo batch: %w", err)
	}
	for _, ownerID := range ownerIDs {
		if decision, ok := response.Data.Results[ownerID.String()]["view_profile"]; ok {
			allowed[ownerID] = decision.Allowed
		}
	}
	return allowed, nil
}

func (p *profilePhotoAccessClient) authorize(req *http.Request) {
	if p.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", p.internalKey)
	}
}
