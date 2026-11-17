package service

import (
	"context"

	"github.com/atpost/post-service/internal/videogate"
	"github.com/google/uuid"
)

// reconcilePendingVideoPost is CreatePost's second look at a video post it
// stored pending (reels-1): the gate is decided again from media_assets, so a
// media-service blip at create, or a completion consumed before the post's
// video_metadata row committed, does not leave the post pending until the
// sweep. Best-effort; internal/videogate's sweeper is the backstop.
func (s *Service) reconcilePendingVideoPost(ctx context.Context, postID, _ uuid.UUID) {
	if s.pgStore == nil {
		return
	}
	videogate.ReconcileOne(ctx, s.pgStore, postID, s.BustPostBody)
}

// BustPostBody drops a post's cached body so a review-status change is seen
// at once (the key the transcode consumer busts).
func (s *Service) BustPostBody(ctx context.Context, postID uuid.UUID) {
	if s.rdb != nil {
		_ = s.rdb.Del(ctx, "post:body:"+postID.String()).Err()
	}
}
