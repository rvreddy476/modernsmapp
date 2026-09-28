package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Creator comment tools (MTube, 2026-09-27).

	  GET    /v1/posts/:id/comments?sort=top|newest   (default newest = the old order)
	  GET    /v1/comments/inbox?status=unanswered|all&content=videos|flicks|posts|all
	                            &sort=newest|relevant&cursor=&limit=
	  POST   /v1/comments/:id/heart     DELETE  — post author only
	  PUT    /v1/comments/:id/pin       DELETE  — post author only, one per post

	Neither a heart nor a pin existed before this pass (comments carried no
	such column), so both are new rather than reused.
*/

// ErrCannotPinReply mirrors the store's refusal at the service boundary.
var ErrCannotPinReply = postgres.ErrCannotPinReply

// creatorCommentStore is the slice of the store these flows need, an
// interface so the author-only guard is testable without a database.
type creatorCommentStore interface {
	GetCommentOwnerRef(ctx context.Context, commentID uuid.UUID) (*postgres.CommentOwnerRef, error)
	SetCommentHeart(ctx context.Context, commentID uuid.UUID, on bool) error
	PinComment(ctx context.Context, postID, commentID uuid.UUID) error
	UnpinComment(ctx context.Context, commentID uuid.UUID) error
	ListCreatorInbox(ctx context.Context, q postgres.InboxQuery) ([]postgres.InboxRow, string, error)
}

// ListCommentsSortedPG is ListCommentsPG with a sort order (comments.go in
// the store): "" / "newest" is the historic order, "top" ranks by
// reaction_count, reply_count, created_at. The pinned comment leads both.
//
// The post's read decision comes first (2026-09-28): the thread of a post
// the viewer may not open is ErrPostNotVisible (404), including a private
// post unless the viewer is on its share list; an 18+ post answers the
// age refusal the detail answers.
func (s *Service) ListCommentsSortedPG(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID, cursor string, limit int, sort string) ([]postgres.Comment, string, error) {
	if err := s.singlePostRead(ctx, postID, viewerID); err != nil {
		return nil, "", err
	}
	if sort != postgres.CommentSortTop {
		sort = postgres.CommentSortNewest
	}
	return s.pgStore.ListCommentsSorted(ctx, postID, viewerID, cursor, limit, sort)
}

// requireCommentPostAuthor loads the comment and establishes that callerID
// authored the POST it sits on. COMMENT_NOT_FOUND for a comment the public
// thread does not hold; ErrNotPostAuthor for anyone but the post's author.
func (s *Service) requireCommentPostAuthor(ctx context.Context, callerID, commentID uuid.UUID) (*postgres.CommentOwnerRef, error) {
	if s.creatorComments == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	if callerID == uuid.Nil {
		return nil, ErrNotPostAuthor
	}
	ref, err := s.creatorComments.GetCommentOwnerRef(ctx, commentID)
	if err != nil {
		return nil, err
	}
	if ref.PostAuthor != callerID {
		return nil, ErrNotPostAuthor
	}
	return ref, nil
}

// CommentHeartResult is the body of POST/DELETE /v1/comments/:id/heart.
type CommentHeartResult struct {
	CommentID       uuid.UUID `json:"comment_id"`
	HeartedByAuthor bool      `json:"hearted_by_author"`
}

// SetCommentHeart hearts (on) or unhearts a comment on the caller's post.
func (s *Service) SetCommentHeart(ctx context.Context, callerID, commentID uuid.UUID, on bool) (*CommentHeartResult, error) {
	if _, err := s.requireCommentPostAuthor(ctx, callerID, commentID); err != nil {
		return nil, err
	}
	if err := s.creatorComments.SetCommentHeart(ctx, commentID, on); err != nil {
		return nil, err
	}
	return &CommentHeartResult{CommentID: commentID, HeartedByAuthor: on}, nil
}

// CommentPinResult is the body of PUT/DELETE /v1/comments/:id/pin.
type CommentPinResult struct {
	CommentID uuid.UUID `json:"comment_id"`
	PostID    uuid.UUID `json:"post_id"`
	Pinned    bool      `json:"pinned"`
}

// PinComment makes the comment the post's one pinned comment (replacing any
// other). Only a top-level comment can be pinned.
func (s *Service) PinComment(ctx context.Context, callerID, commentID uuid.UUID) (*CommentPinResult, error) {
	ref, err := s.requireCommentPostAuthor(ctx, callerID, commentID)
	if err != nil {
		return nil, err
	}
	if ref.ParentID != nil {
		return nil, ErrCannotPinReply
	}
	if err := s.creatorComments.PinComment(ctx, ref.PostID, commentID); err != nil {
		return nil, err
	}
	return &CommentPinResult{CommentID: commentID, PostID: ref.PostID, Pinned: true}, nil
}

// UnpinComment clears the pin. Idempotent.
func (s *Service) UnpinComment(ctx context.Context, callerID, commentID uuid.UUID) (*CommentPinResult, error) {
	ref, err := s.requireCommentPostAuthor(ctx, callerID, commentID)
	if err != nil {
		return nil, err
	}
	if err := s.creatorComments.UnpinComment(ctx, commentID); err != nil {
		return nil, err
	}
	return &CommentPinResult{CommentID: commentID, PostID: ref.PostID, Pinned: false}, nil
}

// Inbox query values as the handler passes them.
var (
	ErrInvalidInboxStatus  = errors.New("status must be unanswered or all")
	ErrInvalidInboxContent = errors.New("content must be videos, flicks, posts or all")
	ErrInvalidInboxSort    = errors.New("sort must be newest or relevant")
)

// InboxContentTypes maps the `content` filter onto stored content types;
// "all" / "" is nil (no filter).
func InboxContentTypes(content string) ([]string, error) {
	switch content {
	case "", "all":
		return nil, nil
	case "videos":
		return []string{"long_video", "video"}, nil
	case "flicks":
		return []string{"flick", "reel"}, nil
	case "posts":
		return []string{"post", "image"}, nil
	}
	return nil, ErrInvalidInboxContent
}

// CreatorInboxQuery is GET /v1/comments/inbox after query parsing.
type CreatorInboxQuery struct {
	Status  string
	Content string
	Sort    string
	Cursor  string
	Limit   int
}

// ListCreatorInbox pages the comments on the caller's own posts.
func (s *Service) ListCreatorInbox(ctx context.Context, callerID uuid.UUID, q CreatorInboxQuery) ([]postgres.InboxRow, string, error) {
	if s.creatorComments == nil {
		return nil, "", ErrAuthoringStoreUnavailable
	}
	status := q.Status
	switch status {
	case "":
		status = postgres.InboxStatusUnanswered
	case postgres.InboxStatusUnanswered, postgres.InboxStatusAll:
	default:
		return nil, "", ErrInvalidInboxStatus
	}
	contentTypes, err := InboxContentTypes(q.Content)
	if err != nil {
		return nil, "", err
	}
	sort := q.Sort
	switch sort {
	case "":
		sort = postgres.InboxSortNewest
	case postgres.InboxSortNewest, postgres.InboxSortRelevant:
	default:
		return nil, "", ErrInvalidInboxSort
	}
	rows, next, err := s.creatorComments.ListCreatorInbox(ctx, postgres.InboxQuery{
		AuthorID: callerID, Status: status, ContentTypes: contentTypes, Sort: sort, Cursor: q.Cursor, Limit: q.Limit,
	})
	if err != nil {
		return nil, "", fmt.Errorf("creator inbox: %w", err)
	}
	if rows == nil {
		rows = []postgres.InboxRow{}
	}
	return rows, next, nil
}
