package postgres

import (
	"context"
	"time"
)

// AdminStats is the trust & safety dashboard's read-only counts. No
// personal data. "Open" means still needing work:
//
//	reports     open, reviewing
//	appeals     open, under_review
//	grievances  open, acknowledged
type AdminStats struct {
	// OpenReportsByStatus always carries both open statuses, zero included.
	OpenReportsByStatus map[string]int64 `json:"open_reports_by_status"`
	OpenReports         int64            `json:"open_reports"`
	OpenAppeals         int64            `json:"open_appeals"`
	OpenGrievances      int64            `json:"open_grievances"`
	// GrievancesOverdue: open grievances past due_at (the IT Rules 15-day
	// timer); the same set as GET /grievances?overdue=true.
	GrievancesOverdue int64 `json:"grievances_overdue"`
	// GrievancesDueSoon: open grievances not yet overdue but due within 48 h.
	GrievancesDueSoon int64     `json:"grievances_due_within_48h"`
	StrikesLast7Days  int64     `json:"strikes_last_7_days"`
	GeneratedAt       time.Time `json:"generated_at"`
}

// AdminStats reads every dashboard count in one statement, so the numbers
// share one snapshot.
func (s *ReportStore) AdminStats(ctx context.Context) (*AdminStats, error) {
	out := &AdminStats{OpenReportsByStatus: map[string]int64{}}
	var open, reviewing int64
	err := s.db.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM trust.reports WHERE status = 'open'),
			(SELECT count(*) FROM trust.reports WHERE status = 'reviewing'),
			(SELECT count(*) FROM trust.content_appeals WHERE status IN ('open', 'under_review')),
			(SELECT count(*) FROM trust.grievances WHERE status IN ('open', 'acknowledged')),
			(SELECT count(*) FROM trust.grievances
			  WHERE status IN ('open', 'acknowledged') AND due_at < NOW()),
			(SELECT count(*) FROM trust.grievances
			  WHERE status IN ('open', 'acknowledged') AND due_at >= NOW() AND due_at < NOW() + interval '48 hours'),
			(SELECT count(*) FROM trust.user_strikes WHERE created_at >= NOW() - interval '7 days'),
			NOW()
	`).Scan(&open, &reviewing, &out.OpenAppeals, &out.OpenGrievances,
		&out.GrievancesOverdue, &out.GrievancesDueSoon, &out.StrikesLast7Days, &out.GeneratedAt)
	if err != nil {
		return nil, err
	}
	out.OpenReportsByStatus["open"] = open
	out.OpenReportsByStatus["reviewing"] = reviewing
	out.OpenReports = open + reviewing
	return out, nil
}
