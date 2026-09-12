package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
)

// Tube channels (2026-09-05). post-service owns channels (one per account);
// the feed attaches the card-sized channel to every post whose author has
// one, resolved in ONE call per page alongside authors and media. Until
// 2026-09-12 only long_video rows carried it; the gate went so the reels
// overlay can offer Subscribe on a channel owner's flick (see
// HydratedPost.Channel).

// ChannelRef is the channel card carried by a feed item whose author has a
// channel.
type ChannelRef struct {
	UserID    uuid.UUID `json:"user_id"`
	Name      string    `json:"name"`
	Handle    string    `json:"handle"`
	AvatarURL *string   `json:"avatar_url"`
}

// fetchChannels asks post-service for the channels of a page of authors:
// GET /v1/channels/batch?user_ids=a,b as the viewer (avatar URLs go through
// the delivery gate). Authors without a channel are absent from the map.
func (s *Service) fetchChannels(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*ChannelRef, error) {
	result := make(map[uuid.UUID]*ChannelRef, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	const chunk = 100
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		parts := make([]string, 0, end-start)
		for _, id := range ids[start:end] {
			parts = append(parts, id.String())
		}
		url := strings.TrimRight(s.postServiceURL, "/") + "/v1/channels/batch?user_ids=" + strings.Join(parts, ",")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-User-Id", viewerID.String())
		if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}
		// Every page reaches here now (not only pages with a long video),
		// so a Service built without a post client, as the unit tests do,
		// must degrade to the best-effort path rather than nil-deref.
		// Same fallback as the graph client in privacyfilter.go.
		client := s.postClient
		if client == nil {
			client = http.DefaultClient
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Data map[string]*ChannelRef `json:"data"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("post-service channels batch returned %d", resp.StatusCode)
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("decode channels batch: %w", decodeErr)
		}
		for idStr, ref := range envelope.Data {
			id, err := uuid.Parse(idStr)
			if err != nil || ref == nil {
				continue
			}
			result[id] = ref
		}
	}
	return result, nil
}
