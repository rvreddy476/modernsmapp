package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/notification-service/internal/store/scylla"
)

// SetProfileServiceURL configures the identity-profile base URL used to
// hydrate inbox actors. Empty disables hydration (rows go out with
// actor_user_id only, as before).
func (s *Service) SetProfileServiceURL(url string) {
	s.profileServiceURL = url
}

// The wire shape of one profile in identity-profile's batch response — the
// same contract feed-service and post-service consume.
type actorProfile struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

const actorHydrationTimeout = 3 * time.Second

// HydrateActors fills each inbox row's Actor from identity-profile's batch
// endpoint. BEST-EFFORT: a profile-service blip must not take down the
// inbox, so on any failure the rows ship exactly as before — actor id only —
// and the client renders its generic fallback. A profile the batch does not
// return (deleted account) stays nil for the same reason: "Someone" is the
// honest rendering for an actor that no longer exists.
func (s *Service) HydrateActors(ctx context.Context, items []scylla.Notification) {
	if s.profileServiceURL == "" || len(items) == 0 {
		return
	}

	seen := make(map[uuid.UUID]bool)
	var ids []string
	for i := range items {
		id := items[i].ActorUserID
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			ids = append(ids, id.String())
		}
	}
	if len(ids) == 0 {
		return
	}

	profiles, err := s.fetchActorProfiles(ctx, ids)
	if err != nil {
		log.Printf("notification actor hydration skipped: %v", err)
		return
	}

	for i := range items {
		id := items[i].ActorUserID
		if p, ok := profiles[id]; ok {
			items[i].Actor = &scylla.NotificationActor{
				ID:          id,
				Username:    p.Username,
				DisplayName: p.DisplayName,
			}
		}
	}
}

func (s *Service) fetchActorProfiles(
	ctx context.Context,
	ids []string,
) (map[uuid.UUID]actorProfile, error) {
	ctx, cancel := context.WithTimeout(ctx, actorHydrationTimeout)
	defer cancel()

	body, err := json.Marshal(map[string]any{"user_ids": ids})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.profileServiceURL, "/")+"/v1/profiles/batch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}

	client := s.profileClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("identity-profile returned %d: %s", resp.StatusCode, string(b))
	}

	profiles := make(map[uuid.UUID]actorProfile)
	if err := json.NewDecoder(resp.Body).Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decode profile batch: %w", err)
	}
	return profiles, nil
}
