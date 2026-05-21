package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// MenuItemReport mirrors a row in food.menu_item_reports.
type MenuItemReport struct {
	ID         uuid.UUID  `json:"id"`
	MenuItemID uuid.UUID  `json:"menu_item_id"`
	ReporterID uuid.UUID  `json:"reporter_id"`
	Category   string     `json:"category"`
	Detail     *string    `json:"detail,omitempty"`
	ResolvedAt *string    `json:"resolved_at,omitempty"`
	ResolvedBy *uuid.UUID `json:"resolved_by,omitempty"`
	CreatedAt  string     `json:"created_at"`
}

// ReportMenuItem records a customer complaint. After insert the
// store auto-flips moderation_status to `flagged` once a configurable
// threshold of unresolved reports is reached (currently 3) so the
// admin queue surfaces it.
const autoFlagThreshold = 3

func (s *Store) ReportMenuItem(ctx context.Context, reporterID, itemID uuid.UUID, category, detail string) (*MenuItemReport, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var r MenuItemReport
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.menu_item_reports (menu_item_id, reporter_id, category, detail)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		RETURNING id, menu_item_id, reporter_id, category, detail, resolved_at::text, resolved_by, created_at::text
	`, itemID, reporterID, category, detail).Scan(
		&r.ID, &r.MenuItemID, &r.ReporterID, &r.Category, &r.Detail,
		&r.ResolvedAt, &r.ResolvedBy, &r.CreatedAt,
	); err != nil {
		return nil, err
	}
	var unresolved int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM food.menu_item_reports
		WHERE menu_item_id = $1 AND resolved_at IS NULL
	`, itemID).Scan(&unresolved); err != nil {
		return nil, err
	}
	if unresolved >= autoFlagThreshold {
		if _, err := tx.Exec(ctx, `
			UPDATE food.menu_items
			SET moderation_status = 'flagged',
				moderation_reason = COALESCE(moderation_reason, '') || ' auto-flagged: ' || $2 || ' reports'
			WHERE id = $1 AND moderation_status = 'approved'
		`, itemID, unresolved); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListPendingModeration returns menu items in flagged or pending_review,
// joined with their open report count. Admin/moderator only.
type PendingModerationItem struct {
	ID                uuid.UUID `json:"id"`
	RestaurantID      uuid.UUID `json:"restaurant_id"`
	RestaurantName    string    `json:"restaurant_name"`
	Name              string    `json:"name"`
	ModerationStatus  string    `json:"moderation_status"`
	ModerationReason  *string   `json:"moderation_reason,omitempty"`
	OpenReports       int       `json:"open_reports"`
	LatestReportAt    *string   `json:"latest_report_at,omitempty"`
	CreatedAt         string    `json:"created_at"`
}

func (s *Store) ListPendingModeration(ctx context.Context, limit int) ([]PendingModerationItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT mi.id, r.id, r.name, mi.name, mi.moderation_status::text,
			mi.moderation_reason,
			COALESCE((SELECT COUNT(*) FROM food.menu_item_reports
				WHERE menu_item_id = mi.id AND resolved_at IS NULL), 0) AS open_reports,
			(SELECT MAX(created_at)::text FROM food.menu_item_reports
				WHERE menu_item_id = mi.id) AS latest_report_at,
			mi.created_at::text
		FROM food.menu_items mi
		JOIN food.restaurants r ON r.id = mi.restaurant_id
		WHERE mi.moderation_status IN ('flagged', 'pending_review')
		ORDER BY mi.moderation_status, latest_report_at DESC NULLS LAST
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending moderation: %w", err)
	}
	defer rows.Close()
	var out []PendingModerationItem
	for rows.Next() {
		var it PendingModerationItem
		if err := rows.Scan(&it.ID, &it.RestaurantID, &it.RestaurantName, &it.Name,
			&it.ModerationStatus, &it.ModerationReason, &it.OpenReports,
			&it.LatestReportAt, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ModerateMenuItem sets the admin verdict on a menu item. `status` must
// be one of approved | rejected | pending_review.
func (s *Store) ModerateMenuItem(ctx context.Context, adminID, itemID uuid.UUID, status, reason string) error {
	allowed := map[string]bool{"approved": true, "rejected": true, "pending_review": true, "flagged": true}
	if !allowed[status] {
		return fmt.Errorf("invalid moderation status: %s", status)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE food.menu_items
		SET moderation_status = $2::food.moderation_status,
			moderation_reason = NULLIF($3, ''),
			moderated_at = NOW(),
			moderated_by = $4
		WHERE id = $1
	`, itemID, status, reason, adminID); err != nil {
		return err
	}
	if status == "approved" || status == "rejected" {
		if _, err := tx.Exec(ctx, `
			UPDATE food.menu_item_reports
			SET resolved_at = NOW(), resolved_by = $2
			WHERE menu_item_id = $1 AND resolved_at IS NULL
		`, itemID, adminID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
