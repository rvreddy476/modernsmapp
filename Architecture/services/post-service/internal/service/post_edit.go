package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Edit after publish (MTube, 2026-09-27; Creator Hub widening 2026-09-28).

	  PATCH /v1/posts/:postId   owner only; any subset of
	    title, text, tags, hashtags, category, visibility, cover_media_id,
	    allow_download, no_comments, made_for_kids, language, and (Creator
	    Hub, hub_settings.go) paid_promotion, altered_content, license,
	    allow_embedding, recording_date, recording_location, remix_setting,
	    comment_moderation, comment_access, notify_subscribers,
	    age_restricted, hide_like_count, default_comment_sort,
	    related_post_id.
	    No media change. Returns the post detail as the owner sees it.

	  POST /v1/uploads/bulk     {post_ids, patch: {...}} (BulkPatch below)
	    owner of ALL ids or 403; each post validated and written on its
	    own (one audit row each), so one bad post never undoes the others;
	    per-id outcome.

	  POST /v1/uploads/bulk-delete {post_ids}
	    each id through DeletePost (the single delete's audit and cascade);
	    per-id outcome.

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

	// Creator Hub (2026-09-28, hub_settings.go). RecordingDate and
	// RelatedPostID take "" to clear. NotifySubscribers changes the stored
	// distribution policy of a post that is not published yet (scheduled)
	// and is accepted and ignored on a published one.
	PaidPromotion      *bool
	AlteredContent     *bool
	License            *string
	AllowEmbedding     *bool
	RecordingDate      *string
	RecordingLocation  *string
	RemixSetting       *string
	CommentModeration  *string
	CommentAccess      *string
	NotifySubscribers  *bool
	AgeRestricted      *bool
	HideLikeCount      *bool
	DefaultCommentSort *string
	RelatedPostID      *string
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

	updated, err := s.applyPostEdit(ctx, callerID, current, in, "post.edit")
	if err != nil {
		return nil, err
	}

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

// applyPostEdit validates in against current (already established as the
// caller's) and writes it with one audit row named action. The single
// PATCH and every post of a bulk edit go through here.
func (s *Service) applyPostEdit(ctx context.Context, callerID uuid.UUID, current *postgres.Post, in PostEditInput, action string) (*postgres.Post, error) {
	patch, err := s.buildPostEditPatch(ctx, current, in)
	if err != nil {
		return nil, err
	}
	patch.AuditAction = action
	updated, err := s.postEdits.UpdatePostFields(ctx, current.ID, callerID, patch)
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
	s.InvalidatePostBodyCache(ctx, current.ID)
	s.invalidateFeedHydration(ctx, callerID, current.ID)
	return updated, nil
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

	if err := s.buildHubSettingsPatch(ctx, current, in, &patch); err != nil {
		return patch, err
	}
	return patch, nil
}

// buildHubSettingsPatch validates the Creator Hub fields (hub_settings.go)
// into patch. Pure apart from the related post's lookup.
func (s *Service) buildHubSettingsPatch(ctx context.Context, current *postgres.Post, in PostEditInput, patch *postgres.PostEditPatch) error {
	patch.PaidPromotion = in.PaidPromotion
	patch.AlteredContent = in.AlteredContent
	patch.AllowEmbedding = in.AllowEmbedding
	patch.AgeRestricted = in.AgeRestricted
	patch.HideLikeCount = in.HideLikeCount

	enum := func(raw *string, allowed []string, bad error, dst **string) error {
		if raw == nil {
			return nil
		}
		v, ok := normalizeEnum(*raw, allowed)
		if !ok {
			return bad
		}
		*dst = &v
		return nil
	}
	if err := enum(in.License, PostLicenses, ErrInvalidLicense, &patch.License); err != nil {
		return err
	}
	if err := enum(in.RemixSetting, PostRemixSettings, ErrInvalidRemixSetting, &patch.RemixSetting); err != nil {
		return err
	}
	if err := enum(in.CommentModeration, PostCommentModerations, ErrInvalidCommentModeration, &patch.CommentModeration); err != nil {
		return err
	}
	if err := enum(in.CommentAccess, PostCommentAccesses, ErrInvalidCommentAccess, &patch.CommentAccess); err != nil {
		return err
	}
	if err := enum(in.DefaultCommentSort, PostCommentSorts, ErrInvalidCommentSort, &patch.DefaultCommentSort); err != nil {
		return err
	}
	if in.RecordingDate != nil {
		if strings.TrimSpace(*in.RecordingDate) == "" {
			patch.ClearRecordingDate = true
		} else {
			d, err := parseRecordingDate(*in.RecordingDate, s.clock())
			if err != nil {
				return err
			}
			patch.RecordingDate = &d
		}
	}
	if in.RecordingLocation != nil {
		loc, err := normalizeRecordingLocation(*in.RecordingLocation)
		if err != nil {
			return err
		}
		patch.RecordingLocation = &loc
	}
	if in.RelatedPostID != nil {
		if err := s.resolveRelatedPost(ctx, current, *in.RelatedPostID, patch); err != nil {
			return err
		}
	}
	if in.NotifySubscribers != nil {
		s.buildNotifySubscribersPatch(current, *in.NotifySubscribers, patch)
	}
	return nil
}

// resolveRelatedPost: "" clears; otherwise the id must name one of the
// owner's own live posts (RELATED_NOT_FOUND for a malformed id, a missing
// or deleted post, or someone else's — one code, so the field cannot probe
// other accounts' posts) and not the post itself (RELATED_SELF).
func (s *Service) resolveRelatedPost(ctx context.Context, current *postgres.Post, raw string, patch *postgres.PostEditPatch) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		patch.ClearRelatedPost = true
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return ErrRelatedNotFound
	}
	if id == current.ID {
		return ErrRelatedSelf
	}
	related, err := s.postEdits.GetPost(ctx, id)
	if err != nil {
		return fmt.Errorf("load related post: %w", err)
	}
	if related == nil || related.DeletedAt != nil || related.AuthorID != current.AuthorID {
		return ErrRelatedNotFound
	}
	patch.RelatedPostID = &id
	return nil
}

