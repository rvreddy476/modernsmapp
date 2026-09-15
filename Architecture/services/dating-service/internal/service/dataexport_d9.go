// Data export completion (Dating plan lane D9).
//
// cmd/data-exporter used to POST the export to a media-service route that
// does not exist, so no export ever completed. DatabaseExportStorage keeps the
// finished document sealed on its dating_data_exports row instead, and the
// owner downloads it from GET /v1/dating/data-export/:id/download until it
// expires (7 days; ExpireOldExports clears it).
//
// What D9 adds to the document, and what it keeps out:
//
//   - sealed fields (religion, community, panic and meet points) opened for
//     the owner;
//   - reports the user filed (target as an id, their own details);
//   - reports against the user as reason, status and month only: never the
//     reporter, their details, evidence or the exact time, so a report can
//     not be traced back to the person who filed it;
//   - the user's panic incidents and meets, other people as ids only.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// DataExportDownloadTTL is how long a finished export can be downloaded.
const DataExportDownloadTTL = 7 * 24 * time.Hour

// DataExportDownloadPath is the owner's download route for one export.
func DataExportDownloadPath(exportID uuid.UUID) string {
	return "/v1/dating/data-export/" + exportID.String() + "/download"
}

// DatabaseExportStorage stores the finished export sealed on its row.
type DatabaseExportStorage struct {
	store *store.Store
	now   func() time.Time
}

// NewDatabaseExportStorage builds the sink. The store needs sealing keys.
func NewDatabaseExportStorage(st *store.Store) *DatabaseExportStorage {
	return &DatabaseExportStorage{store: st, now: time.Now}
}

// WriteExport seals payload onto the export row and returns the download
// path and expiry.
func (d *DatabaseExportStorage) WriteExport(ctx context.Context, exportID uuid.UUID, payload []byte) (string, time.Time, error) {
	if err := d.store.SaveExportPayload(ctx, exportID, payload); err != nil {
		return "", time.Time{}, fmt.Errorf("store export: %w", err)
	}
	return DataExportDownloadPath(exportID), d.now().Add(DataExportDownloadTTL).UTC(), nil
}

// DownloadDataExport returns a ready export's document to its owner.
func (s *Service) DownloadDataExport(ctx context.Context, userID, exportID uuid.UUID) ([]byte, error) {
	return s.store.OpenExportPayloadForOwner(ctx, exportID, userID)
}

// ExportedReportFiled is a report the user filed.
type ExportedReportFiled struct {
	ReportID     uuid.UUID `json:"report_id"`
	TargetUserID uuid.UUID `json:"target_user_id"`
	Reason       string    `json:"reason"`
	Details      string    `json:"details,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// ExportedReportAgainst is a report naming the user, reduced so the reporter
// cannot be identified: no reporter, no details, no evidence, month only.
type ExportedReportAgainst struct {
	Reason string `json:"reason"`
	Status string `json:"status"`
	Month  string `json:"month"`
}

// ExportedPanicIncident is one of the user's own panic incidents.
type ExportedPanicIncident struct {
	IncidentID   uuid.UUID  `json:"incident_id"`
	Source       string     `json:"source"`
	Status       string     `json:"status"`
	TriggerCount int        `json:"trigger_count"`
	Latitude     *float64   `json:"latitude,omitempty"`
	Longitude    *float64   `json:"longitude,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
}

// ExportedMeet is a meet the user scheduled; the other person is an id.
type ExportedMeet struct {
	MeetID        uuid.UUID `json:"meet_id"`
	WithUserID    uuid.UUID `json:"with_user_id"`
	ScheduledAt   time.Time `json:"scheduled_at"`
	Venue         *string   `json:"venue,omitempty"`
	Latitude      *float64  `json:"latitude,omitempty"`
	Longitude     *float64  `json:"longitude,omitempty"`
	CheckInStatus *string   `json:"check_in_status,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// addSafetyRecords fills the D9 sections. A failed read fails the export
// rather than silently leaving a section out.
func (s *Service) addSafetyRecords(ctx context.Context, userID uuid.UUID, out *UserDataExport) error {
	filed, err := s.store.ListReportsFiledBy(ctx, userID)
	if err != nil {
		return err
	}
	for _, r := range filed {
		out.ReportsFiled = append(out.ReportsFiled, ExportedReportFiled{
			ReportID: r.ID, TargetUserID: r.TargetID, Reason: r.Category, Details: r.Details,
			Status: r.Status, CreatedAt: r.CreatedAt,
		})
	}
	against, err := s.store.ListReportsAgainst(ctx, userID)
	if err != nil {
		return err
	}
	for _, r := range against {
		out.ReportsAgainst = append(out.ReportsAgainst, ExportedReportAgainst{
			Reason: r.Category, Status: r.Status, Month: r.CreatedAt.UTC().Format("2006-01"),
		})
	}
	incidents, err := s.store.ListPanicIncidentsForUser(ctx, userID)
	if err != nil {
		return err
	}
	for _, inc := range incidents {
		out.PanicIncidents = append(out.PanicIncidents, ExportedPanicIncident{
			IncidentID: inc.ID, Source: inc.Source, Status: inc.Status, TriggerCount: inc.TriggerCount,
			Latitude: inc.Latitude, Longitude: inc.Longitude, CreatedAt: inc.CreatedAt, ResolvedAt: inc.ResolvedAt,
		})
	}
	meets, err := s.store.ListMeetsForUser(ctx, userID)
	if err != nil {
		return err
	}
	for _, m := range meets {
		out.Meets = append(out.Meets, ExportedMeet{
			MeetID: m.ID, WithUserID: m.WithUserID, ScheduledAt: m.ScheduledAt, Venue: m.Venue,
			Latitude: m.Latitude, Longitude: m.Longitude, CheckInStatus: m.CheckInStatus, CreatedAt: m.CreatedAt,
		})
	}
	return nil
}
