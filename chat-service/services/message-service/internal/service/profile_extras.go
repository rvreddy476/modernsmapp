package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// fetchProfileWithExtras reads one user from the user directory
// (GET /v1/users/:id) with the two fields chat parity caches beside the
// profile: username (DM peer, search) and created_at (the "new account"
// request signal, CONTRACT §7).
func (s *Service) fetchProfileWithExtras(ctx context.Context, userID uuid.UUID) (postgres.UserProfile, postgres.ProfileExtras, bool) {
	if s.userServiceURL == "" || s.httpClient == nil {
		return postgres.UserProfile{}, postgres.ProfileExtras{}, false
	}
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("%s/v1/users/%s", s.userServiceURL, userID), nil)
	if err != nil {
		return postgres.UserProfile{}, postgres.ProfileExtras{}, false
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return postgres.UserProfile{}, postgres.ProfileExtras{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return postgres.UserProfile{}, postgres.ProfileExtras{}, false
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return postgres.UserProfile{}, postgres.ProfileExtras{}, false
	}
	var envelope struct {
		Data struct {
			DisplayName   string     `json:"display_name"`
			Username      *string    `json:"username"`
			AvatarMediaID *string    `json:"avatar_media_id"`
			CreatedAt     *time.Time `json:"created_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return postgres.UserProfile{}, postgres.ProfileExtras{}, false
	}
	d := envelope.Data
	name := strings.TrimSpace(d.DisplayName)
	if name == "" || d.CreatedAt == nil {
		return postgres.UserProfile{}, postgres.ProfileExtras{}, false
	}
	profile := postgres.UserProfile{UserID: userID, DisplayName: name}
	if d.AvatarMediaID != nil {
		if id, err := uuid.Parse(*d.AvatarMediaID); err == nil {
			profile.AvatarMediaID = &id
		}
	}
	extra := postgres.ProfileExtras{AccountCreatedAt: d.CreatedAt}
	if d.Username != nil {
		extra.Username = *d.Username
	}
	return profile, extra, true
}
