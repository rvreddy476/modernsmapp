package postgres

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A released assignment is reused for the next partner. Keep a safe immutable
// view in the existing tracking ledger before clearing it; never keep the
// customer's precise location, contact, instructions or handover codes here.
func (s *Store) releasedAssignmentSnapshotTx(ctx context.Context, tx pgx.Tx, assignmentID uuid.UUID) (*DeliveryAssignment, []byte, error) {
	a, err := s.getAssignmentTx(ctx, tx, assignmentID)
	if err != nil {
		return nil, nil, err
	}
	a.Status = "REJECTED"
	a.OrderStatus = "DELIVERY_ASSIGNING"
	a.PickupCode = ""
	a.Drop = nil
	a.Navigation = nil
	a.ETAAt = ""
	a.ETASource = ""
	a.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := json.Marshal(a)
	return a, raw, err
}

func (s *Store) appendReleasedDeliveryHistory(ctx context.Context, partnerID uuid.UUID, current []DeliveryAssignment) ([]DeliveryAssignment, error) {
	rows, err := s.db.Query(ctx, `SELECT snapshot FROM food.delivery_tracking_events
		WHERE delivery_partner_id=$1 AND status='REJECTED' AND snapshot <> '{}'::jsonb
		ORDER BY created_at DESC LIMIT 100`, partnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var a DeliveryAssignment
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		current = append(current, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(current, func(i, j int) bool {
		return deliveryHistoryTime(current[i].CreatedAt).After(deliveryHistoryTime(current[j].CreatedAt))
	})
	if len(current) > 100 {
		current = current[:100]
	}
	return current, nil
}

func releasedAssignmentReplay(body map[string]any) (*DeliveryAssignment, error) {
	v, ok := body["released_assignment"]
	if !ok || v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var a DeliveryAssignment
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func deliveryHistoryTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999Z07"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
