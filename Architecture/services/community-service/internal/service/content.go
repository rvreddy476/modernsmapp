package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/atpost/community-service/internal/store"
	"github.com/google/uuid"
)

// Every community route that used to call h.svc.Store() from the handler
// now comes through a method here, and each one opens with the access gate
// (access.go). The handlers keep parsing and status mapping only (R-L1).

// ── Posts ───────────────────────────────────────────────────────────────

// CreatePostParams is a new post's content. Community, space and author come
// from the path and the caller, never from the body.
type CreatePostParams struct {
	ContentType  string
	Title        *string
	Body         *string
	TypePayload  json.RawMessage
	Attachments  json.RawMessage
	Tags         []string
	ParentPostID *uuid.UUID
}

// ErrInvalidParent: a reply must answer a published top-level post in the
// same space.
var ErrInvalidParent = errors.New("invalid: parent_post_id is not a top-level post in this space")

// CreateCommunityPost writes a post into a space of the PATH community. The
// space must belong to it (K-M2: a post could be written into another
// community's space by id and was listed there), must not be quarantined,
// and a reply's parent must be a top-level post of the same space.
func (s *Service) CreateCommunityPost(ctx context.Context, communityID, spaceID, actorID uuid.UUID, params CreatePostParams) (*store.CommunityPost, error) {
	acc, err := s.authorize(ctx, communityID, &actorID, accessWrite)
	if err != nil {
		return nil, err
	}
	if _, err := s.space(ctx, acc, spaceID, true); err != nil {
		return nil, err
	}

	depth := 0
	if params.ParentPostID != nil {
		parent, err := s.post(ctx, communityID, *params.ParentPostID)
		if errors.Is(err, ErrPostNotFound) {
			return nil, ErrInvalidParent
		}
		if err != nil {
			return nil, err
		}
		if parent.SpaceID != spaceID || parent.ParentPostID != nil {
			return nil, ErrInvalidParent
		}
		depth = 1 // max 2-level threading
	}

	ct := params.ContentType
	if ct == "" {
		ct = "text"
	}
	tp := params.TypePayload
	if tp == nil {
		tp = json.RawMessage(`{}`)
	}
	att := params.Attachments
	if att == nil {
		att = json.RawMessage(`[]`)
	}

	post := &store.CommunityPost{
		CommunityID:  communityID,
		SpaceID:      spaceID,
		AuthorID:     actorID.String(),
		ContentType:  ct,
		Title:        params.Title,
		Body:         params.Body,
		TypePayload:  tp,
		Attachments:  att,
		Tags:         params.Tags,
		ParentPostID: params.ParentPostID,
		ThreadDepth:  depth,
	}
	if err := s.store.CreateCommunityPost(ctx, post); err != nil {
		return nil, fmt.Errorf("failed to create post: %w", err)
	}
	return post, nil
}

// GetCommunityPost reads one published post of the PATH community. Before,
// the post was looked up by id alone and gated on ITS community, so any
// community id in the path served any post.
func (s *Service) GetCommunityPost(ctx context.Context, communityID, postID uuid.UUID, viewerID *uuid.UUID) (*store.CommunityPost, error) {
	if _, err := s.authorize(ctx, communityID, viewerID, accessRead); err != nil {
		return nil, err
	}
	return s.post(ctx, communityID, postID)
}

// ListCommunityPosts lists the community's feed, or one space of it. The
// space branch used to run before any gate and ignore the community id
// (K-M1): one space id read every post in a private space.
func (s *Service) ListCommunityPosts(ctx context.Context, communityID uuid.UUID, spaceID *uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]store.CommunityPost, error) {
	acc, err := s.authorize(ctx, communityID, viewerID, accessRead)
	if err != nil {
		return nil, err
	}
	if spaceID != nil {
		if _, err := s.space(ctx, acc, *spaceID, false); err != nil {
			return nil, err
		}
		return s.store.ListSpacePosts(ctx, communityID, *spaceID, limit, offset)
	}
	return s.store.ListCommunityPosts(ctx, communityID, limit, offset)
}

