package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Edit after publish (MTube, 2026-09-27).

	  PATCH /v1/posts/:postId   owner only; any subset of
	    title, text, tags, hashtags, category, visibility, cover_media_id,
	    allow_download, no_comments, made_for_kids, language.
	    No media change. Returns the post detail as the owner sees it.

	  POST /v1/uploads/bulk     {post_ids, patch: {visibility}}
	    owner of ALL ids or 403; per-id outcome.

	  GET  /v1/posts/me/summary {videos, shorts, live, collections, followers}

	Every value is validated here, with the same rules the create path
	applies (taxonomy for the post's kind, the create visibility enum, the
	title / text ceilings, the explicit-hashtag alphabet, media ownership +
	kind for the cover), so an edit can never produce a post the composer
	could not have created. The store writes the audit row and bumps the
	search revision in the same transaction (store/postgres/post_edit.go).
*/

// PostVisibilities is the enum the create path accepts and the only one an
// edit may set ('staged' is the moderation pipeline's, never the owner's).
var PostVisibilities = []string{"public", "followers", "private", "unlisted"}

var (
	// ErrInvalidVisibility: a visibility outside PostVisibilities.
	ErrInvalidVisibility = fmt.Errorf("visibility must be one of %s", strings.Join(PostVisibilities, ", "))
	// ErrInvalidLanguage: a language tag that is not a short BCP-47-ish code.
	ErrInvalidLanguage = errors.New("language must be a 2-8 character language code")
	// ErrTooManyTags / ErrTagTooLong mirror the create route's binding caps
	// (max 20 tags of at most 50 characters).
	ErrTooManyTags = errors.New("a post may carry at most 20 tags")
	ErrTagTooLong  = errors.New("a tag may be at most 50 characters")
	// ErrBulkTooMany: POST /v1/uploads/bulk accepts at most MaxBulkPostIDs.
	ErrBulkTooMany = fmt.Errorf("at most %d post ids per bulk request", MaxBulkPostIDs)
	// ErrBulkNothing: an empty post_ids list.
	ErrBulkNothing = errors.New("post_ids is required")
)

// MaxBulkPostIDs caps one bulk request.
const MaxBulkPostIDs = 100

// postEditStore is the slice of the Postgres store the edit flows need, an
// interface so the owner guard and the validation can be tested without a
// database (the same way channelStore and videoAuthoringStore are).
type postEditStore interface {
	GetPost(ctx context.Context, id uuid.UUID) (*postgres.Post, error)
	UpdatePostFields(ctx context.Context, postID, actorID uuid.UUID, patch postgres.PostEditPatch) (*postgres.Post, error)
	BatchGetMediaOwnership(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.MediaOwnership, error)
	PostAuthorsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]uuid.UUID, error)
	BulkSetVisibility(ctx context.Context, actorID uuid.UUID, postIDs []uuid.UUID, visibility string) []postgres.BulkVisibilityOutcome
	CountCreatorContent(ctx context.Context, authorID uuid.UUID) (postgres.CreatorCounts, error)
	UpdatePostCategory(ctx context.Context, postID uuid.UUID, category string) error
}

// PostEditInput is the PATCH body after JSON decoding: nil = untouched.
type PostEditInput struct {
	Title         *string
	Text          *string
	Tags          *[]string
	Hashtags      *[]string
	Category      *string
	Visibility    *string
	CoverMediaID  *uuid.UUID
	AllowDownload *bool
	NoComments    *bool
	MadeForKids   *bool
	Language      *string
}

// UpdatePost validates and applies an owner edit, then returns the post
// detail as the owner sees it. Order of refusals: 404 for a post that does
// not exist (or is deleted), 403 for someone else's, then the field
// validations, then the cover's media authority.
func (s *Service) UpdatePost(ctx context.Context, callerID, postID uuid.UUID, in PostEditInput) (*PostDetail, error) {
	if s.postEdits == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	if callerID == uuid.Nil {
		return nil, ErrNotPostAuthor
	}
	current, err := s.postEdits.GetPost(ctx, postID)
	if err != nil {
		return nil, fmt.Errorf("load post for edit: %w", err)
	}
	if current == nil {
		return nil, ErrPostNotFound
	}
	if current.AuthorID != callerID {
		return nil, ErrNotPostAuthor
	}

	patch, err := s.buildPostEditPatch(ctx, current, in)
	if err != nil {
		return nil, err
	}

	updated, err := s.postEdits.UpdatePostFields(ctx, postID, callerID, patch)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil, ErrPostNotFound
		case errors.Is(err, postgres.ErrPostEditNotOwned):
			return nil, ErrNotPostAuthor
		}
		return nil, err
	}
	// Tier 1b: every edited field is in the cached body.
	s.InvalidatePostBodyCache(ctx, postID)
	s.invalidateFeedHydration(ctx, callerID, postID)

	if s.pgStore == nil {
		// Test rig without a full store: answer from the row.
		return &PostDetail{Post: updated}, nil
	}
	detail, err := s.GetPost(ctx, postID, &callerID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return &PostDetail{Post: updated}, nil
	}
	return detail, nil
}

