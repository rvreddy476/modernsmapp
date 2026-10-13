package service

import (
	"context"
	"errors"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

func (s *Service) PartnerNotificationFailures(ctx context.Context) ([]postgres.PartnerNotificationSummary, error) {
	if s.pgStore == nil {
		return nil, errors.New("partner notification queue unavailable")
	}
	return s.pgStore.PartnerNotificationFailures(ctx)
}
func (s *Service) RetryPartnerNotification(ctx context.Context, id, actor uuid.UUID, reason string) error {
	if s.pgStore == nil {
		return errors.New("partner notification queue unavailable")
	}
	return s.pgStore.RetryPartnerNotification(ctx, id, actor, reason)
}