// ListFeaturedPosts lists the featured posts (K-M1: it was ungated).
func (s *Service) ListFeaturedPosts(ctx context.Context, communityID uuid.UUID, viewerID *uuid.UUID) ([]store.CommunityPost, error) {
	if _, err := s.authorize(ctx, communityID, viewerID, accessRead); err != nil {
		return nil, err
	}
	return s.store.ListFeaturedPosts(ctx, communityID, 10)
}

// DeleteCommunityPost: the author deletes their own post, moderators and
// above any post, of the PATH community only.
func (s *Service) DeleteCommunityPost(ctx context.Context, communityID, postID, actorID uuid.UUID) error {
	acc, err := s.authorize(ctx, communityID, &actorID, accessRead)
	if err != nil {
		return err
	}
	p, err := s.post(ctx, communityID, postID)
	if err != nil {
		return err
	}
	if p.AuthorID != actorID.String() && !isAtLeast(acc.role, "moderator") {
		return ErrInsufficientRole
	}
	return s.store.DeleteCommunityPost(ctx, communityID, postID)
}

// FeaturePost and PinCommunityPost are moderation actions on a post of the
// PATH community. The role used to be checked on the post's own community,
// which made the path id decorative.
func (s *Service) FeaturePost(ctx context.Context, communityID, postID, actorID uuid.UUID, featured bool) error {
	if _, err := s.moderatePost(ctx, communityID, postID, actorID); err != nil {
		return err
	}
	return s.store.MarkFeatured(ctx, communityID, postID, featured)
}

func (s *Service) PinCommunityPost(ctx context.Context, communityID, postID, actorID uuid.UUID) error {
	if _, err := s.moderatePost(ctx, communityID, postID, actorID); err != nil {
		return err
	}
	return s.store.PinCommunityPost(ctx, communityID, postID)
}

func (s *Service) moderatePost(ctx context.Context, communityID, postID, actorID uuid.UUID) (*store.CommunityPost, error) {
	if _, err := s.authorizeRole(ctx, communityID, actorID, "moderator"); err != nil {
		return nil, err
	}
	return s.post(ctx, communityID, postID)
}

// AcceptAnswer: only the question's author, and the answer must be a reply
// to that question (the store's WHERE holds this too).
func (s *Service) AcceptAnswer(ctx context.Context, communityID, postID, answerID, actorID uuid.UUID) error {
	if _, err := s.authorize(ctx, communityID, &actorID, accessRead); err != nil {
		return err
	}
	p, err := s.post(ctx, communityID, postID)
	if err != nil {
		return err
	}
	if p.AuthorID != actorID.String() {
		return ErrNotPostAuthor
	}
	if err := s.store.AcceptAnswer(ctx, communityID, postID, answerID); errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("invalid: answer_id is not a reply to this post")
	} else if err != nil {
		return err
	}
	return nil
}

// ── Engagement ──────────────────────────────────────────────────────────

// engagementGate is the one gate for spark, stash and view: a signed-in
// viewer who may read the community, on a published post of it. These ran
// with no gate at all, and stash and view with an empty user id (K-M2).
func (s *Service) engagementGate(ctx context.Context, communityID, postID uuid.UUID, viewerID uuid.UUID) error {
	if viewerID == uuid.Nil {
		return ErrNotCommunityMember
	}
	if _, err := s.authorize(ctx, communityID, &viewerID, accessRead); err != nil {
		return err
	}
	_, err := s.post(ctx, communityID, postID)
	return err
}

func (s *Service) SparkCommunityPost(ctx context.Context, communityID, postID, viewerID uuid.UUID, isSupernova bool) error {
	if err := s.engagementGate(ctx, communityID, postID, viewerID); err != nil {
		return err
	}
	return s.store.SparkCommunityPost(ctx, postID, viewerID.String(), isSupernova)
}

