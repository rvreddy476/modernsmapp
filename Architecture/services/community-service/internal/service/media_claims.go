package service

import (
	"context"
	"fmt"

	"github.com/atpost/community-service/internal/mediaclaims"
	"github.com/atpost/community-service/internal/mediaclient"
	"github.com/google/uuid"
)

// Community media authority and claims.
//
// communities.avatar_media_id and banner_media_id carry no foreign key into
// media-service. Before a save names an asset, the gate checks it is the
// actor's own finished upload and media-service records a claim on it, so the
// uploader cannot delete it while the community shows it; the replaced or
// deleted community's media is released afterwards. The rules are
// internal/mediaclaims; the backfill is cmd/media-claims.
//
// Fail closed: without a media-service client, a save that sets a new avatar
// or banner is refused with 503 MEDIA_UNAVAILABLE. Saves that change neither
// never call out.

// WithMedia installs the attach gate and claim client.
func (s *Service) WithMedia(g *mediaclaims.Gate) *Service {
	s.media = g
	return s
}

// communityMediaChanges is what one save does to a community's media.
func communityMediaChanges(id uuid.UUID, prevAvatar, prevBanner, nextAvatar, nextBanner *uuid.UUID) []mediaclaims.Change {
	avatarAdd, avatarDrop := mediaclaims.OptionalDiff(prevAvatar, nextAvatar)
	bannerAdd, bannerDrop := mediaclaims.OptionalDiff(prevBanner, nextBanner)
	return []mediaclaims.Change{
		{Kind: mediaclient.KindCommunityAvatar, Ref: id, Add: avatarAdd, Drop: avatarDrop},
		{Kind: mediaclient.KindCommunityBanner, Ref: id, Add: bannerAdd, Drop: bannerDrop},
	}
}

// releaseCommunityMedia releases everything a deleted community held. Best
// effort; whatever is left is the reconcile tool's.
func (s *Service) releaseCommunityMedia(ctx context.Context, id uuid.UUID) {
	if !s.media.Release(ctx, mediaclient.KindCommunityAvatar, id, nil) {
		return // media-service is not answering; one timeout, not two
	}
	s.media.Release(ctx, mediaclient.KindCommunityBanner, id, nil)
}

// lockMediaHolder takes the store's per-row media lock. A Service without a
// store (unit tests of the pure protocol) has no rows to race over.
func (s *Service) lockMediaHolder(ctx context.Context, ref uuid.UUID) (func(), error) {
	if s.store == nil {
		return func() {}, nil
	}
	unlock, err := s.store.LockMediaHolder(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to lock media holder: %w", err)
	}
	return unlock, nil
}
