package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DatingReportEntityType is about_entity_type for a grievance opened from a
// dating report.
const DatingReportEntityType = "dating_report"

// UpsertDatingReportGrievance stores g as the grievance for dating report
// reportID unless one already exists (uq_grievances_dating_report), and
// returns the stored grievance and whether this call created it. A retry
// from dating-service therefore always gets the first grievance and its
// original due_at.
//
// A newly created grievance writes one audit row with meta's actor (the
// named calling service) in the same transaction; a retry writes none.
func (s *ReportStore) UpsertDatingReportGrievance(ctx context.Context, g *Grievance, reportID uuid.UUID, meta AuditMeta) (*Grievance, bool, error) {
	if reportID == uuid.Nil {
		return nil, false, fmt.Errorf("report id required")
	}
	if err := meta.Actor.Validate(); err != nil {
		return nil, false, err
	}
	var stored *Grievance
	created := false
	err := withTx(ctx, s.db.Begin, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO trust.grievances (id, complainant_id, subject, about_entity_type,
				about_entity_id, description, status, due_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'open', $7)
			ON CONFLICT (about_entity_id) WHERE about_entity_type = 'dating_report' DO NOTHING
		`, g.ID, g.ComplainantID, g.Subject, DatingReportEntityType, reportID, g.Description, g.DueAt)
		if err != nil {
			return fmt.Errorf("insert dating report grievance: %w", err)
		}
		stored, err = scanGrievance(tx.QueryRow(ctx,
			`SELECT `+grievanceCols+` FROM trust.grievances
			 WHERE about_entity_type = 'dating_report' AND about_entity_id = $1`, reportID))
		if err != nil {
			return fmt.Errorf("load dating report grievance: %w", err)
		}
		created = tag.RowsAffected() == 1
		if !created {
			return nil
		}
		return insertAudit(ctx, tx, meta, auditChange{
			Action:     "grievance.created",
			TargetType: AuditTargetGrievance,
			TargetID:   stored.ID,
			NewStatus:  stored.Status,
		})
	})
	if err != nil {
		return nil, false, err
	}
	return stored, created, nil
}