// buildNotifySubscribersPatch folds notify_subscribers into the stored
// distribution policy of a post that has not published yet. A published
// post already announced itself (or did not), so the field is accepted and
// changes nothing there. main_feed keeps its resolved value, so writing a
// policy onto a legacy (NULL) row cannot change feed placement.
func (s *Service) buildNotifySubscribersPatch(current *postgres.Post, notify bool, patch *postgres.PostEditPatch) {
	if current.PublishAt == nil {
		return
	}
	policy, err := ParseDistributionPolicy(current.Distribution)
	if err != nil {
		policy = nil
	}
	resolved := ResolveDistribution(policy)
	if resolved.NotifySubscribers == notify {
		return
	}
	mainFeed := resolved.MainFeed
	next := &DistributionPolicy{Version: distributionPolicyVersion, MainFeed: &mainFeed, NotifySubscribers: &notify}
	stored, err := MarshalPolicy(next)
	if err != nil {
		return
	}
	patch.Distribution = stored
	patch.DistributionChange = &postgres.PostEditChange{From: resolved.NotifySubscribers, To: notify}
	postID, authorID, contentType := current.ID, current.AuthorID, current.ContentType
	patch.DistributionEvent = func(rev int64) (string, interface{}) {
		return events.PostDistributionUpdated, events.PostDistributionUpdatedPayload{
			PostID:            postID.String(),
			AuthorID:          authorID.String(),
			ContentType:       contentType,
			MainFeed:          mainFeed,
			NotifySubscribers: notify,
			DistributionRev:   rev,
			UpdatedAt:         time.Now().UTC(),
		}
	}
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

// BulkPatch is POST /v1/uploads/bulk's patch: any subset of the bulk-
// editable fields (title and description are not). Tags go with TagsMode:
// "add" (the default) unions them into each post's tags, "replace" sets
// them, "remove" takes them out.
type BulkPatch struct {
	Visibility         *string
	Category           *string
	Language           *string
	MadeForKids        *bool
	AgeRestricted      *bool
	NoComments         *bool
	CommentModeration  *string
	CommentAccess      *string
	DefaultCommentSort *string
	AllowEmbedding     *bool
	License            *string
	RemixSetting       *string
	RecordingDate      *string
	HideLikeCount      *bool
	AlteredContent     *bool
	PaidPromotion      *bool
	Tags               *[]string
	TagsMode           string
}

// Bulk tag modes.
const (
	BulkTagsAdd     = "add"
	BulkTagsReplace = "replace"
	BulkTagsRemove  = "remove"
)

var (
	// ErrBulkEmptyPatch: a bulk request that changes nothing.
	ErrBulkEmptyPatch = errors.New("patch must set at least one field")
	// ErrBulkTagsMode: tags_mode outside add | replace | remove.
	ErrBulkTagsMode = errors.New("tags_mode must be add, replace or remove")
)

// BulkEditInput is POST /v1/uploads/bulk.
type BulkEditInput struct {
	PostIDs []uuid.UUID
	Patch   BulkPatch
}

// BulkOutcome is one id's result from the bulk routes: ok, or the error
// code the single route would have answered for that post.
type BulkOutcome = postgres.BulkVisibilityOutcome

// Empty reports a patch that sets no field (422 INVALID_REQUEST).
func (p BulkPatch) Empty() bool {
	return p.Visibility == nil && p.Category == nil && p.Language == nil && p.MadeForKids == nil &&
		p.AgeRestricted == nil && p.NoComments == nil && p.CommentModeration == nil && p.CommentAccess == nil &&
		p.DefaultCommentSort == nil && p.AllowEmbedding == nil && p.License == nil && p.RemixSetting == nil &&
		p.RecordingDate == nil && p.HideLikeCount == nil && p.AlteredContent == nil && p.PaidPromotion == nil &&
		p.Tags == nil
}

// editInput is the patch as a PATCH body for one post: tags resolved
// against that post's current tags by TagsMode.
func (p BulkPatch) editInput(current *postgres.Post) PostEditInput {
	in := PostEditInput{
		Visibility: p.Visibility, Category: p.Category, Language: p.Language, MadeForKids: p.MadeForKids,
		AgeRestricted: p.AgeRestricted, NoComments: p.NoComments, CommentModeration: p.CommentModeration,
		CommentAccess: p.CommentAccess, DefaultCommentSort: p.DefaultCommentSort, AllowEmbedding: p.AllowEmbedding,
		License: p.License, RemixSetting: p.RemixSetting, RecordingDate: p.RecordingDate,
		HideLikeCount: p.HideLikeCount, AlteredContent: p.AlteredContent, PaidPromotion: p.PaidPromotion,
	}
	if p.Tags != nil {
		tags := bulkTags(current.Tags, *p.Tags, p.TagsMode)
		in.Tags = &tags
	}
	return in
}

// bulkTags applies a tags_mode to one post's tags (case-insensitive match
// for remove). Normalization, dedupe and the 20 x 50 caps are the edit
// path's (normalizeEditTags), per post.
func bulkTags(current, given []string, mode string) []string {
	switch mode {
	case BulkTagsReplace:
		return append([]string{}, given...)
	case BulkTagsRemove:
		drop := make(map[string]struct{}, len(given))
		for _, t := range given {
			drop[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
		}
		out := []string{}
		for _, t := range current {
			if _, gone := drop[strings.ToLower(strings.TrimSpace(t))]; !gone {
				out = append(out, t)
			}
		}
		return out
	default: // add
		return append(append([]string{}, current...), given...)
	}
}

// validateBulkPatch refuses, for the whole request, what is wrong whatever
// the post: an empty patch, a bad tags_mode, and every post-independent
// value (visibility, language, the enums, recording date). Category (the
// taxonomy depends on the post's kind) and the tag caps (they depend on the
// post's tags) are per post.
func (s *Service) validateBulkPatch(p *BulkPatch) error {
	if p.Empty() {
		return ErrBulkEmptyPatch
	}
	if p.Tags != nil {
		mode := strings.ToLower(strings.TrimSpace(p.TagsMode))
		if mode == "" {
			mode = BulkTagsAdd
		}
		switch mode {
		case BulkTagsAdd, BulkTagsReplace, BulkTagsRemove:
			p.TagsMode = mode
		default:
			return ErrBulkTagsMode
		}
	}
	if p.Visibility != nil && !editVisibilityAllowed(strings.ToLower(strings.TrimSpace(*p.Visibility))) {
		return ErrInvalidVisibility
	}
	if p.Language != nil {
		if lang := strings.TrimSpace(*p.Language); lang != "" && !validLanguageTag(lang) {
			return ErrInvalidLanguage
		}
	}
	checks := []struct {
		raw     *string
		allowed []string
		bad     error
	}{
		{p.License, PostLicenses, ErrInvalidLicense},
		{p.RemixSetting, PostRemixSettings, ErrInvalidRemixSetting},
		{p.CommentModeration, PostCommentModerations, ErrInvalidCommentModeration},
		{p.CommentAccess, PostCommentAccesses, ErrInvalidCommentAccess},
		{p.DefaultCommentSort, PostCommentSorts, ErrInvalidCommentSort},
	}
	for _, c := range checks {
		if c.raw != nil {
			if _, ok := normalizeEnum(*c.raw, c.allowed); !ok {
				return c.bad
			}
		}
	}
	if p.RecordingDate != nil && strings.TrimSpace(*p.RecordingDate) != "" {
		if _, err := parseRecordingDate(*p.RecordingDate, s.clock()); err != nil {
			return err
		}
	}
	return nil
}

// ownedBulkIDs dedupes, caps and establishes that callerID owns every id
// (403 ErrNotPostAuthor otherwise; an unknown id counts as not owned so the
// refusal discloses nothing).
func (s *Service) ownedBulkIDs(ctx context.Context, callerID uuid.UUID, raw []uuid.UUID) ([]uuid.UUID, error) {
	if s.postEdits == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	if callerID == uuid.Nil {
		return nil, ErrNotPostAuthor
	}
	ids := dedupeUUIDs(raw)
	if len(ids) == 0 {
		return nil, ErrBulkNothing
	}
	if len(ids) > MaxBulkPostIDs {
		return nil, ErrBulkTooMany
	}
	authors, err := s.postEdits.PostAuthorsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("bulk owners: %w", err)
	}
	for _, id := range ids {
		if authors[id] != callerID {
			return nil, ErrNotPostAuthor
		}
	}
	return ids, nil
}

// BulkEditUploads applies one patch to every listed post. Request-level
// refusals come first (empty patch, bad tags_mode, a value no post may take
// -> 422; any id not the caller's -> 403); after that each post is
// validated and written in its own transaction with its own
// 'post.bulk_edit' audit row, and its outcome carries the code the single
// PATCH would have answered (INVALID_CATEGORY, INVALID_TAGS, NOT_FOUND ...).
func (s *Service) BulkEditUploads(ctx context.Context, callerID uuid.UUID, in BulkEditInput) ([]BulkOutcome, error) {
	if err := s.validateBulkPatch(&in.Patch); err != nil {
		return nil, err
	}
	ids, err := s.ownedBulkIDs(ctx, callerID, in.PostIDs)
	if err != nil {
		return nil, err
	}
	out := make([]BulkOutcome, 0, len(ids))
	for _, id := range ids {
		outcome := BulkOutcome{ID: id, OK: true}
		if err := s.bulkEditOne(ctx, callerID, id, in.Patch); err != nil {
			outcome.OK = false
			_, outcome.Error = PostEditErrorStatus(err)
		}
		out = append(out, outcome)
	}
	return out, nil
}

func (s *Service) bulkEditOne(ctx context.Context, callerID, postID uuid.UUID, patch BulkPatch) error {
	current, err := s.postEdits.GetPost(ctx, postID)
	if err != nil {
		return err
	}
	if current == nil {
		return ErrPostNotFound
	}
	if current.AuthorID != callerID {
		return ErrNotPostAuthor
	}
	_, err = s.applyPostEdit(ctx, callerID, current, patch.editInput(current), "post.bulk_edit")
	return err
}

// BulkDeleteUploads is POST /v1/uploads/bulk-delete: every id through
// DeletePost (the single delete's ownership check, soft delete, cascade and
// outbox events) with a per-id outcome (NOT_FOUND, FORBIDDEN, ...).
func (s *Service) BulkDeleteUploads(ctx context.Context, callerID uuid.UUID, postIDs []uuid.UUID) ([]BulkOutcome, error) {
	if callerID == uuid.Nil {
		return nil, ErrNotPostAuthor
	}
	ids := dedupeUUIDs(postIDs)
	if len(ids) == 0 {
		return nil, ErrBulkNothing
	}
	if len(ids) > MaxBulkPostIDs {
		return nil, ErrBulkTooMany
	}
	out := make([]BulkOutcome, 0, len(ids))
	for _, id := range ids {
		outcome := BulkOutcome{ID: id, OK: true}
		if err := s.deleteOne(ctx, id, callerID); err != nil {
			outcome.OK = false
			_, outcome.Error = PostEditErrorStatus(err)
		}
		out = append(out, outcome)
	}
	return out, nil
}

// deleteOne is DeletePost; bulkDelete replaces it in tests.
func (s *Service) deleteOne(ctx context.Context, postID, callerID uuid.UUID) error {
	if s.bulkDelete != nil {
		return s.bulkDelete(ctx, postID, callerID)
	}
	return s.DeletePost(ctx, postID, callerID)
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