// buildPostEditPatch validates every supplied field against the create
// rules and turns the input into the store patch. Pure apart from the
// cover's media-authority lookup.
func (s *Service) buildPostEditPatch(ctx context.Context, current *postgres.Post, in PostEditInput) (postgres.PostEditPatch, error) {
	var patch postgres.PostEditPatch

	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if err := ValidateTitle(current.ContentType, title); err != nil {
			return patch, err
		}
		patch.Title = &title
	}
	if in.Text != nil {
		if err := ValidatePostContent(*in.Text, len(current.Media)); err != nil {
			return patch, err
		}
		text := *in.Text
		patch.Text = &text
	}
	if in.Tags != nil {
		tags, err := normalizeEditTags(*in.Tags)
		if err != nil {
			return patch, err
		}
		patch.Tags = &tags
	}
	// Hashtags: the stored list is the union of what the text carries and
	// the explicit field, exactly as on create. When only the text changed
	// the explicit list is not known any more, so the previously stored
	// tags are kept alongside the re-parsed ones; a client that wants the
	// list exact sends `hashtags` with the text.
	if in.Hashtags != nil || in.Text != nil {
		text := current.Text
		if in.Text != nil {
			text = *in.Text
		}
		parsed := extractHashtags(text)
		if len(parsed) > 20 {
			parsed = parsed[:20]
		}
		explicit := current.Hashtags
		if in.Hashtags != nil {
			normalized, err := NormalizeExplicitHashtags(*in.Hashtags)
			if err != nil {
				return patch, err
			}
			explicit = normalized
		}
		merged := mergeTags(parsed, explicit, maxMergedHashtags)
		if s.pgStore != nil {
			merged = s.filterBlockedHashtags(ctx, merged)
		}
		if merged == nil {
			merged = []string{}
		}
		patch.Hashtags = &merged
	}
	if in.Category != nil {
		category, err := NormalizeCategoryFor(current.ContentType, *in.Category)
		if err != nil {
			return patch, err
		}
		patch.Category = &category
	}
	if in.Visibility != nil {
		v := strings.ToLower(strings.TrimSpace(*in.Visibility))
		if !editVisibilityAllowed(v) {
			return patch, ErrInvalidVisibility
		}
		patch.Visibility = &v
	}
	if in.Language != nil {
		lang := strings.TrimSpace(*in.Language)
		if lang != "" && !validLanguageTag(lang) {
			return patch, ErrInvalidLanguage
		}
		patch.Language = &lang
	}
	if in.CoverMediaID != nil {
		if err := s.verifyCoverMedia(ctx, current.AuthorID, *in.CoverMediaID); err != nil {
			return patch, err
		}
		id := *in.CoverMediaID
		patch.CoverMediaID = &id
	}
	patch.AllowDownload = in.AllowDownload
	patch.NoComments = in.NoComments
	patch.MadeForKids = in.MadeForKids
	return patch, nil
}

// verifyCoverMedia is the cover's media authority: the asset must exist,
// belong to the author, be confirmed and not refused, and be an IMAGE — the
// same per-asset decision the create path makes (checkMediaAuthority with
// an image post's kind rule), so a video or someone else's asset can never
// become a cover through an edit.
func (s *Service) verifyCoverMedia(ctx context.Context, authorID, mediaID uuid.UUID) error {
	ownership, err := s.postEdits.BatchGetMediaOwnership(ctx, []uuid.UUID{mediaID})
	if err != nil {
		return fmt.Errorf("verify cover media: %w", err)
	}
	m, ok := ownership[mediaID]
	return checkMediaAuthority(mediaID, authorID, m, ok, "post", postTypeImage)
}

func editVisibilityAllowed(v string) bool {
	for _, allowed := range PostVisibilities {
		if v == allowed {
			return true
		}
	}
	return false
}

