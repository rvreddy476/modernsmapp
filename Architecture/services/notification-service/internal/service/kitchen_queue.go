package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"time"
)

// Merchant notices reuse durable transport, but never enter the delivery-partner
// audience or dispatch policy. Keep this entry point explicitly allowlisted.
func (s *Service) queueKitchenNotice(ctx context.Context, p FoodPush) error {
	if s.pgStore == nil {
		return errors.New("kitchen notification queue unavailable")
	}
	if p.App != AppFeastKitchen {
		return errors.New("invalid kitchen audience")
	}
	if err := p.validate(); err != nil {
		return err
	}
	entity, kind := p.OrderID, "food_order"
	if p.accountNotice() {
		entity, kind = p.EntityID, "food_restaurant"
	}
	if entity == uuid.Nil {
		return errors.New("missing kitchen entity")
	}
	n := PartnerNotice{ID: foodDedupID(p), UserID: p.RecipientID, App: p.App, Type: p.Type,
		EntityID: entity, EntityType: kind, Title: p.Title, Body: p.Body, DeepLink: p.DeepLink,
		Data: FoodPushData(p), CreatedAt: p.CreatedAt, Inbox: false}
	if p.Type == FoodTypeOrderNew {
		n.ExpiresAt = p.AcceptDeadlineAt
		if n.ExpiresAt == nil {
			// Legacy events have no authoritative deadline. Never replay them as fresh
			// ringing orders; the Kitchen queue remains the authoritative readback.
			expired := time.Unix(0, 0).UTC()
			n.ExpiresAt = &expired
		}
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return s.pgStore.EnqueuePartnerNotification(ctx, n.ID, raw, n.ExpiresAt)
}
