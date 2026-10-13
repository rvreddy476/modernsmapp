package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Customer notices share the durable delivery engine, NOT partner dispatch policy.
// Separate entry point prevents merchant/offer payloads bypassing audience checks.
func customerNoticeAllowed(n PartnerNotice) bool {
	if n.App != postgres.AppMomentum {
		return false
	}
	if spec, ok := doorstepPushSpecs[n.Type]; ok {
		return spec.App == postgres.AppMomentum
	}
	if n.Type == FoodTypeOrderStatus {
		return true
	}
	if strings.HasPrefix(n.Type, "ride.") {
		return n.Type == "ride.assigned" || n.Type == "ride.arriving" || n.Type == "ride.arrived" || n.Type == "ride.started" || n.Type == "ride.completed" || n.Type == "ride.cancelled" || n.Type == "ride.payment.paid"
	}
	switch n.Type {
	case OrderTypeConfirmed, OrderTypeShipped, OrderTypeDelivered, OrderTypeCancelled, OrderTypeRefunded, OrderTypePaymentFailed:
		return true
	}
	return false
}

func (s *Service) queueCustomerNotice(ctx context.Context, n PartnerNotice) error {
	if s.pgStore == nil || !customerNoticeAllowed(n) || n.ID == uuid.Nil || n.UserID == uuid.Nil || n.EntityID == uuid.Nil {
		return errors.New("invalid customer notification")
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	// Keep the history durable. Provider delivery retries are bounded by the worker.
	return s.pgStore.EnqueuePartnerNotification(ctx, n.ID, raw, nil)
}
