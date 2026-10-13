package events

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"log/slog"
	"time"
)

func partnerSupportNotificationType(typ string) bool {
	return typ == "rider.partner.support.raised" || typ == "rider.partner.support.updated"
}

func (c *Consumer) handlePartnerSupport(ctx context.Context, typ, key string, raw json.RawMessage) error {
	var e struct {
		User      uuid.UUID `json:"partner_user_id"`
		Ride      uuid.UUID `json:"ride_id"`
		Complaint uuid.UUID `json:"complaint_id"`
		At        time.Time `json:"occurred_at"`
	}
	if json.Unmarshal(raw, &e) != nil || e.User == uuid.Nil || e.Ride == uuid.Nil || e.Complaint == uuid.Nil {
		slog.Error("partner support event lacks routing identities", "event", typ)
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("partner support notifications unavailable")
	}
	return c.service.QueuePartnerSupport(ctx, typ, key, e.User, e.Ride, e.Complaint, e.At)
}
