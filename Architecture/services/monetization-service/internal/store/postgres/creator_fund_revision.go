package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// RevisionAdjustment is one creator_fund_revision_adjustments row
// (migration 026): an accrued, final day whose analytics inputs moved
// after it was priced. It records the difference; it moves no money.
type RevisionAdjustment struct {
	ID                      uuid.UUID `json:"id"`
	EarningID               uuid.UUID `json:"earning_id"`
	CreatorID               uuid.UUID `json:"creator_id"`
	DayBucket               time.Time `json:"day_bucket"`
	ContentType             string    `json:"content_type"`
	RegionCode              string    `json:"region_code"`
	RecordedRevision        string    `json:"recorded_revision"`
	ObservedRevision        string    `json:"observed_revision"`
	RecordedViews           int64     `json:"recorded_views"`
	ObservedViews           int64     `json:"observed_views"`
	RecordedGrossMicroPaise int64     `json:"recorded_gross_micro_paise"`
	ObservedGrossMicroPaise int64     `json:"observed_gross_micro_paise"`
	DeltaGrossMicroPaise    int64     `json:"delta_gross_micro_paise"`
	Status                  string    `json:"status"`
}

// RecordRevisionAdjustment inserts the row once per (earning, observed
// revision). inserted is false when the same drift was already recorded.
func (s *Store) RecordRevisionAdjustment(ctx context.Context, a *RevisionAdjustment) (inserted bool, err error) {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO creator_fund_revision_adjustments (
			id, earning_id, creator_id, day_bucket, content_type, region_code,
			recorded_revision, observed_revision, recorded_views, observed_views,
			recorded_gross_micro_paise, observed_gross_micro_paise, delta_gross_micro_paise
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (earning_id, observed_revision) DO NOTHING
	`, a.ID, a.EarningID, a.CreatorID, a.DayBucket, a.ContentType, a.RegionCode,
		a.RecordedRevision, a.ObservedRevision, a.RecordedViews, a.ObservedViews,
		a.RecordedGrossMicroPaise, a.ObservedGrossMicroPaise, a.DeltaGrossMicroPaise)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// PendingSettlement names a stored period settlement that still owes money
// (pending_paise > 0) to a creator whose fund participation is not
// suspended: a held period whose hold has been released, or a claim that
// partly failed. The release sweep settles each again.
type PendingSettlement struct {
	CreatorID uuid.UUID
	PeriodKey string
}

// ListReleasableSettlements returns stored settlements with money still
// pending whose creator is not suspended, oldest period first.
func (s *Store) ListReleasableSettlements(ctx context.Context, regionCode string, limit int) ([]PendingSettlement, error) {
	rows, err := s.db.Query(ctx, `
		SELECT ps.creator_id, ps.period_key
		FROM creator_fund_period_settlements ps
		LEFT JOIN creator_fund_eligibility e ON e.creator_id = ps.creator_id
		WHERE ps.region_code = $1
		  AND ps.pending_paise > 0
		  AND COALESCE(e.status, '') <> 'suspended'
		ORDER BY ps.period_start, ps.creator_id
		LIMIT $2
	`, regionCode, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingSettlement
	for rows.Next() {
		var p PendingSettlement
		if err := rows.Scan(&p.CreatorID, &p.PeriodKey); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
