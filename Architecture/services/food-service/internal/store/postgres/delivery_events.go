package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The review and its notification commit together. Only routing IDs and the
// decision leave food-service; document numbers, images and review notes do not.
func enqueueDeliveryReviewTx(ctx context.Context, tx pgx.Tx, partnerID, documentID uuid.UUID, status string) error {
	var userID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT user_id FROM food.delivery_partners WHERE id = $1`, partnerID).Scan(&userID); err != nil {
		return err
	}
	typ := "food.delivery_partner.status_changed"
	if documentID != uuid.Nil {
		typ = "food.delivery_partner.document_reviewed"
	}
	body, err := json.Marshal(struct {
		PartnerID  uuid.UUID `json:"partner_id"`
		UserID     uuid.UUID `json:"partner_user_id"`
		DocumentID uuid.UUID `json:"document_id"`
		Status     string    `json:"status"`
		OccurredAt time.Time `json:"occurred_at"`
	}{partnerID, userID, documentID, status, time.Now().UTC()})
	if err != nil {
		return err
	}
	return foodOutbox.Enqueue(ctx, tx, typ, "food:delivery:"+partnerID.String(), body)
}
