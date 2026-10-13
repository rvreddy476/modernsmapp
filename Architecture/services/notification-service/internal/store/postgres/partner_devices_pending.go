package postgres

import (
	"context"
	"github.com/google/uuid"
)

// PendingPartnerDevices bounds both rows transferred and sends per claim. The
// durable receipt is also the pagination cursor, so process restarts cannot
// skip unsent devices. A token shared by duplicate platform rows is sent once.
func (s *Store) PendingPartnerDevices(ctx context.Context, user uuid.UUID, app string, job uuid.UUID, limit int) ([]UserDevice, bool, error) {
	limit = min(max(limit, 1), 10)
	rows, err := s.db.Query(ctx, `SELECT id,user_id,platform,push_token,app,is_active,created_at,updated_at
	 FROM (SELECT DISTINCT ON(d.push_token) d.* FROM user_devices d
	 WHERE d.user_id=$1 AND (d.app=$2 OR ($3 AND d.app='partner')) AND d.is_active
	 AND NOT EXISTS(SELECT 1 FROM partner_notification_receipts r WHERE r.job_id=$4
	 AND r.token_hash=encode(sha256(convert_to(d.push_token,'UTF8')),'hex'))
	 ORDER BY d.push_token,d.updated_at DESC,d.id) pending
	 ORDER BY updated_at DESC,id LIMIT $5`, user, app, includesUnifiedPartner(app), job, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []UserDevice{}
	for rows.Next() {
		var d UserDevice
		if err = rows.Scan(&d.ID, &d.UserID, &d.Platform, &d.PushToken, &d.App, &d.IsActive, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, false, err
		}
		out = append(out, d)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

func (s *Store) HasActivePartnerDevice(ctx context.Context, user uuid.UUID, app string) (bool, error) {
	var active bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_devices WHERE user_id=$1 AND (app=$2 OR($3 AND app='partner')) AND is_active)`, user, app, includesUnifiedPartner(app)).Scan(&active)
	return active, err
}
