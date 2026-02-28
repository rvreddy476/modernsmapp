package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditLog struct {
	ID         uuid.UUID       `json:"id"`
	AdminActor string          `json:"admin_actor"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

func (s *Store) LogAction(ctx context.Context, actor, action, entityType, entityID string, payload interface{}) error {
	pBytes, _ := json.Marshal(payload)

	query := `
		INSERT INTO admin.audit_log (id, admin_actor, action, entity_type, entity_id, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := s.db.Exec(ctx, query,
		uuid.New(),
		actor,
		action,
		entityType,
		entityID,
		pBytes,
		time.Now(),
	)
	return err
}
