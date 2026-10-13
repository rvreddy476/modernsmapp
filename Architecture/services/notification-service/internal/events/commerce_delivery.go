package events

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"log/slog"
	"time"
)

func commerceDeliveryNotificationType(typ string) bool {
	switch typ {
	case "commerce.delivery.application.reviewed", "commerce.delivery.document.reviewed", "commerce.delivery.offer", "commerce.delivery.assigned", "commerce.delivery.picked_up", "commerce.delivery.delivered", "commerce.delivery.failed", "commerce.delivery.cancelled", "commerce.delivery.earnings", "commerce.delivery.support":
		return true
	}
	return false
}

func (c *Consumer) handleCommerceDeliveryWork(ctx context.Context, typ, key string, raw json.RawMessage) error {
	var e struct {
		UserID     uuid.UUID `json:"partner_user_id"`
		JobID      uuid.UUID `json:"job_id"`
		OfferID    uuid.UUID `json:"offer_id"`
		OccurredAt time.Time `json:"occurred_at"`
		ExpiresAt  time.Time `json:"expires_at"`
	}
	if json.Unmarshal(raw, &e) != nil || e.UserID == uuid.Nil || e.JobID == uuid.Nil || (typ == "commerce.delivery.offer" && (e.OfferID == uuid.Nil || e.ExpiresAt.IsZero())) {
		slog.Error("commerce delivery work notification malformed", "event", typ)
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("commerce delivery notifications unavailable")
	}
	return c.service.QueueCommerceDeliveryWork(ctx, typ, key, e.UserID, e.JobID, e.OfferID, e.OccurredAt, e.ExpiresAt)
}

func (c *Consumer) handleCommerceDeliveryReview(ctx context.Context, typ, key string, raw json.RawMessage) error {
	var e struct {
		UserID     uuid.UUID `json:"partner_user_id"`
		ProfileID  uuid.UUID `json:"profile_id"`
		Status     string    `json:"status"`
		OccurredAt time.Time `json:"occurred_at"`
	}
	if json.Unmarshal(raw, &e) != nil || e.UserID == uuid.Nil || e.ProfileID == uuid.Nil {
		slog.Error("commerce delivery notification malformed; recipient required", "event", typ)
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("commerce delivery notifications unavailable")
	}
	return c.service.QueueCommerceDeliveryReview(ctx, typ, key, e.UserID, e.ProfileID, e.OccurredAt)
}
