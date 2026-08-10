package service

import (
	"context"

	"github.com/atpost/user-service/internal/store"
	"github.com/google/uuid"
)

// SubscribeToChannel subscribes a user to a channel with the given notification preference.
func (s *Service) SubscribeToChannel(ctx context.Context, channelID, userID uuid.UUID, notifyOn string) error {
	return s.store.SubscribeToChannel(ctx, channelID, userID, notifyOn)
}

// UnsubscribeFromChannel removes a user's subscription from a channel.
func (s *Service) UnsubscribeFromChannel(ctx context.Context, channelID, userID uuid.UUID) error {
	return s.store.UnsubscribeFromChannel(ctx, channelID, userID)
}

// GetChannelSubscriptionStatus returns the subscription row or nil if not subscribed.
func (s *Service) GetChannelSubscriptionStatus(ctx context.Context, channelID, userID uuid.UUID) (*store.ChannelSubscription, error) {
	return s.store.GetChannelSubscription(ctx, channelID, userID)
}

// ListUserChannelSubscriptions returns all channels a user subscribes to (paginated).
func (s *Service) ListUserChannelSubscriptions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.ChannelSubscription, error) {
	return s.store.ListUserChannelSubscriptions(ctx, userID, limit, offset)
}

// ListChannelSubscribers returns all subscribers of a channel (paginated).
func (s *Service) ListChannelSubscribers(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]store.ChannelSubscription, error) {
	return s.store.ListChannelSubscribers(ctx, channelID, limit, offset)
}

// Module 1 P0-3 — internal fan-out helpers.

// GetChannelByOwner resolves a user to their canonical channel.
func (s *Service) GetChannelByOwner(ctx context.Context, ownerID uuid.UUID) (*uuid.UUID, error) {
	return s.store.GetChannelByOwner(ctx, ownerID)
}

// ListSubscriberIDsAfter returns a keyset page of notify-eligible
// subscriber IDs for upload fan-out.
func (s *Service) ListSubscriberIDsAfter(ctx context.Context, channelID, after uuid.UUID, limit int) ([]uuid.UUID, error) {
	return s.store.ListSubscriberIDsAfter(ctx, channelID, after, limit)
}

// ListSubscribedChannelOwners returns creator IDs the viewer subscribes
// to — the source for the PostTube Subscriptions feed.
func (s *Service) ListSubscribedChannelOwners(ctx context.Context, viewerID uuid.UUID, limit int) ([]uuid.UUID, error) {
	return s.store.ListSubscribedChannelOwners(ctx, viewerID, limit)
}

// ListSubscribedChannelOwnersAfter is the paginated form (P2-2).
func (s *Service) ListSubscribedChannelOwnersAfter(ctx context.Context, viewerID, after uuid.UUID, limit int) ([]uuid.UUID, error) {
	return s.store.ListSubscribedChannelOwnersAfter(ctx, viewerID, after, limit)
}