func (s *Service) StashCommunityPost(ctx context.Context, communityID, postID, viewerID uuid.UUID) error {
	if err := s.engagementGate(ctx, communityID, postID, viewerID); err != nil {
		return err
	}
	return s.store.StashCommunityPost(ctx, postID, viewerID.String())
}

func (s *Service) RecordCommunityPostView(ctx context.Context, communityID, postID, viewerID uuid.UUID) error {
	if err := s.engagementGate(ctx, communityID, postID, viewerID); err != nil {
		return err
	}
	return s.store.RecordCommunityPostView(ctx, postID, viewerID.String())
}

// ── Wiki ────────────────────────────────────────────────────────────────

func (s *Service) ListWikiPages(ctx context.Context, communityID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]store.WikiPage, error) {
	if _, err := s.authorize(ctx, communityID, viewerID, accessRead); err != nil {
		return nil, err
	}
	return s.store.ListWikiPages(ctx, communityID, limit, offset)
}

func (s *Service) GetWikiPage(ctx context.Context, communityID uuid.UUID, slug string, viewerID *uuid.UUID) (*store.WikiPage, error) {
	if _, err := s.authorize(ctx, communityID, viewerID, accessRead); err != nil {
		return nil, err
	}
	page, err := s.store.GetWikiPage(ctx, communityID, slug)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrWikiNotFound
	}
	return page, err
}

// Wiki pages are durable shared content: moderators and above write them.
func (s *Service) CreateWikiPage(ctx context.Context, communityID, actorID uuid.UUID, title, slug, content string, category *string) (*store.WikiPage, error) {
	if _, err := s.authorizeRole(ctx, communityID, actorID, "moderator"); err != nil {
		return nil, err
	}
	page := &store.WikiPage{
		CommunityID: communityID,
		Title:       title,
		Slug:        slug,
		Content:     content,
		Category:    category,
		CreatedBy:   actorID.String(),
	}
	if err := s.store.CreateWikiPage(ctx, page); err != nil {
		return nil, fmt.Errorf("failed to create wiki page: %w", err)
	}
	return page, nil
}

func (s *Service) UpdateWikiPage(ctx context.Context, communityID, actorID uuid.UUID, slug, title, content string) error {
	if _, err := s.authorizeRole(ctx, communityID, actorID, "moderator"); err != nil {
		return err
	}
	page, err := s.store.GetWikiPage(ctx, communityID, slug)
	if errors.Is(err, store.ErrNotFound) {
		return ErrWikiNotFound
	}
	if err != nil {
		return err
	}
	if err := s.store.UpdateWikiPage(ctx, communityID, page.ID, title, content, "", actorID.String()); errors.Is(err, store.ErrNotFound) {
		return ErrWikiNotFound
	} else if err != nil {
		return err
	}
	return nil
}

// ── Events ──────────────────────────────────────────────────────────────

// CreateEvent: moderators and above (K-M2: any signed-in user could create an
// event in any community). The event's community and creator are set here.
func (s *Service) CreateEvent(ctx context.Context, communityID, actorID uuid.UUID, e *store.CommunityEvent) error {
	if _, err := s.authorizeRole(ctx, communityID, actorID, "moderator"); err != nil {
		return err
	}
	e.CommunityID = communityID
	e.CreatorID = actorID
	if err := s.store.CreateEvent(ctx, e); err != nil {
		return fmt.Errorf("failed to create event: %w", err)
	}
	return nil
}

// ListEvents: one page, gated (K-M1: ungated and unbounded).
func (s *Service) ListEvents(ctx context.Context, communityID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]store.CommunityEvent, error) {
	if _, err := s.authorize(ctx, communityID, viewerID, accessRead); err != nil {
		return nil, err
	}
	return s.store.ListEvents(ctx, communityID, limit, offset)
}
