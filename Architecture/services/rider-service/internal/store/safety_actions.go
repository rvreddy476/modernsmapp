package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SafetyAction is an append-only operator response action.
type SafetyAction struct {
	ID         uuid.UUID       `json:"id"`
	IncidentID uuid.UUID       `json:"incident_id"`
	ActorID    uuid.UUID       `json:"actor_id"`
	ActorRole  string          `json:"actor_role"`
	Action     string          `json:"action"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  time.Time       `json:"created_at"`
}

// RecordSafetyAction inserts an append-only action into rider_safety_actions.
func (s *Store) RecordSafetyAction(ctx context.Context, incidentID, actorID uuid.UUID, role, action string, metadata map[string]any) error {
	metaJSON, _ := json.Marshal(metadata)
	if len(metaJSON) == 0 {
		metaJSON = []byte("{}")
	}
	const q = `
        INSERT INTO rider_safety_actions (incident_id, actor_id, actor_role, action, metadata)
        VALUES ($1, $2, $3, $4, $5)`
	_, err := s.db.Exec(ctx, q, incidentID, actorID, role, action, metaJSON)
	if err != nil {
		return fmt.Errorf("record safety action: %w", err)
	}
	return nil
}
