package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// OpsAlert is one row of notify_meta.ops_alerts. Detail must never carry a
// user id or a location.
type OpsAlert struct {
	Source    string
	Kind      string
	Severity  string // info | warning | critical
	SubjectID uuid.UUID
	DedupeKey string
	Detail    map[string]any
}

// InsertOpsAlert records an operator alert. A repeated DedupeKey is a no-op;
// the bool reports whether this call inserted the row.
func (s *Store) InsertOpsAlert(ctx context.Context, a OpsAlert) (bool, error) {
	detail := []byte(`{}`)
	if len(a.Detail) > 0 {
		b, err := json.Marshal(a.Detail)
		if err != nil {
			return false, fmt.Errorf("encode ops alert detail: %w", err)
		}
		detail = b
	}
	var subject any
	if a.SubjectID != uuid.Nil {
		subject = a.SubjectID
	}
	var dedupe any
	if a.DedupeKey != "" {
		dedupe = a.DedupeKey
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO notify_meta.ops_alerts (source, kind, severity, subject_id, dedupe_key, detail)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		ON CONFLICT (dedupe_key) DO NOTHING`,
		a.Source, a.Kind, a.Severity, subject, dedupe, detail)
	if err != nil {
		return false, fmt.Errorf("insert ops alert: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
