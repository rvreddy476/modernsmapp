package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Flag struct {
	Key              string          `json:"key"`
	Enabled          bool            `json:"enabled"`
	RolloutPct       int             `json:"rollout_pct"`
	TargetUserIDs    []string        `json:"target_user_ids"`
	Payload          json.RawMessage `json:"payload"`
	UpdatedAt        time.Time       `json:"updated_at"`
	ExperimentName   string          `json:"experiment_name"`
	Hypothesis       string          `json:"hypothesis"`
	StartDate        *time.Time      `json:"start_date"`
	EndDate          *time.Time      `json:"end_date"`
	ControlGroupPct  int             `json:"control_group_pct"`
	TreatmentGroupPct int            `json:"treatment_group_pct"`
}

// FlagAuditEntry represents a single audit log record for a flag change.
type FlagAuditEntry struct {
	FlagKey   string    `json:"flag_key"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	OldValue  []byte    `json:"old_value"`
	NewValue  []byte    `json:"new_value"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

func (s *Store) GetFlag(ctx context.Context, key string) (*Flag, error) {
	query := `
		SELECT key, enabled, rollout_pct, target_user_ids, payload, updated_at,
		       COALESCE(experiment_name, ''), COALESCE(hypothesis, ''),
		       start_date, end_date,
		       COALESCE(control_group_pct, 0), COALESCE(treatment_group_pct, 0)
		FROM flags.flags
		WHERE key = $1
	`
	var f Flag
	err := s.db.QueryRow(ctx, query, key).Scan(
		&f.Key, &f.Enabled, &f.RolloutPct, &f.TargetUserIDs, &f.Payload, &f.UpdatedAt,
		&f.ExperimentName, &f.Hypothesis, &f.StartDate, &f.EndDate,
		&f.ControlGroupPct, &f.TreatmentGroupPct,
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
		INSERT INTO flags.flags (key, enabled, rollout_pct, target_user_ids, payload, updated_at,
		                         experiment_name, hypothesis, start_date, end_date,
		                         control_group_pct, treatment_group_pct)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (key) DO UPDATE
		SET enabled = EXCLUDED.enabled,
		    rollout_pct = EXCLUDED.rollout_pct,
		    target_user_ids = EXCLUDED.target_user_ids,
		    payload = EXCLUDED.payload,
		    updated_at = EXCLUDED.updated_at,
		    experiment_name = EXCLUDED.experiment_name,
		    hypothesis = EXCLUDED.hypothesis,
		    start_date = EXCLUDED.start_date,
		    end_date = EXCLUDED.end_date,
		    control_group_pct = EXCLUDED.control_group_pct,
		    treatment_group_pct = EXCLUDED.treatment_group_pct
	`
	_, err := s.db.Exec(ctx, query,
		flag.Key,
		flag.Enabled,
		flag.RolloutPct,
		flag.TargetUserIDs,
		pBytes,
		time.Now(),
		flag.ExperimentName,
		flag.Hypothesis,
		flag.StartDate,
		flag.EndDate,
		flag.ControlGroupPct,
		flag.TreatmentGroupPct,
	)
	return err
}

func (s *Store) ListFlags(ctx context.Context) ([]Flag, error) {
	query := `
		SELECT key, enabled, rollout_pct, target_user_ids, payload, updated_at,
		       COALESCE(experiment_name, ''), COALESCE(hypothesis, ''),
		       start_date, end_date,
		       COALESCE(control_group_pct, 0), COALESCE(treatment_group_pct, 0)
		FROM flags.flags
	`
	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var flags []Flag
	for rows.Next() {
		var f Flag
		if err := rows.Scan(
			&f.Key, &f.Enabled, &f.RolloutPct, &f.TargetUserIDs, &f.Payload, &f.UpdatedAt,
			&f.ExperimentName, &f.Hypothesis, &f.StartDate, &f.EndDate,
			&f.ControlGroupPct, &f.TreatmentGroupPct,
		); err != nil {
			return nil, err
		}
		flags = append(flags, f)
	}
	return flags, nil
}

// InsertAuditLog records a flag change in the audit log table.
func (s *Store) InsertAuditLog(ctx context.Context, entry FlagAuditEntry) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO flags.flag_audit_log (flag_key, actor, action, old_value, new_value)
		VALUES ($1, $2, $3, $4, $5)
	`, entry.FlagKey, entry.Actor, entry.Action, entry.OldValue, entry.NewValue)
	return err
}

// GetAuditLog retrieves paginated audit log entries for a given flag key.
func (s *Store) GetAuditLog(ctx context.Context, flagKey string, limit, offset int) ([]FlagAuditEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT flag_key, actor, action, old_value, new_value, created_at
		FROM flags.flag_audit_log
		WHERE flag_key = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, flagKey, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []FlagAuditEntry
	for rows.Next() {
		var e FlagAuditEntry
		if err := rows.Scan(&e.FlagKey, &e.Actor, &e.Action, &e.OldValue, &e.NewValue, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// InsertConversion records an A/B experiment conversion event.
func (s *Store) InsertConversion(ctx context.Context, flagKey, userID, variant, eventType string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO flags.experiment_conversions (flag_key, user_id, variant, event_type)
		VALUES ($1, $2, $3, $4)
	`, flagKey, userID, variant, eventType)
	return err
}

// CountConversionsByVariant returns conversion counts grouped by variant for a given flag.
func (s *Store) CountConversionsByVariant(ctx context.Context, flagKey string) (map[string]int64, error) {
	rows, err := s.db.Query(ctx, `
		SELECT variant, COUNT(*) FROM flags.experiment_conversions
		WHERE flag_key = $1 GROUP BY variant
	`, flagKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var variant string
		var count int64
		if err := rows.Scan(&variant, &count); err != nil {
			return nil, err
		}
		result[variant] = count
	}
	return result, nil
}

// CountEvaluations returns the total number of evaluations recorded for a given flag.
// Since evaluations are emitted as Kafka events rather than stored in the DB,
// we return the total conversion count as a proxy measure here.
func (s *Store) CountEvaluations(ctx context.Context, flagKey string) (int64, error) {
	var count int64
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM flags.experiment_conversions
		WHERE flag_key = $1
	`, flagKey).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}