// validLanguageTag admits "en", "hi", "te", "pt-BR", "zh-Hant": 2-8
// characters of letters, digits and hyphens.
func validLanguageTag(lang string) bool {
	if len(lang) < 2 || len(lang) > 8 {
		return false
	}
	for _, r := range lang {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// normalizeEditTags trims, drops empties, dedupes and applies the create
// route's caps (20 × 50).
func normalizeEditTags(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, t := range raw {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if len([]rune(t)) > 50 {
			return nil, ErrTagTooLong
		}
		key := strings.ToLower(t)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	if len(out) > 20 {
		return nil, ErrTooManyTags
	}
	return out, nil
}

// SetPostCategory is PATCH /v1/videos/:id/category's taxonomy branch (the
// legacy flick|long_video values keep going to OverrideCategory): the owner
// sets posts.category to a slug the post's kind admits.
func (s *Service) SetPostCategory(ctx context.Context, callerID, postID uuid.UUID, raw string) error {
	if s.postEdits == nil {
		return ErrAuthoringStoreUnavailable
	}
	current, err := s.postEdits.GetPost(ctx, postID)
	if err != nil {
		return fmt.Errorf("load post: %w", err)
	}
	if current == nil {
		return ErrPostNotFound
	}
	if current.AuthorID != callerID {
		return ErrNotPostAuthor
	}
	category, err := NormalizeCategoryFor(current.ContentType, raw)
	if err != nil {
		return err
	}
	if category == "" {
		return ErrInvalidCategory
	}
	if err := s.postEdits.UpdatePostCategory(ctx, postID, category); err != nil {
		return err
	}
	s.InvalidatePostBodyCache(ctx, postID)
	return nil
}

// BulkVisibilityInput is POST /v1/uploads/bulk.
type BulkVisibilityInput struct {
	PostIDs    []uuid.UUID
	Visibility string
}

// BulkSetVisibility applies one visibility to every listed post. The caller
// must own ALL of them (403 ErrNotPostAuthor otherwise; an unknown id counts
// as not owned so the refusal discloses nothing). Duplicates are collapsed.
func (s *Service) BulkSetVisibility(ctx context.Context, callerID uuid.UUID, in BulkVisibilityInput) ([]postgres.BulkVisibilityOutcome, error) {
	if s.postEdits == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	if callerID == uuid.Nil {
		return nil, ErrNotPostAuthor
	}
	ids := dedupeUUIDs(in.PostIDs)
	if len(ids) == 0 {
		return nil, ErrBulkNothing
	}
	if len(ids) > MaxBulkPostIDs {
		return nil, ErrBulkTooMany
	}
	v := strings.ToLower(strings.TrimSpace(in.Visibility))
	if !editVisibilityAllowed(v) {
		return nil, ErrInvalidVisibility
	}
	authors, err := s.postEdits.PostAuthorsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("bulk visibility owners: %w", err)
	}
	for _, id := range ids {
		if authors[id] != callerID {
			return nil, ErrNotPostAuthor
		}
	}
	outcomes := s.postEdits.BulkSetVisibility(ctx, callerID, ids, v)
	for _, o := range outcomes {
		if o.OK {
			s.InvalidatePostBodyCache(ctx, o.ID)
		}
	}
	return outcomes, nil
}

func dedupeUUIDs(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// CreatorSummary is GET /v1/posts/me/summary.
type CreatorSummary struct {
	Videos      int64 `json:"videos"`
	Shorts      int64 `json:"shorts"`
	Live        int64 `json:"live"`
	Collections int64 `json:"collections"`
	Followers   int64 `json:"followers"`
}

// GetCreatorSummary counts the caller's content and reads followers from
// the channel row when there is one (subscriber_count), else 0.
func (s *Service) GetCreatorSummary(ctx context.Context, callerID uuid.UUID) (*CreatorSummary, error) {
	if s.postEdits == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	counts, err := s.postEdits.CountCreatorContent(ctx, callerID)
	if err != nil {
		return nil, fmt.Errorf("creator summary: %w", err)
	}
	out := &CreatorSummary{Videos: counts.Videos, Shorts: counts.Shorts, Live: counts.Live, Collections: counts.Collections}
	if s.channels != nil {
		if ch, err := s.channels.GetChannelByUserID(ctx, callerID); err == nil && ch != nil {
			out.Followers = int64(ch.SubscriberCount)
		}
	}
	return out, nil
}
