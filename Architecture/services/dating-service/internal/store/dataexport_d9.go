// Data export storage and reads (Dating plan lane D9).
//
// The finished export is kept sealed on its dating_data_exports row
// (payload_sealed, scope dating.data_export) until it expires, and is opened
// only for its owner. The reads below gather what the export adds in D9: the
// profile even when soft-deleted, the user's own reports and the reports
// against them (the service strips the reporter), the user's panic incidents
// and meets with their sealed points opened.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SaveExportPayload seals doc onto the export row. Without keys it refuses
// (ErrPIINotConfigured), so an export is never stored in plaintext.
func (s *Store) SaveExportPayload(ctx context.Context, exportID uuid.UUID, doc []byte) error {
	if exportID == uuid.Nil || len(doc) == 0 {
		return fmt.Errorf("invalid: export id and payload required")
	}
	blob, err := s.pii.SealExport(ctx, doc)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `UPDATE dating_data_exports SET payload_sealed = $2 WHERE id = $1`, exportID, blob)
	if err != nil {
		return fmt.Errorf("save export payload: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDataExportNotFound
	}
	return nil
}

// OpenExportPayloadForOwner returns the opened export only to its owner, only
// while it is ready and unexpired. Every other case is ErrDataExportNotFound,
// so an export's existence is never revealed to anyone else.
func (s *Store) OpenExportPayloadForOwner(ctx context.Context, exportID, userID uuid.UUID) ([]byte, error) {
	if exportID == uuid.Nil || userID == uuid.Nil {
		return nil, ErrDataExportNotFound
	}
	var blob []byte
	err := s.db.QueryRow(ctx, `
        SELECT payload_sealed FROM dating_data_exports
        WHERE id = $1 AND user_id = $2 AND status = 'ready'
          AND payload_sealed IS NOT NULL
          AND (download_expires_at IS NULL OR download_expires_at > now())`, exportID, userID).Scan(&blob)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDataExportNotFound
		}
		return nil, fmt.Errorf("open export payload: %w", err)
	}
	return s.pii.OpenExport(ctx, blob)
}

// GetProfileForExport returns the profile even when soft-deleted.
func (s *Store) GetProfileForExport(ctx context.Context, userID uuid.UUID) (*Profile, error) {
	return s.scanProfile(ctx, s.db.QueryRow(ctx, `
        SELECT `+profileSelectCols+` FROM dating_profiles WHERE user_id = $1`, userID))
}

// ListReportsFiledBy returns the reports the user filed, newest first.
func (s *Store) ListReportsFiledBy(ctx context.Context, userID uuid.UUID) ([]*Report, error) {
	return s.listReportsWhere(ctx, `reporter_id = $1`, userID)
}

// ListReportsAgainst returns the reports naming the user as the target.
func (s *Store) ListReportsAgainst(ctx context.Context, userID uuid.UUID) ([]*Report, error) {
	return s.listReportsWhere(ctx, `target_id = $1`, userID)
}

func (s *Store) listReportsWhere(ctx context.Context, predicate string, userID uuid.UUID) ([]*Report, error) {
	rows, err := s.db.Query(ctx, `SELECT `+reportCols+` FROM dating_reports WHERE `+predicate+`
        ORDER BY created_at DESC LIMIT 500`, userID)
	if err != nil {
		return nil, fmt.Errorf("list reports for export: %w", err)
	}
	defer rows.Close()
	var out []*Report
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPanicIncidentsForUser returns the user's own incidents, points opened.
func (s *Store) ListPanicIncidentsForUser(ctx context.Context, userID uuid.UUID) ([]*PanicIncident, error) {
	rows, err := s.db.Query(ctx, `SELECT `+panicIncidentCols+` FROM dating_panic_incidents
        WHERE user_id = $1 ORDER BY created_at DESC LIMIT 500`, userID)
	if err != nil {
		return nil, fmt.Errorf("list panic incidents for export: %w", err)
	}
	defer rows.Close()
	var out []*PanicIncident
	for rows.Next() {
		inc, err := s.scanPanicIncident(ctx, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

// ListMeetsForUser returns the meets the user scheduled, venue points opened.
func (s *Store) ListMeetsForUser(ctx context.Context, userID uuid.UUID) ([]*Meet, error) {
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, with_user_id, scheduled_at, venue, latitude, longitude, location_sealed,
               check_in_status, checked_in_at, no_show_at, created_at
        FROM dating_meets WHERE user_id = $1 ORDER BY scheduled_at DESC LIMIT 500`, userID)
	if err != nil {
		return nil, fmt.Errorf("list meets for export: %w", err)
	}
	defer rows.Close()
	var out []*Meet
	for rows.Next() {
		m := &Meet{}
		if err := rows.Scan(&m.ID, &m.UserID, &m.WithUserID, &m.ScheduledAt, &m.Venue, &m.Latitude, &m.Longitude,
			&m.locationSealed, &m.CheckInStatus, &m.CheckedInAt, &m.NoShowAt, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan meet for export: %w", err)
		}
		s.openMeetPoint(ctx, m)
		out = append(out, m)
	}
	return out, rows.Err()
}
