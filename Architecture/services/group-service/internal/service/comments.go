package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

func (s *Service) ListGroupPostComments(ctx context.Context, actorID, groupID, postID uuid.UUID, limit, offset int) ([]store.GroupPostComment, error) {
	// The same readability rule as the post itself: a private group's
	// comments are for its members.
	if _, err := s.authorize(ctx, groupID, actorID, accessRead); err != nil {
		return nil, err
	}
	// Use cached post-in-group check (Redis → DB fallback)
	exists, err := s.store.PostExistsInGroup(ctx, s.rdb, postID, groupID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("not found: post not found in this group")
	}
	return s.store.ListGroupPostComments(ctx, postID, limit, offset)
}

/*
	publicActorID is the id an actor is known by on a post's wire: their own,
	unless they are the anonymous author of that post, in which case it is
	the post's alias. Every event and realtime payload about a post that
	names an actor goes through this, so an author cannot be unmasked by
	commenting on, or deleting, their own anonymous post.
*/
func (s *Service) publicActorID(post *store.GroupPostV2, actorID uuid.UUID) uuid.UUID {
	if post != nil && post.IsAnonymous && post.AuthorID == actorID.String() && post.AnonAlias != nil {
		return *post.AnonAlias
	}
	return actorID
}

// ErrCommentsRestricted: the group's comment_permission keeps commenting to
// a higher role than the caller's (G-L1).
var ErrCommentsRestricted = errors.New("forbidden: commenting in this group is limited to its moderators or admins")

func (s *Service) AddGroupPostComment(ctx context.Context, actorID, groupID, postID uuid.UUID, body string, parentID *uuid.UUID) (*store.GroupPostComment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("body is required")
	}

	// The gate, not the five-minute membership cache: a ban must stop
	// commenting at once, and comment_permission needs the caller's rank.
	acc, err := s.authorize(ctx, groupID, actorID, accessWrite)
	if err != nil {
		return nil, err
	}
	if !settingAllows(acc.g.CommentPermission, acc.rank) {
		return nil, ErrCommentsRestricted
	}

	// Use cached post-in-group check (Redis hit = ~0.5ms vs DB = ~15ms)
	exists, err := s.store.PostExistsInGroup(ctx, s.rdb, postID, groupID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("not found: post not found in this group")
	}

	// Insert comment (single DB write — count increment is async inside store)
	// The anonymous author commenting on their own post stays the post's
	// pseudonym — decided here, never by the client (store/anonymous.go).
	post, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return nil, err
	}
	anon := post != nil && post.IsAnonymous && post.AuthorID == actorID.String()
	var alias *uuid.UUID
	if anon {
		alias = post.AnonAlias
	}
	publicID := s.publicActorID(post, actorID)
	comment, err := s.store.AddGroupPostComment(ctx, postID, actorID.String(), body, parentID, anon, alias)
	if err != nil {
		return nil, err
	}

	// Fire-and-forget: publish Kafka event for notifications/realtime
	go func() {
		parentStr := ""
		if parentID != nil {
			parentStr = parentID.String()
		}
		s.publishEvent(func() error {
			return s.producer.PublishGroupPostCommented(context.Background(), groupID, postID, comment.ID, publicID, body, parentStr)
		})
	}()

	return comment, nil
}

// DeleteGroupPostComment deletes a comment of a post of the PATH group: the
// author's own, or any for the group's moderators and above. The store
// matches the comment on its post and on that post's group, so a moderator
// of group A can no longer delete group B's comments by id (G-M3).
func (s *Service) DeleteGroupPostComment(ctx context.Context, actorID, groupID, postID, commentID uuid.UUID) error {
	// The membership read fails closed: before, its error was dropped and the
	// caller treated as unprivileged, which hid a failure as a 404.
	acc, err := s.authorize(ctx, groupID, actorID, accessRead)
	if err != nil {
		return err
	}
	isPrivileged := acc.rank >= rankModerator
	err = s.store.DeleteGroupPostComment(ctx, groupID, postID, commentID, actorID.String(), isPrivileged)
	if err != nil {
		return err
	}

	// Fire-and-forget: publish realtime delete event, naming the actor as
	// the post knows them (the alias for the anonymous author).
	post, _ := s.store.GetGroupPostV2(ctx, postID)
	publicID := s.publicActorID(post, actorID)
	go func() {
		s.publishEvent(func() error {
			return s.producer.PublishGroupPostCommentDeleted(context.Background(), groupID, postID, commentID, publicID)
		})
	}()
	return nil
}
