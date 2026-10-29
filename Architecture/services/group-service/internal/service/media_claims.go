package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/atpost/group-service/internal/mediaclaims"
	"github.com/atpost/group-service/internal/mediaclient"
	"github.com/google/uuid"
)

// Group media authority and claims.
//
// groups.avatar_media_id / cover_media_id, group_events.cover_media_id and
// group_posts.attachments carry no foreign key into media-service. Before a write names an asset, the
// gate checks it is the actor's own finished upload and media-service records
// a claim on it, so the uploader cannot delete it while the group or event
// shows it; replaced, cancelled or deleted media is released afterwards. A
// group post's attachments are claimed (kind group_post_attachment, ref = the
// post id) by the author, under the same owner/readiness check, and released
// when the post is deleted by its author or a moderator, rejected from the
// approval queue, or its group is deleted. The
// rules are internal/mediaclaims; the backfill is cmd/media-claims.
//
// group_resources.media_id has no writer in this service (no route creates a
// resource), so nothing here claims it.
//
// Fail closed: without a media-service client, a write that sets a new
// avatar, cover or event cover is refused with 503 MEDIA_UNAVAILABLE. Writes
// that set none never call out.

// WithMedia installs the attach gate and claim client.
func (s *Service) WithMedia(g *mediaclaims.Gate) *Service {
	s.media = g
	return s
}

// keepUnlessSet is the next value of an optional column: set, or kept.
func keepUnlessSet(current, set *uuid.UUID) *uuid.UUID {
	if set != nil {
		return set
	}
	return current
}

// groupMediaChanges is what one save does to a group's media.
func groupMediaChanges(id uuid.UUID, prevAvatar, prevCover, nextAvatar, nextCover *uuid.UUID) []mediaclaims.Change {
	avatarAdd, avatarDrop := mediaclaims.OptionalDiff(prevAvatar, nextAvatar)
	coverAdd, coverDrop := mediaclaims.OptionalDiff(prevCover, nextCover)
	return []mediaclaims.Change{
		{Kind: mediaclient.KindGroupAvatar, Ref: id, Add: avatarAdd, Drop: avatarDrop},
		{Kind: mediaclient.KindGroupCover, Ref: id, Add: coverAdd, Drop: coverDrop},
	}
}

// releaseGroupMedia releases everything a deleted group held: its avatar and
// cover, every event's cover and every post's attachments. Best effort and
// bounded; whatever is left is the reconcile tool's.
func (s *Service) releaseGroupMedia(ctx context.Context, groupID uuid.UUID) {
	if !s.media.Release(ctx, mediaclient.KindGroupAvatar, groupID, nil) ||
		!s.media.Release(ctx, mediaclient.KindGroupCover, groupID, nil) {
		return // media-service is not answering; one timeout, not one per event
	}
	if s.store == nil {
		return
	}
	ids, err := s.store.ListEventIDsWithCover(context.WithoutCancel(ctx), groupID)
	if err != nil {
		slog.Warn("group-service: deleted group's event covers not released; run the reconcile tool",
			"group_id", groupID, "error", err)
		return
	}
	for i, id := range ids {
		if !s.media.Release(ctx, mediaclient.KindGroupEventCover, id, nil) {
			slog.Warn("group-service: stopped releasing a deleted group's event covers; run the reconcile tool",
				"group_id", groupID, "left", len(ids)-i)
			return
		}
	}
	posts, err := s.store.PostIDsWithAttachments(context.WithoutCancel(ctx), groupID)
	if err != nil {
		slog.Warn("group-service: deleted group's post attachments not released; run the reconcile tool",
			"group_id", groupID, "error", err)
		return
	}
	if len(posts) > maxInlinePostReleases {
		slog.Warn("group-service: deleted group has more media posts than are released inline; run the reconcile tool",
			"group_id", groupID, "posts", len(posts), "released", maxInlinePostReleases)
		posts = posts[:maxInlinePostReleases]
	}
	for i, id := range posts {
		if !s.media.Release(ctx, mediaclient.KindGroupPostAttachment, id, nil) {
			slog.Warn("group-service: stopped releasing a deleted group's post attachments; run the reconcile tool",
				"group_id", groupID, "left", len(posts)-i)
			return
		}
	}
}

// maxInlinePostReleases bounds the releases a group delete makes while its
// caller waits (one media-service call per post); the rest are dead rows the
// reconcile tool releases.
const maxInlinePostReleases = 500

// postAttachmentChange is what writing a new post does to its media: it adds
// every attached id. A post's attachments are never edited, so there is
// nothing to drop.
func postAttachmentChange(postID uuid.UUID, ids []uuid.UUID) []mediaclaims.Change {
	return []mediaclaims.Change{{Kind: mediaclient.KindGroupPostAttachment, Ref: postID, Add: mediaclaims.Unique(ids)}}
}

// releasePostMedia releases everything a post that is no longer shown
// (deleted, rejected) held. Best effort: a failure is logged by the gate and
// left to the reconcile tool, which sees the dead row.
func (s *Service) releasePostMedia(ctx context.Context, postID uuid.UUID, attachments json.RawMessage) {
	if len(mediaclaims.StoredAttachmentIDs(attachments)) == 0 {
		return // nothing was claimed
	}
	s.media.Release(ctx, mediaclient.KindGroupPostAttachment, postID, nil)
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
