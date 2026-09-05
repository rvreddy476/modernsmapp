package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Chat-app pass (2026-09-05): community suggestions.
//
// Communities are broadcast channels (channel-service) and live in the same
// app database this service already reads the graph from, so the candidate
// query is local: public, active channels the viewer has NOT joined, ordered
// by subscriber count then recency. Block safety (owner blocked either way)
// is applied at egress by the service through the canonical graph lookup.

// CommunityCandidate is one suggested community row.
type CommunityCandidate struct {
	ID              uuid.UUID  `json:"id"`
	OwnerID         uuid.UUID  `json:"owner_id"`
	Handle          string     `json:"handle"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	AvatarMediaID   *uuid.UUID `json:"avatar_media_id,omitempty"`
	Category        string     `json:"category"`
	IsVerified      bool       `json:"is_verified"`
	SubscriberCount int64      `json:"subscriber_count"`
	UpdateCount     int64      `json:"update_count"`
	CreatedAt       time.Time  `json:"created_at"`
}

// GetCommunityCandidates returns up to limit public channels the viewer is
// not a member of (any role, banned included — a banned user is never
// re-suggested the channel).
func (s *Store) GetCommunityCandidates(ctx context.Context, viewerID uuid.UUID, limit int) ([]CommunityCandidate, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := s.appDB.Query(ctx, `
		SELECT bc.id, bc.owner_id, bc.handle, bc.name, bc.description, bc.avatar_media_id,
		       bc.category, bc.is_verified, bc.subscriber_count, bc.update_count, bc.created_at
		FROM broadcast_channels bc
		WHERE bc.status = 'active'
		  AND bc.channel_type IN ('public','creator','brand','education','official','topic')
		  AND bc.owner_id <> $1
		  AND NOT EXISTS (
		      SELECT 1 FROM channel_members cm WHERE cm.channel_id = bc.id AND cm.user_id = $1
		  )
		ORDER BY bc.subscriber_count DESC, bc.created_at DESC
		LIMIT $2
	`, viewerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommunityCandidate{}
	for rows.Next() {
		var c CommunityCandidate
		if err := rows.Scan(&c.ID, &c.OwnerID, &c.Handle, &c.Name, &c.Description, &c.AvatarMediaID,
			&c.Category, &c.IsVerified, &c.SubscriberCount, &c.UpdateCount, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
