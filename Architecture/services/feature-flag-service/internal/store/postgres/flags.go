package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Flag struct {
	Key           string          `json:"key"`
	Enabled       bool            `json:"enabled"`
	RolloutPct    int             `json:"rollout_pct"`
	TargetUserIDs []string        `json:"target_user_ids"`
	Payload       json.RawMessage `json:"payload"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

func (s *Store) GetFlag(ctx context.Context, key string) (*Flag, error) {
	query := `
		SELECT key, enabled, rollout_pct, target_user_ids, payload, updated_at
		FROM flags.flags
		WHERE key = $1
	`
	var f Flag
	err := s.db.QueryRow(ctx, query, key).Scan(
		&f.Key, &f.Enabled, &f.RolloutPct, &f.TargetUserIDs, &f.Payload, &f.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil // Not found
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) UpsertFlag(ctx context.Context, flag *Flag) error {
	pBytes, _ := json.Marshal(flag.Payload)
	query := `
		INSERT INTO flags.flags (key, enabled, rollout_pct, target_user_ids, payload, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (key) DO UPDATE
		SET enabled = EXCLUDED.enabled,
		    rollout_pct = EXCLUDED.rollout_pct,
		    target_user_ids = EXCLUDED.target_user_ids,
		    payload = EXCLUDED.payload,
		    updated_at = EXCLUDED.updated_at
	`
	_, err := s.db.Exec(ctx, query,
		flag.Key,
		flag.Enabled,
		flag.RolloutPct,
		flag.TargetUserIDs,
		pBytes,
		time.Now(),
	)
	return err
}

func (s *Store) ListFlags(ctx context.Context) ([]Flag, error) {
	query := `SELECT key, enabled, rollout_pct, target_user_ids, payload, updated_at FROM flags.flags`
	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var flags []Flag
	for rows.Next() {
		var f Flag
		if err := rows.Scan(&f.Key, &f.Enabled, &f.RolloutPct, &f.TargetUserIDs, &f.Payload, &f.UpdatedAt); err != nil {
			return nil, err
		}
		flags = append(flags, f)
	}
	return flags, nil
}
