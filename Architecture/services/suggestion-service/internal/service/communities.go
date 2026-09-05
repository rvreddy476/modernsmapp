package service

import (
	"context"
	"log"
	"time"

	"github.com/atpost/suggestion-service/internal/store"
	"github.com/google/uuid"
)

// Chat-app pass (2026-09-05): GET /v1/suggestions/communities.
//
// Communities the viewer has not joined, ordered by member count then
// recency. Reason codes: POPULAR (≥ 10 members), NEW (created in the last
// 14 days), ACTIVE (has updates); every row carries at least one.
//
// Block safety is the same fail-closed egress filter every people surface
// uses (SR-7): a community whose OWNER is blocked by / has blocked the viewer
// is never shown, and with the block set unavailable the list is empty.

const (
	communityPopularThreshold = 10
	communityNewWindow        = 14 * 24 * time.Hour
)

// CommunitySuggestion is the API-facing community row.
type CommunitySuggestion struct {
	EntityType    string   `json:"entityType"`
	CommunityID   string   `json:"community_id"`
	OwnerID       string   `json:"owner_id"`
	Handle        string   `json:"handle"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	AvatarMediaID *string  `json:"avatar_media_id,omitempty"`
	Category      string   `json:"category,omitempty"`
	IsVerified    bool     `json:"is_verified"`
	MemberCount   int64    `json:"member_count"`
	UpdateCount   int64    `json:"update_count"`
	ReasonCodes   []string `json:"reason_codes"`
	ExplainText   string   `json:"explain_text"`
	CreatedAt     string   `json:"created_at"`
	// JoinPath is the call Android makes to join: POST {gateway}{join_path}.
	JoinPath string `json:"join_path"`
}

// CommunitySuggestionsResponse is the payload of GET /v1/suggestions/communities.
type CommunitySuggestionsResponse struct {
	Type        string                `json:"type"`
	Items       []CommunitySuggestion `json:"items"`
	GeneratedAt string                `json:"generated_at"`
}

func communityReasons(c store.CommunityCandidate, now time.Time) ([]string, string) {
	var codes []string
	explain := ""
	if c.SubscriberCount >= communityPopularThreshold {
		codes = append(codes, "POPULAR")
		explain = "Popular community"
	}
	if now.Sub(c.CreatedAt) <= communityNewWindow {
		codes = append(codes, "NEW")
		if explain == "" {
			explain = "New community"
		}
	}
	if c.UpdateCount > 0 {
		codes = append(codes, "ACTIVE")
		if explain == "" {
			explain = "Posting regularly"
		}
	}
	if len(codes) == 0 {
		codes = []string{"DISCOVER"}
		explain = "Community you might like"
	}
	return codes, explain
}

// GetCommunitySuggestions lists public communities the viewer has not joined.
func (s *Service) GetCommunitySuggestions(ctx context.Context, viewerID uuid.UUID, limit int) (*CommunitySuggestionsResponse, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	now := time.Now().UTC()
	resp := &CommunitySuggestionsResponse{
		Type:        "community",
		Items:       []CommunitySuggestion{},
		GeneratedAt: now.Format(time.RFC3339),
	}

	// Fail closed on block safety, exactly like the people surfaces.
	if s.blocks == nil {
		log.Printf("[suggestions] BLOCK LOOKUP NOT CONFIGURED — returning no community suggestions")
		return resp, nil
	}
	blocked, err := s.blocks.BlockedSet(ctx, viewerID)
	if err != nil {
		log.Printf("[suggestions] block lookup failed for viewer %s: %v — returning no community suggestions", viewerID, err)
		return resp, nil
	}

	// Over-fetch so owner-blocked rows do not shrink the page.
	candidates, err := s.store.GetCommunityCandidates(ctx, viewerID, limit*2)
	if err != nil {
		return nil, err
	}
	for _, c := range candidates {
		if _, isBlocked := blocked[c.OwnerID]; isBlocked {
			continue
		}
		codes, explain := communityReasons(c, now)
		item := CommunitySuggestion{
			EntityType:  "community",
			CommunityID: c.ID.String(),
			OwnerID:     c.OwnerID.String(),
			Handle:      c.Handle,
			Name:        c.Name,
			Description: c.Description,
			Category:    c.Category,
			IsVerified:  c.IsVerified,
			MemberCount: c.SubscriberCount,
			UpdateCount: c.UpdateCount,
			ReasonCodes: codes,
			ExplainText: explain,
			CreatedAt:   c.CreatedAt.UTC().Format(time.RFC3339),
			JoinPath:    "/v1/broadcast-channels/" + c.ID.String() + "/subscribe",
		}
		if c.AvatarMediaID != nil {
			aid := c.AvatarMediaID.String()
			item.AvatarMediaID = &aid
		}
		resp.Items = append(resp.Items, item)
		if len(resp.Items) >= limit {
			break
		}
	}
	return resp, nil
}
