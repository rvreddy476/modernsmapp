package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// DatingReportEntityType is about_entity_type for a grievance opened from a
// dating report.
const DatingReportEntityType = "dating_report"

// UpsertDatingReportGrievance stores g as the grievance for dating report
// reportID unless one already exists (uq_grievances_dating_report), and
// returns the stored grievance and whether this call created it. A retry
// from dating-service therefore always gets the first grievance and its
// original due_at.
func (s *ReportStore) UpsertDatingReportGrievance(ctx context.Context, g *Grievance, reportID uuid.UUID) (*Grievance, bool, error) {
	if reportID == uuid.Nil {
		return nil, false, fmt.Errorf("report id required")
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO trust.grievances (id, complainant_id, subject, about_entity_type,
			about_entity_id, description, status, due_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'open', $7)
		ON CONFLICT (about_entity_id) WHERE about_entity_type = 'dating_report' DO NOTHING
	`, g.ID, g.ComplainantID, g.Subject, DatingReportEntityType, reportID, g.Description, g.DueAt)
	if err != nil {
		return nil, false, fmt.Errorf("insert dating report grievance: %w", err)
	}
	stored, err := scanGrievance(s.db.QueryRow(ctx,
		`SELECT `+grievanceCols+` FROM trust.grievances
		 WHERE about_entity_type = 'dating_report' AND about_entity_id = $1`, reportID))
	if err != nil {
		return nil, false, fmt.Errorf("load dating report grievance: %w", err)
	}
	return stored, tag.RowsAffected() == 1, nil
}
